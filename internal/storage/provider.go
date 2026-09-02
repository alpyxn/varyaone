// Package storage contains the provider-neutral object storage contract used
// by product media, attachments and generated documents.  Business metadata
// belongs in PostgreSQL; providers only own opaque bytes.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrInvalidKey            = errors.New("storage object key is invalid")
	ErrObjectNotFound        = errors.New("storage object not found")
	ErrObjectExists          = errors.New("storage object already exists")
	ErrProviderNotConfigured = errors.New("storage provider is not configured")
	ErrObjectTooLarge        = errors.New("storage object is too large")
)

// ObjectInfo describes an object without exposing provider credentials or
// backend implementation details. SHA-256 is the canonical content identity
// returned by all providers.
type ObjectInfo struct {
	Key         string    `json:"key"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	ETag        string    `json:"etag,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type PutOptions struct {
	ContentType string
	// MaxBytes is an optional per-write guard. A zero value uses the provider
	// limit. The default local provider limit is intentionally generous but
	// finite so an upload cannot fill a volume accidentally.
	MaxBytes int64
	// Overwrite is opt-in. Posted documents and uploaded media are immutable by
	// default; replacing one requires a new key/version or an explicit caller
	// decision.
	Overwrite bool
}

// StorageProvider is deliberately small so local, S3 and MinIO backends can
// be exercised by the same contract tests.
type StorageProvider interface {
	Put(context.Context, string, io.Reader, PutOptions) (ObjectInfo, error)
	Open(context.Context, string) (io.ReadCloser, ObjectInfo, error)
	Stat(context.Context, string) (ObjectInfo, error)
	Delete(context.Context, string) error
}

// PutBytes is a convenience for generated artefacts and tests. Upload paths
// should use Put so the caller can stream a request body without buffering it.
func PutBytes(ctx context.Context, provider StorageProvider, key string, payload []byte, options PutOptions) (ObjectInfo, error) {
	return provider.Put(ctx, key, bytes.NewReader(payload), options)
}

type LocalProvider struct {
	root          string
	maxObjectSize int64
	now           func() time.Time
}

const defaultMaxObjectSize int64 = 512 << 20 // 512 MiB

// NewLocalProvider creates a provider rooted at root. The root is created if
// necessary and is never derived from a user supplied object key.
func NewLocalProvider(root string) (*LocalProvider, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("%w: local storage root is required", ErrProviderNotConfigured)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve local storage root: %w", err)
	}
	if err = os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("create local storage root: %w", err)
	}
	return &LocalProvider{root: absolute, maxObjectSize: defaultMaxObjectSize, now: time.Now}, nil
}

// NewLocalStorageProvider is kept as a descriptive alias for callers wiring
// the application from configuration.
func NewLocalStorageProvider(root string) (*LocalProvider, error) {
	return NewLocalProvider(root)
}

func (p *LocalProvider) Root() string { return p.root }

func (p *LocalProvider) Put(ctx context.Context, key string, source io.Reader, options PutOptions) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	if source == nil {
		return ObjectInfo{}, errors.New("storage object source is required")
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	limit := p.maxObjectSize
	if options.MaxBytes > 0 && (limit == 0 || options.MaxBytes < limit) {
		limit = options.MaxBytes
	}
	destination, err := p.pathFor(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	if err = os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return ObjectInfo{}, fmt.Errorf("create object directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".varya-object-*")
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("create temporary object: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	_ = temporary.Chmod(0o600)

	digest := sha256.New()
	writer := io.MultiWriter(temporary, digest)
	reader := &contextReader{ctx: ctx, reader: source}
	var copied int64
	if limit > 0 {
		copied, err = io.Copy(writer, io.LimitReader(reader, limit+1))
		if err == nil && copied > limit {
			return ObjectInfo{}, fmt.Errorf("%w: %d byte sınırı aşıldı", ErrObjectTooLarge, limit)
		}
	} else {
		copied, err = io.Copy(writer, reader)
	}
	if err != nil {
		return ObjectInfo{}, err
	}
	if err = ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	if err = temporary.Sync(); err != nil {
		return ObjectInfo{}, fmt.Errorf("sync temporary object: %w", err)
	}
	if err = temporary.Close(); err != nil {
		return ObjectInfo{}, fmt.Errorf("close temporary object: %w", err)
	}
	// A hard link publishes the complete fsynced file atomically and fails if
	// another writer already owns the immutable key. Overwrites are explicit.
	if options.Overwrite {
		if err = os.Rename(temporaryName, destination); err != nil {
			return ObjectInfo{}, fmt.Errorf("publish object: %w", err)
		}
	} else if err = os.Link(temporaryName, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ObjectInfo{}, ErrObjectExists
		}
		return ObjectInfo{}, fmt.Errorf("publish object: %w", err)
	}
	created := p.now()
	if created.IsZero() {
		created = time.Now()
	}
	return ObjectInfo{
		Key:         key,
		ContentType: normalizeContentType(options.ContentType, key),
		Size:        copied,
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
		ETag:        hex.EncodeToString(digest.Sum(nil)),
		CreatedAt:   created,
	}, nil
}

func (p *LocalProvider) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, ObjectInfo{}, err
	}
	info, err := p.Stat(ctx, key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	file, err := os.Open(p.mustPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ObjectInfo{}, ErrObjectNotFound
	}
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	return file, info, nil
}

func (p *LocalProvider) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := ValidateKey(key); err != nil {
		return ObjectInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return ObjectInfo{}, err
	}
	filename, err := p.pathFor(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	fileInfo, err := os.Stat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return ObjectInfo{}, ErrObjectNotFound
	}
	if err != nil {
		return ObjectInfo{}, err
	}
	if fileInfo.IsDir() {
		return ObjectInfo{}, ErrObjectNotFound
	}
	file, err := os.Open(filename)
	if err != nil {
		return ObjectInfo{}, err
	}
	digest := sha256.New()
	var header [512]byte
	headerBytes, headerErr := io.ReadFull(file, header[:])
	if headerErr != nil && !errors.Is(headerErr, io.EOF) && !errors.Is(headerErr, io.ErrUnexpectedEOF) {
		_ = file.Close()
		return ObjectInfo{}, headerErr
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return ObjectInfo{}, err
	}
	if _, err = io.Copy(digest, &contextReader{ctx: ctx, reader: file}); err != nil {
		_ = file.Close()
		return ObjectInfo{}, err
	}
	if err = file.Close(); err != nil {
		return ObjectInfo{}, err
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	return ObjectInfo{
		Key:         key,
		ContentType: detectContentType(header[:headerBytes], key),
		Size:        fileInfo.Size(),
		SHA256:      hash,
		ETag:        hash,
		CreatedAt:   fileInfo.ModTime(),
	}, nil
}

func (p *LocalProvider) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	filename, err := p.pathFor(key)
	if err != nil {
		return err
	}
	if err = os.Remove(filename); errors.Is(err, os.ErrNotExist) {
		return ErrObjectNotFound
	}
	return err
}

func (p *LocalProvider) pathFor(key string) (string, error) {
	if err := ValidateKey(key); err != nil {
		return "", err
	}
	filename := filepath.Join(p.root, filepath.FromSlash(key))
	relative, err := filepath.Rel(p.root, filename)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", ErrInvalidKey
	}
	return filename, nil
}

func (p *LocalProvider) mustPath(key string) string {
	filename, _ := p.pathFor(key)
	return filename
}

// ValidateKey rejects absolute paths, parent traversal, platform separators,
// NUL bytes and non-canonical path forms. Keys are opaque slash-separated
// identifiers generated by the application, never user filenames.
func ValidateKey(key string) error {
	if strings.TrimSpace(key) == "" || strings.IndexByte(key, 0) >= 0 || strings.Contains(key, "\\") || strings.HasPrefix(key, "/") || filepath.IsAbs(key) {
		return ErrInvalidKey
	}
	clean := path.Clean(key)
	if clean == "." || clean != key || strings.HasPrefix(clean, "../") || clean == ".." {
		return ErrInvalidKey
	}
	for _, segment := range strings.Split(clean, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return ErrInvalidKey
		}
	}
	return nil
}

func normalizeContentType(value, key string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value != "" {
		if mediaType, _, err := mime.ParseMediaType(value); err == nil {
			return mediaType
		}
	}
	return mime.TypeByExtension(filepath.Ext(key))
}

func detectContentType(header []byte, key string) string {
	if len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(header) >= 8 && string(header[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(header) >= 3 && header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff {
		return "image/jpeg"
	}
	if contentType := http.DetectContentType(header); contentType != "application/octet-stream" {
		return contentType
	}
	return normalizeContentType("", key)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
