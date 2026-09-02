package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalProviderContract(t *testing.T) {
	root := t.TempDir()
	provider, err := NewLocalProvider(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	payload := []byte("şirket kapsamlı belge")
	info, err := provider.Put(ctx, "company/01/attachments/document.txt", bytes.NewReader(payload), PutOptions{ContentType: "text/plain"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(payload)) || info.SHA256 == "" || info.ContentType != "text/plain" {
		t.Fatalf("unexpected object info: %+v", info)
	}
	opened, stat, err := provider.Open(ctx, info.Key)
	if err != nil {
		t.Fatal(err)
	}
	read, readErr := io.ReadAll(opened)
	_ = opened.Close()
	if readErr != nil || !bytes.Equal(read, payload) || stat.SHA256 != info.SHA256 {
		t.Fatalf("opened object mismatch: read=%q stat=%+v err=%v", read, stat, readErr)
	}
	if _, err = provider.Put(ctx, info.Key, bytes.NewReader(payload), PutOptions{}); !errors.Is(err, ErrObjectExists) {
		t.Fatalf("expected immutable key conflict, got %v", err)
	}
	if err = provider.Delete(ctx, info.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Stat(ctx, info.Key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestLocalProviderRejectsTraversalAndPublishesAtomically(t *testing.T) {
	provider, err := NewLocalProvider(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"../outside", "/absolute", "a/../../outside", `a\\b`, "a//b", "a/./b"} {
		if _, err = provider.Put(context.Background(), key, bytes.NewReader([]byte("x")), PutOptions{}); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("key %q accepted: %v", key, err)
		}
	}
	broken := errReader{err: errors.New("simulated upload failure")}
	if _, err = provider.Put(context.Background(), "company/broken.bin", broken, PutOptions{}); !errors.Is(err, broken.err) {
		t.Fatalf("expected source error, got %v", err)
	}
	matches, globErr := filepath.Glob(filepath.Join(provider.Root(), "company", ".varya-object-*"))
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary object leaked: %v", matches)
	}
	if _, statErr := os.Stat(filepath.Join(provider.Root(), "company", "broken.bin")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("partial destination published: %v", statErr)
	}
}

func TestProviderConfigContract(t *testing.T) {
	if _, err := NewProvider(Config{Provider: ProviderLocal, LocalRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(Config{Provider: ProviderS3, Endpoint: "http://minio:9000", Bucket: "media", AccessKey: "access", SecretKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(Config{Provider: ProviderMinIO, Endpoint: "http://minio:9000", Bucket: "media", AccessKey: "access", SecretKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProvider(Config{Provider: ProviderS3}); !errors.Is(err, ErrProviderNotConfigured) {
		t.Fatalf("expected remote config error, got %v", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }
