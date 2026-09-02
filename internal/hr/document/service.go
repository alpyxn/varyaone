// Package document owns employee document metadata. Bytes live behind
// storage.StorageProvider with an opaque key; PostgreSQL keeps only metadata.
// Documents are archived, never deleted.
package document

import (
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
	ErrNotFound     = errors.New("EMPLOYEE_DOCUMENT_NOT_FOUND")
	ErrEmployeeGone = errors.New("EMPLOYEE_DOCUMENT_EMPLOYEE_NOT_FOUND")
	ErrArchived     = errors.New("EMPLOYEE_DOCUMENT_ARCHIVED")
)

const maxDocumentBytes = 25 << 20

var validSensitivity = map[string]bool{"GENERAL": true, "IDENTITY": true, "HEALTH": true}

type Service struct {
	pool     database.Querier
	provider storage.StorageProvider
}

func NewService(pool database.Querier, provider storage.StorageProvider) *Service {
	return &Service{pool: pool, provider: provider}
}

type Document struct {
	ID               string     `json:"id"`
	EmployeeID       string     `json:"employee_id"`
	DocumentType     string     `json:"document_type"`
	Sensitivity      string     `json:"sensitivity"`
	MimeType         string     `json:"mime_type"`
	SizeBytes        int64      `json:"size_bytes"`
	SHA256           string     `json:"sha256"`
	OriginalFilename string     `json:"original_filename,omitempty"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

// visibleSensitivities returns the sensitivities the session may read.
func visibleSensitivities(session identity.Session) []string {
	visible := []string{}
	if session.HasPermission("hr.employee_document.read") {
		visible = append(visible, "GENERAL", "IDENTITY")
	}
	if session.HasPermission("hr.health_document.read") {
		visible = append(visible, "HEALTH")
	}
	return visible
}

// List returns documents for an employee. When archivedOnly is true only
// archived documents are returned; otherwise only active (non-archived) ones.
func (s *Service) List(ctx context.Context, session identity.Session, employeeID string, archivedOnly bool) ([]Document, error) {
	visible := visibleSensitivities(session)
	if len(visible) == 0 {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,employee_id::text,document_type,sensitivity,mime_type,size_bytes,sha256,original_filename,archived_at,created_at
 FROM employee_documents WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND sensitivity = ANY($3)
 AND (CASE WHEN $4 THEN archived_at IS NOT NULL ELSE archived_at IS NULL END) ORDER BY created_at DESC,id`,
		session.CurrentCompanyID, employeeID, visible, archivedOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Document{}
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.EmployeeID, &d.DocumentType, &d.Sensitivity, &d.MimeType, &d.SizeBytes, &d.SHA256, &d.OriginalFilename, &d.ArchivedAt, &d.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

func (s *Service) Upload(ctx context.Context, session identity.Session, employeeID, documentType, sensitivity, filename string, source io.Reader, meta identity.RequestMeta) (Document, error) {
	if !session.HasPermission("hr.employee_document.edit") {
		return Document{}, identity.ErrForbidden
	}
	sensitivity = strings.ToUpper(strings.TrimSpace(sensitivity))
	documentType = strings.TrimSpace(documentType)
	if sensitivity == "" {
		sensitivity = "GENERAL"
	}
	if !validSensitivity[sensitivity] {
		return Document{}, fmt.Errorf("%w: belge hassasiyeti geçersiz", identity.ErrValidation)
	}
	if sensitivity == "HEALTH" && !session.HasPermission("hr.health_document.read") {
		return Document{}, identity.ErrForbidden
	}
	if documentType == "" {
		return Document{}, fmt.Errorf("%w: belge türü zorunlu", identity.ErrValidation)
	}
	payload, err := io.ReadAll(io.LimitReader(source, maxDocumentBytes+1))
	if err != nil {
		return Document{}, err
	}
	if len(payload) == 0 {
		return Document{}, fmt.Errorf("%w: belge dosyası boş", identity.ErrValidation)
	}
	if len(payload) > maxDocumentBytes {
		return Document{}, fmt.Errorf("%w: belge boyutu sınırı aşıyor", identity.ErrValidation)
	}
	contentType := http.DetectContentType(payload)
	if detected := mime.TypeByExtension(filepath.Ext(filename)); detected != "" && contentType == "application/octet-stream" {
		contentType = detected
	}
	digest := sha256.Sum256(payload)
	id := uuid.NewString()
	key := fmt.Sprintf("companies/%s/hr/employees/%s/documents/%s", session.CurrentCompanyID, employeeID, id)

	var employeeExists bool
	if err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE company_id=$1 AND id=NULLIF($2,'')::uuid)`, session.CurrentCompanyID, employeeID).Scan(&employeeExists); err != nil {
		return Document{}, err
	}
	if !employeeExists {
		return Document{}, ErrEmployeeGone
	}
	if _, err = storage.PutBytes(ctx, s.provider, key, payload, storage.PutOptions{ContentType: contentType, MaxBytes: maxDocumentBytes}); err != nil {
		return Document{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `INSERT INTO employee_documents(id,company_id,employee_id,document_type,sensitivity,storage_key,sha256,mime_type,size_bytes,original_filename,created_by)
 VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id, session.CurrentCompanyID, employeeID, documentType, sensitivity, key, hex.EncodeToString(digest[:]), contentType, len(payload), sanitizeUploadName(filename), session.User.ID)
	if err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Document{}, mapConstraint(err)
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_DOCUMENT_UPLOADED", "hr.employee_document.uploaded", employeeID, map[string]any{"document_id": id, "sensitivity": sensitivity}); err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		_ = s.provider.Delete(context.WithoutCancel(ctx), key)
		return Document{}, err
	}
	return s.get(ctx, session, employeeID, id)
}

