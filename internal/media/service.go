// Package media owns product image and attachment metadata. Object bytes are
// kept behind storage.StorageProvider; PostgreSQL only stores immutable keys
// and the presentation fields that may be changed with optimistic locking.
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound       = errors.New("media record not found")
	ErrInvalidProduct = errors.New("media product is invalid")
	ErrInvalidVariant = errors.New("media variant is invalid")
	ErrConflict       = errors.New("media record conflict")
)

type Service struct {
	pool      database.Querier
	provider  storage.StorageProvider
	processor storage.ImageProcessor
	now       func() time.Time
}

func NewService(pool database.Querier, provider storage.StorageProvider, processor storage.ImageProcessor) *Service {
	if processor.Limits.MaxBytes == 0 {
		processor.Limits = storage.DefaultImageLimits()
	}
	return &Service{pool: pool, provider: provider, processor: processor, now: time.Now}
}

type ImageVariant struct {
	Name        string `json:"name"`
	StorageKey  string `json:"storage_key"`
	ContentType string `json:"content_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ByteSize    int64  `json:"byte_size"`
	SHA256      string `json:"sha256"`
}

type Image struct {
	ID                  string         `json:"id"`
	ProductID           string         `json:"product_id"`
	VariantID           *string        `json:"variant_id,omitempty"`
	OriginalFilename    string         `json:"original_filename"`
	OriginalContentType string         `json:"original_content_type"`
	OriginalSize        int64          `json:"original_size"`
	Width               int            `json:"width"`
	Height              int            `json:"height"`
	Orientation         int            `json:"orientation"`
	SHA256              string         `json:"sha256"`
	Position            int            `json:"position"`
	IsPrimary           bool           `json:"is_primary"`
	ArchivedAt          *time.Time     `json:"archived_at,omitempty"`
	Variants            []ImageVariant `json:"variants"`
	Version             int64          `json:"version"`
}

type ImagePresentationInput struct {
	Position  *int  `json:"position,omitempty"`
	IsPrimary *bool `json:"is_primary,omitempty"`
	Archived  *bool `json:"archived,omitempty"`
}

type Attachment struct {
	ID               string     `json:"id"`
	ProductID        string     `json:"product_id"`
	VariantID        *string    `json:"variant_id,omitempty"`
	AttachmentKind   string     `json:"attachment_kind"`
	OriginalFilename string     `json:"original_filename"`
	ContentType      string     `json:"content_type"`
	ByteSize         int64      `json:"byte_size"`
	SHA256           string     `json:"sha256"`
	Description      string     `json:"description"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	Version          int64      `json:"version"`
}

func (s *Service) check() error {
	if s == nil || s.pool == nil || s.provider == nil {
		return errors.New("media service is not configured")
	}
	return nil
}

func (s *Service) UploadImage(ctx context.Context, session identity.Session, productID, variantID, filename string, source io.Reader, position int, primary bool, meta identity.RequestMeta) (Image, error) {
	if !session.HasPermission("product.image.manage") {
		return Image{}, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return Image{}, err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return Image{}, fmt.Errorf("%w: %v", identity.ErrValidation, err)
	}
	variantID, err = optionalID(variantID)
	if err != nil {
		return Image{}, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
	}
	if position < 0 {
		return Image{}, fmt.Errorf("%w: görsel sırası geçersiz", identity.ErrValidation)
	}
	payload, info, err := storage.ValidateImage(ctx, source, s.processor.Limits)
	if err != nil {
		return Image{}, err
	}
	imageID := uuid.NewString()
	prefix := fmt.Sprintf("companies/%s/products/%s/images/%s", session.CurrentCompanyID, productID, imageID)
	processed, err := s.processor.Process(ctx, payload, prefix)
	if err != nil {
		return Image{}, err
	}
	if len(processed.Plans) == 0 || len(processed.Master) == 0 {
		return Image{}, storage.ErrWebPEncoderUnavailable
	}
	extension := extensionForContentType(info.ContentType)
	sourceKey := prefix + "/original" + extension
	keys := []string{sourceKey}
	if _, err = s.provider.Put(ctx, sourceKey, bytes.NewReader(payload), storage.PutOptions{ContentType: info.ContentType, MaxBytes: s.processor.Limits.MaxBytes}); err != nil {
		return Image{}, err
	}
	for _, plan := range processed.Plans {
		variantPayload := processed.VariantPayloads[plan.Name]
		if len(variantPayload) == 0 {
			s.cleanup(ctx, keys)
			return Image{}, fmt.Errorf("%w: varyant çıktısı boş", storage.ErrWebPEncoderUnavailable)
		}
		if _, err = s.provider.Put(ctx, plan.StorageKey, bytes.NewReader(variantPayload), storage.PutOptions{ContentType: plan.ContentType}); err != nil {
			s.cleanup(ctx, append(keys, plan.StorageKey))
			return Image{}, err
		}
		keys = append(keys, plan.StorageKey)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		s.cleanup(ctx, keys)
		return Image{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = validateProductTx(ctx, tx, session.CurrentCompanyID, productID, variantID); err != nil {
		s.cleanup(ctx, keys)
		return Image{}, err
	}
	if !primary {
		var count int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_images WHERE company_id=$1 AND product_id=$2 AND variant_id IS NOT DISTINCT FROM $3::uuid AND archived_at IS NULL`, session.CurrentCompanyID, productID, nullableUUID(variantID)).Scan(&count); err != nil {
			s.cleanup(ctx, keys)
			return Image{}, err
		}
		primary = count == 0
	}
	if primary {
		if _, err = tx.Exec(ctx, `UPDATE product_images SET is_primary=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND product_id=$2 AND variant_id IS NOT DISTINCT FROM $3::uuid AND archived_at IS NULL`, session.CurrentCompanyID, productID, nullableUUID(variantID)); err != nil {
			s.cleanup(ctx, keys)
			return Image{}, err
		}
	}
	master := processed.Plans[0]
	masterBytes := processed.VariantPayloads[master.Name]
	if _, err = tx.Exec(ctx, `INSERT INTO product_images(id,company_id,product_id,variant_id,source_storage_key,master_storage_key,original_filename,original_content_type,original_size,master_size,width,height,orientation,sha256,master_sha256,position,is_primary,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, imageID, session.CurrentCompanyID, productID, nullableUUID(variantID), sourceKey, master.StorageKey, safeFilename(filename), info.ContentType, info.Size, len(masterBytes), info.Width, info.Height, info.Orientation, info.SHA256, sha256Hex(masterBytes), position, primary, nullableUUID(session.User.ID)); err != nil {
		s.cleanup(ctx, keys)
		return Image{}, mapConstraint(err)
	}
	for _, plan := range processed.Plans {
		variantCode := strings.ToUpper(plan.Name)
		variantPayload := processed.VariantPayloads[plan.Name]
		if _, err = tx.Exec(ctx, `INSERT INTO product_image_variants(id,company_id,product_image_id,variant_code,storage_key,content_type,byte_size,width,height,sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, imageID, variantCode, plan.StorageKey, plan.ContentType, len(variantPayload), plan.Width, plan.Height, sha256Hex(variantPayload)); err != nil {
			s.cleanup(ctx, keys)
			return Image{}, mapConstraint(err)
		}
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "PRODUCT_IMAGE_UPLOADED", "product.image.uploaded", "product_image", imageID, meta, map[string]any{"product_id": productID, "sha256": info.SHA256}); err != nil {
		s.cleanup(ctx, keys)
		return Image{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		s.cleanup(ctx, keys)
		return Image{}, err
	}
	return s.GetImage(ctx, session, productID, imageID)
}

func (s *Service) ListImages(ctx context.Context, session identity.Session, productID, variantID string, limit int) ([]Image, error) {
	if !session.HasPermission("product.image.read") {
		return nil, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return nil, fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	variantID, err = optionalID(variantID)
	if err != nil {
		return nil, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID, productID}
	query := `SELECT id,product_id,variant_id,original_filename,original_content_type,original_size,width,height,orientation,sha256,position,is_primary,archived_at,version FROM product_images WHERE company_id=$1 AND product_id=$2`
	if variantID != "" {
		args = append(args, variantID)
		query += ` AND variant_id=$3`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY position,id LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	items := make([]Image, 0)
	for rows.Next() {
		var item Image
		if err = rows.Scan(&item.ID, &item.ProductID, &item.VariantID, &item.OriginalFilename, &item.OriginalContentType, &item.OriginalSize, &item.Width, &item.Height, &item.Orientation, &item.SHA256, &item.Position, &item.IsPrimary, &item.ArchivedAt, &item.Version); err != nil {
			rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	// imageVariants issues its own query per image; run it only after the image
	// rows are drained, otherwise the request-pinned connection fails with
	// "conn busy".
	for i := range items {
		items[i].Variants, err = s.imageVariants(ctx, session.CurrentCompanyID, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Service) GetImage(ctx context.Context, session identity.Session, productID, imageID string) (Image, error) {
	items, err := s.ListImages(ctx, session, productID, "", 200)
	if err != nil {
		return Image{}, err
	}
	for _, item := range items {
		if item.ID == imageID {
			return item, nil
		}
	}
	return Image{}, ErrNotFound
}

// OpenImageVariant authorizes an image object through its company-scoped
// metadata row and only then opens the provider key. Callers never submit a
// storage key directly, so a leaked key from another company cannot be used
// as a path traversal or cross-tenant read primitive.
func (s *Service) OpenImageVariant(ctx context.Context, session identity.Session, productID, imageID, variantCode string) (io.ReadCloser, storage.ObjectInfo, error) {
	if !session.HasPermission("product.image.read") {
		return nil, storage.ObjectInfo{}, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return nil, storage.ObjectInfo{}, fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	imageID, err = parseID("image_id", imageID)
	if err != nil {
		return nil, storage.ObjectInfo{}, fmt.Errorf("%w: görsel kimliği geçersiz", identity.ErrValidation)
	}
	variantCode = strings.ToUpper(strings.TrimSpace(variantCode))
	var key string
	if variantCode == "" || variantCode == "ORIGINAL" {
		if err = s.pool.QueryRow(ctx, `SELECT source_storage_key FROM product_images WHERE company_id=$1 AND product_id=$2 AND id=$3 AND archived_at IS NULL`, session.CurrentCompanyID, productID, imageID).Scan(&key); errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ObjectInfo{}, ErrNotFound
		} else if err != nil {
			return nil, storage.ObjectInfo{}, err
		}
	} else {
		if err = s.pool.QueryRow(ctx, `SELECT v.storage_key FROM product_image_variants v JOIN product_images i ON i.company_id=v.company_id AND i.id=v.product_image_id WHERE v.company_id=$1 AND i.product_id=$2 AND i.id=$3 AND i.archived_at IS NULL AND v.variant_code=$4`, session.CurrentCompanyID, productID, imageID, variantCode).Scan(&key); errors.Is(err, pgx.ErrNoRows) {
			return nil, storage.ObjectInfo{}, ErrNotFound
		} else if err != nil {
			return nil, storage.ObjectInfo{}, err
		}
	}
	file, info, err := s.provider.Open(ctx, key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return nil, storage.ObjectInfo{}, ErrNotFound
	}
	return file, info, err
}

// OpenAttachment authorizes and opens an attachment's immutable original
// object. Technical PDFs/certificates retain their source content type.
func (s *Service) OpenAttachment(ctx context.Context, session identity.Session, productID, attachmentID string) (io.ReadCloser, storage.ObjectInfo, string, error) {
	if !session.HasPermission("product.attachment.read") {
		return nil, storage.ObjectInfo{}, "", identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return nil, storage.ObjectInfo{}, "", fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	attachmentID, err = parseID("attachment_id", attachmentID)
	if err != nil {
		return nil, storage.ObjectInfo{}, "", fmt.Errorf("%w: ek kimliği geçersiz", identity.ErrValidation)
	}
	var key, filename string
	if err = s.pool.QueryRow(ctx, `SELECT storage_key,original_filename FROM product_attachments WHERE company_id=$1 AND product_id=$2 AND id=$3 AND archived_at IS NULL`, session.CurrentCompanyID, productID, attachmentID).Scan(&key, &filename); errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ObjectInfo{}, "", ErrNotFound
	} else if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	file, info, err := s.provider.Open(ctx, key)
	if errors.Is(err, storage.ErrObjectNotFound) {
		return nil, storage.ObjectInfo{}, "", ErrNotFound
	}
	return file, info, filename, err
}

func (s *Service) UpdateImagePresentation(ctx context.Context, session identity.Session, productID, imageID string, expectedVersion int64, input ImagePresentationInput, meta identity.RequestMeta) (Image, error) {
	if !session.HasPermission("product.image.manage") {
		return Image{}, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return Image{}, err
	}
	if expectedVersion < 1 {
		return Image{}, fmt.Errorf("%w: görsel sürümü gereklidir", identity.ErrValidation)
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return Image{}, fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	imageID, err = parseID("image_id", imageID)
	if err != nil {
		return Image{}, fmt.Errorf("%w: görsel kimliği geçersiz", identity.ErrValidation)
	}
	if input.Position != nil && *input.Position < 0 {
		return Image{}, fmt.Errorf("%w: görsel sırası geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var variantID *string
	if err = tx.QueryRow(ctx, `SELECT variant_id FROM product_images WHERE company_id=$1 AND product_id=$2 AND id=$3 FOR UPDATE`, session.CurrentCompanyID, productID, imageID).Scan(&variantID); errors.Is(err, pgx.ErrNoRows) {
		return Image{}, ErrNotFound
	} else if err != nil {
		return Image{}, err
	}
	if input.IsPrimary != nil && *input.IsPrimary {
		if _, err = tx.Exec(ctx, `UPDATE product_images SET is_primary=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND product_id=$2 AND variant_id IS NOT DISTINCT FROM $3::uuid AND id<>$4 AND archived_at IS NULL`, session.CurrentCompanyID, productID, nullableUUID(stringValue(variantID)), imageID); err != nil {
			return Image{}, err
		}
	}
	result, err := tx.Exec(ctx, `UPDATE product_images SET position=COALESCE($1,position),is_primary=CASE WHEN COALESCE($3,false) THEN false ELSE COALESCE($2,is_primary) END,archived_at=CASE WHEN $3 IS NULL THEN archived_at WHEN $3 THEN now() ELSE NULL END,updated_at=now(),version=version+1 WHERE company_id=$4 AND product_id=$5 AND id=$6 AND version=$7`, input.Position, input.IsPrimary, input.Archived, session.CurrentCompanyID, productID, imageID, expectedVersion)
	if err != nil {
		return Image{}, mapConstraint(err)
	}
	if result.RowsAffected() != 1 {
		return Image{}, ErrConflict
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "PRODUCT_IMAGE_PRESENTATION_UPDATED", "product.image.presentation.updated", "product_image", imageID, meta, nil); err != nil {
		return Image{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Image{}, err
	}
	return s.GetImage(ctx, session, productID, imageID)
}

func (s *Service) ArchiveImage(ctx context.Context, session identity.Session, productID, imageID string, expectedVersion int64, meta identity.RequestMeta) error {
	archived := true
	_, err := s.UpdateImagePresentation(ctx, session, productID, imageID, expectedVersion, ImagePresentationInput{Archived: &archived}, meta)
	return err
}

func (s *Service) UploadAttachment(ctx context.Context, session identity.Session, productID, variantID, filename, kind, description string, source io.Reader, meta identity.RequestMeta) (Attachment, error) {
	if !session.HasPermission("product.attachment.manage") {
		return Attachment{}, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return Attachment{}, err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	variantID, err = optionalID(variantID)
	if err != nil {
		return Attachment{}, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
	}
	if err = validateProductTx(ctx, s.pool, session.CurrentCompanyID, productID, variantID); err != nil {
		return Attachment{}, err
	}
	if strings.TrimSpace(kind) == "" {
		kind = "GENERAL"
	}
	kind = strings.ToUpper(strings.TrimSpace(kind))
	if len(kind) > 64 || !validKind(kind) {
		return Attachment{}, fmt.Errorf("%w: ek türü geçersiz", identity.ErrValidation)
	}
	description = strings.TrimSpace(description)
	if len(description) > 2000 {
		return Attachment{}, fmt.Errorf("%w: açıklama çok uzun", identity.ErrValidation)
	}
	payload, err := readBounded(ctx, source, 100<<20)
	if err != nil {
		return Attachment{}, err
	}
	if len(payload) == 0 {
		return Attachment{}, fmt.Errorf("%w: ek dosyası boş", identity.ErrValidation)
	}
	contentType := http.DetectContentType(payload)
	if detected := mime.TypeByExtension(filepath.Ext(filename)); detected != "" && contentType == "application/octet-stream" {
		contentType = detected
	}
	digest := sha256.Sum256(payload)
	attachmentID := uuid.NewString()
	key := fmt.Sprintf("companies/%s/products/%s/attachments/%s", session.CurrentCompanyID, productID, attachmentID)
	if _, err = s.provider.Put(ctx, key, bytes.NewReader(payload), storage.PutOptions{ContentType: contentType, MaxBytes: 100 << 20}); err != nil {
		return Attachment{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.cleanup(ctx, []string{key})
		return Attachment{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = validateProductTx(ctx, tx, session.CurrentCompanyID, productID, variantID); err != nil {
		s.cleanup(ctx, []string{key})
		return Attachment{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO product_attachments(id,company_id,product_id,variant_id,attachment_kind,storage_key,original_filename,content_type,byte_size,sha256,description,uploaded_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, attachmentID, session.CurrentCompanyID, productID, nullableUUID(variantID), kind, key, safeFilename(filename), contentType, len(payload), hex.EncodeToString(digest[:]), description, nullableUUID(session.User.ID)); err != nil {
		s.cleanup(ctx, []string{key})
		return Attachment{}, mapConstraint(err)
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "PRODUCT_ATTACHMENT_UPLOADED", "product.attachment.uploaded", "product_attachment", attachmentID, meta, map[string]any{"product_id": productID, "sha256": hex.EncodeToString(digest[:])}); err != nil {
		s.cleanup(ctx, []string{key})
		return Attachment{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		s.cleanup(ctx, []string{key})
		return Attachment{}, err
	}
	return s.GetAttachment(ctx, session, productID, attachmentID)
}

func (s *Service) ListAttachments(ctx context.Context, session identity.Session, productID, variantID string, limit int) ([]Attachment, error) {
	if !session.HasPermission("product.attachment.read") {
		return nil, identity.ErrForbidden
	}
	if err := s.check(); err != nil {
		return nil, err
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return nil, fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	variantID, err = optionalID(variantID)
	if err != nil {
		return nil, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	args := []any{session.CurrentCompanyID, productID}
	query := `SELECT id,product_id,variant_id,attachment_kind,original_filename,content_type,byte_size,sha256,description,archived_at,version FROM product_attachments WHERE company_id=$1 AND product_id=$2`
	if variantID != "" {
		args = append(args, variantID)
		query += ` AND variant_id=$3`
	}
	args = append(args, limit)
	query += fmt.Sprintf(` ORDER BY created_at DESC,id DESC LIMIT $%d`, len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Attachment, 0)
	for rows.Next() {
		var item Attachment
		if err = rows.Scan(&item.ID, &item.ProductID, &item.VariantID, &item.AttachmentKind, &item.OriginalFilename, &item.ContentType, &item.ByteSize, &item.SHA256, &item.Description, &item.ArchivedAt, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetAttachment(ctx context.Context, session identity.Session, productID, attachmentID string) (Attachment, error) {
	items, err := s.ListAttachments(ctx, session, productID, "", 200)
	if err != nil {
		return Attachment{}, err
	}
	for _, item := range items {
		if item.ID == attachmentID {
			return item, nil
		}
	}
	return Attachment{}, ErrNotFound
}

func (s *Service) ArchiveAttachment(ctx context.Context, session identity.Session, productID, attachmentID string, expectedVersion int64, meta identity.RequestMeta) error {
	if !session.HasPermission("product.attachment.manage") {
		return identity.ErrForbidden
	}
	if expectedVersion < 1 {
		return fmt.Errorf("%w: ek sürümü gereklidir", identity.ErrValidation)
	}
	productID, err := parseID("product_id", productID)
	if err != nil {
		return fmt.Errorf("%w: ürün kimliği geçersiz", identity.ErrValidation)
	}
	attachmentID, err = parseID("attachment_id", attachmentID)
	if err != nil {
		return fmt.Errorf("%w: ek kimliği geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	result, err := tx.Exec(ctx, `UPDATE product_attachments SET archived_at=COALESCE(archived_at,now()),version=version+1 WHERE company_id=$1 AND product_id=$2 AND id=$3 AND version=$4`, session.CurrentCompanyID, productID, attachmentID, expectedVersion)
	if err != nil {
		return mapConstraint(err)
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = writeAuditAndEventTx(ctx, tx, session, "PRODUCT_ATTACHMENT_ARCHIVED", "product.attachment.archived", "product_attachment", attachmentID, meta, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) imageVariants(ctx context.Context, companyID, imageID string) ([]ImageVariant, error) {
	rows, err := s.pool.Query(ctx, `SELECT variant_code,storage_key,content_type,width,height,byte_size,sha256 FROM product_image_variants WHERE company_id=$1 AND product_image_id=$2 ORDER BY CASE variant_code WHEN 'MASTER' THEN 0 ELSE 1 END,variant_code`, companyID, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ImageVariant{}
	for rows.Next() {
		var item ImageVariant
		if err = rows.Scan(&item.Name, &item.StorageKey, &item.ContentType, &item.Width, &item.Height, &item.ByteSize, &item.SHA256); err != nil {
			return nil, err
		}
		item.Name = strings.ToLower(item.Name)
		items = append(items, item)
	}
	return items, rows.Err()
}

func validateProductTx(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, companyID, productID, variantID string) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND id=$2)`, companyID, productID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidProduct
	}
	if variantID != "" {
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3)`, companyID, variantID, productID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrInvalidVariant
		}
	}
	return nil
}

func writeAuditAndEventTx(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityType, entityID string, meta identity.RequestMeta, payload map[string]any) error {
	details, _ := json.Marshal(map[string]any{"version": 1})
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, nullableUUID(session.User.ID), eventType, entityType, nullableUUID(entityID), details, meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	if payload == nil {
		payload = map[string]any{}
	}
	encoded, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, encoded)
	return err
}

func readBounded(ctx context.Context, source io.Reader, max int64) ([]byte, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: dosya gereklidir", identity.ErrValidation)
	}
	if max <= 0 {
		max = 100 << 20
	}
	reader := io.LimitReader(&ctxReader{ctx: ctx, reader: source}, max+1)
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > max {
		return nil, fmt.Errorf("%w: dosya boyutu sınırı aşıldı", identity.ErrValidation)
	}
	return payload, nil
}

type ctxReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *ctxReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}

func (s *Service) cleanup(ctx context.Context, keys []string) {
	for _, key := range keys {
		_ = s.provider.Delete(ctx, key)
	}
}

func parseID(name, value string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("%s geçersiz", name)
	}
	return id.String(), nil
}

func optionalID(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	return parseID("id", value)
}

func nullableUUID(value string) any {
	if value == "" || uuid.Validate(value) != nil {
		return nil
	}
	return value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func extensionForContentType(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".bin"
	}
}

func safeFilename(filename string) string {
	filename = filepath.Base(strings.ReplaceAll(strings.TrimSpace(filename), "\x00", ""))
	if filename == "." || filename == "" {
		filename = "dosya"
	}
	if len(filename) > 255 {
		filename = filename[:255]
	}
	return filename
}

func validKind(value string) bool {
	for index, r := range value {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (index > 0 && (r == '_' || r == '.' || r == '-')) {
			continue
		}
		return false
	}
	return value != ""
}

func sha256Hex(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func mapConstraint(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}

func truncate(value string, max int) string {
	if len(value) > max {
		return value[:max]
	}
	return value
}