func (s *Service) Open(ctx context.Context, session identity.Session, employeeID, documentID string) (io.ReadCloser, storage.ObjectInfo, string, error) {
	visible := visibleSensitivities(session)
	if len(visible) == 0 {
		return nil, storage.ObjectInfo{}, "", identity.ErrForbidden
	}
	var key, docType, mimeType, originalName string
	err := s.pool.QueryRow(ctx, `SELECT storage_key,document_type,mime_type,original_filename FROM employee_documents
 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid AND sensitivity = ANY($4)`,
		session.CurrentCompanyID, employeeID, documentID, visible).Scan(&key, &docType, &mimeType, &originalName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, storage.ObjectInfo{}, "", ErrNotFound
	}
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	reader, info, err := s.provider.Open(ctx, key)
	if err != nil {
		return nil, storage.ObjectInfo{}, "", err
	}
	if info.ContentType == "" {
		info.ContentType = mimeType
	}
	// İndirme adı: yüklenen dosyanın orijinal adı korunur. Orijinal ad yoksa
	// "<belge türü>.<uzantı>" olarak üretilir (uzantı MIME türünden).
	if name := sanitizeUploadName(originalName); name != "" {
		return reader, info, name, nil
	}
	ext := ""
	if exts, _ := mime.ExtensionsByType(mimeType); len(exts) > 0 {
		ext = exts[0]
	}
	return reader, info, sanitizeFilename(docType) + ext, nil
}

// sanitizeUploadName keeps a filesystem-safe version of the uploaded filename.
func sanitizeUploadName(value string) string {
	value = filepath.Base(strings.TrimSpace(value))
	value = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '-'
		}
		return r
	}, value)
	if value == "." || value == ".." {
		return ""
	}
	return value
}

func (s *Service) Archive(ctx context.Context, session identity.Session, employeeID, documentID string, meta identity.RequestMeta) error {
	if !session.HasPermission("hr.employee_document.edit") {
		return identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE employee_documents SET archived_at=now(),archived_by=$4
 WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid AND archived_at IS NULL`,
		session.CurrentCompanyID, employeeID, documentID, session.User.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		_ = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employee_documents WHERE company_id=$1 AND id=NULLIF($2,'')::uuid)`, session.CurrentCompanyID, documentID).Scan(&exists)
		if exists {
			return ErrArchived
		}
		return ErrNotFound
	}
	if err = writeEvent(ctx, tx, session, meta, "EMPLOYEE_DOCUMENT_ARCHIVED", "hr.employee_document.archived", employeeID, map[string]any{"document_id": documentID}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) get(ctx context.Context, session identity.Session, employeeID, documentID string) (Document, error) {
	visible := visibleSensitivities(session)
	var d Document
	err := s.pool.QueryRow(ctx, `SELECT id::text,employee_id::text,document_type,sensitivity,mime_type,size_bytes,sha256,original_filename,archived_at,created_at
 FROM employee_documents WHERE company_id=$1 AND employee_id=NULLIF($2,'')::uuid AND id=NULLIF($3,'')::uuid AND sensitivity = ANY($4)`,
		session.CurrentCompanyID, employeeID, documentID, visible).
		Scan(&d.ID, &d.EmployeeID, &d.DocumentType, &d.Sensitivity, &d.MimeType, &d.SizeBytes, &d.SHA256, &d.OriginalFilename, &d.ArchivedAt, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	return d, err
}

func sanitizeFilename(value string) string {
	value = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return '-'
		}
		return r
	}, strings.TrimSpace(value))
	if value == "" {
		return "belge"
	}
	return value
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrEmployeeGone
	}
	return err
}

func writeEvent(ctx context.Context, tx pgx.Tx, session identity.Session, meta identity.RequestMeta, auditType, outboxType, employeeID string, details map[string]any) error {
	detailBytes, _ := json.Marshal(details)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)
 VALUES($1,$2,$3,$4,'employee',$5,$6,$7,$8,$9)`,
		uuid.NewString(), session.CurrentCompanyID, session.User.ID, auditType, employeeID, detailBytes, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"employee_id": employeeID})
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`,
		uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, payload)
	return err
}
