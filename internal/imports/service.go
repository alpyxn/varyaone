// Package imports owns the durable metadata and file boundary for the common
// data-exchange platform. Domain adapters remain behind explicit hooks.
package imports

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const maxUploadBytes int64 = 64 << 20

const commitClaimTimeout = 5 * time.Minute

var (
	ErrStalePreview     = errors.New("import preview is stale")
	ErrCommitInProgress = errors.New("import commit is in progress")
)

type Service struct {
	pool  database.Querier
	store storage.StorageProvider
	stock *inventory.Service
}

type ExportJob struct {
	ID          string     `json:"id"`
	CompanyID   string     `json:"company_id"`
	EntityType  string     `json:"entity_type"`
	Format      string     `json:"format"`
	Status      string     `json:"status"`
	Filename    string     `json:"filename,omitempty"`
	RowCount    int        `json:"row_count"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func NewService(pool database.Querier, store storage.StorageProvider, stock *inventory.Service) *Service {
	return &Service{pool: pool, store: store, stock: stock}
}

type Job struct {
	ID                string     `json:"id"`
	CompanyID         string     `json:"company_id"`
	EntityType        string     `json:"entity_type"`
	Direction         string     `json:"direction"`
	TargetID          *string    `json:"target_id,omitempty"`
	Status            string     `json:"status"`
	CommitMode        string     `json:"commit_mode"`
	DryRun            bool       `json:"dry_run"`
	SourceFilename    string     `json:"source_filename"`
	SourceContentType string     `json:"source_content_type"`
	SourceSHA256      string     `json:"source_sha256"`
	RowCount          int        `json:"row_count"`
	ErrorCount        int        `json:"error_count"`
	WarningCount      int        `json:"warning_count"`
	AnalysisRevision  string     `json:"analysis_revision,omitempty"`
	PreviewHash       string     `json:"preview_hash,omitempty"`
	CommittingAt      *time.Time `json:"committing_at,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type UploadInput struct {
	CompanyID   string
	ActorUserID string
	EntityType  string
	TargetID    string
	CommitMode  string
	Filename    string
	ContentType string
	Source      io.Reader
}

func validImportEntity(entity string) bool {
	for _, spec := range dataexchange.InitialEntitySpecs() {
		if string(spec.Type) == strings.ToUpper(strings.TrimSpace(entity)) {
			return spec.Importable
		}
	}
	return false
}

func (s *Service) Upload(ctx context.Context, input UploadInput) (Job, error) {
	if s == nil || s.pool == nil || s.store == nil {
		return Job{}, errors.New("aktarım hizmeti yapılandırılmamış")
	}
	entity := strings.ToUpper(strings.TrimSpace(input.EntityType))
	if !validImportEntity(entity) {
		return Job{}, fmt.Errorf("geçersiz aktarım türü")
	}
	if input.Source == nil || strings.TrimSpace(input.Filename) == "" {
		return Job{}, fmt.Errorf("dosya ve dosya adı zorunludur")
	}
	mode := strings.ToUpper(strings.TrimSpace(input.CommitMode))
	if mode == "" {
		mode = "ATOMIC"
	}
	if mode != "ATOMIC" {
		return Job{}, fmt.Errorf("sayım ve açılış stoku aktarımı yalnızca atomik çalışır")
	}
	if entity == string(dataexchange.EntityStockCount) && strings.TrimSpace(input.TargetID) == "" {
		return Job{}, fmt.Errorf("stok sayımı aktarımı için sayım kimliği zorunludur")
	}
	id := uuid.NewString()
	ext := strings.ToLower(filepath.Ext(input.Filename))
	if ext != ".csv" && ext != ".xlsx" {
		return Job{}, fmt.Errorf("yalnızca CSV veya XLSX dosyası yükleyin")
	}
	key := fmt.Sprintf("imports/%s/%s/source%s", input.CompanyID, id, ext)
	object, err := s.store.Put(ctx, key, io.LimitReader(input.Source, maxUploadBytes+1), storage.PutOptions{ContentType: input.ContentType, MaxBytes: maxUploadBytes})
	if err != nil {
		return Job{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO import_jobs(id,company_id,entity_type,direction,target_id,status,commit_mode,source_filename,source_content_type,source_storage_key,source_sha256,created_by) VALUES($1,$2,$3,'IMPORT',$4,'UPLOADED',$5,$6,$7,$8,$9,$10)`, id, input.CompanyID, entity, nullable(input.TargetID), mode, filepath.Base(input.Filename), input.ContentType, key, object.SHA256, input.ActorUserID)
	if err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), key)
		return Job{}, err
	}
	return s.Get(ctx, input.CompanyID, id)
}

func nullable(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func (s *Service) Get(ctx context.Context, companyID, id string) (Job, error) {
	var job Job
	var target *string
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,entity_type,direction,target_id,status,commit_mode,dry_run,source_filename,source_content_type,source_sha256,row_count,error_count,warning_count,analysis_revision,preview_hash,committing_at,last_error,created_at,updated_at FROM import_jobs WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&job.ID, &job.CompanyID, &job.EntityType, &job.Direction, &target, &job.Status, &job.CommitMode, &job.DryRun, &job.SourceFilename, &job.SourceContentType, &job.SourceSHA256, &job.RowCount, &job.ErrorCount, &job.WarningCount, &job.AnalysisRevision, &job.PreviewHash, &job.CommittingAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, inventory.ErrNotFound
	}
	job.TargetID = target
	return job, err
}

func (s *Service) List(ctx context.Context, companyID string, limit int) ([]Job, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id,company_id,entity_type,direction,target_id,status,commit_mode,dry_run,source_filename,source_content_type,source_sha256,row_count,error_count,warning_count,analysis_revision,preview_hash,committing_at,last_error,created_at,updated_at FROM import_jobs WHERE company_id=$1 ORDER BY created_at DESC,id DESC LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Job, 0)
	for rows.Next() {
		var job Job
		var target *string
		if err = rows.Scan(&job.ID, &job.CompanyID, &job.EntityType, &job.Direction, &target, &job.Status, &job.CommitMode, &job.DryRun, &job.SourceFilename, &job.SourceContentType, &job.SourceSHA256, &job.RowCount, &job.ErrorCount, &job.WarningCount, &job.AnalysisRevision, &job.PreviewHash, &job.CommittingAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		job.TargetID = target
		result = append(result, job)
	}
	return result, rows.Err()
}

type AnalyzeInput struct {
	ManualMapping     map[string]string `json:"mapping"`
	DryRun            bool              `json:"dry_run,omitempty"`
	ActorUserID       string            `json:"-"`
	AllowOpeningStock bool              `json:"-"`
}
type Analysis struct {
	Job              Job                          `json:"job"`
	AnalysisRevision string                       `json:"analysis_revision"`
	Preview          dataexchange.Preview         `json:"preview"`
	Mapping          []dataexchange.ColumnMapping `json:"mapping"`
}

func specFor(entity string) (dataexchange.EntitySpec, bool) {
	for _, spec := range dataexchange.InitialEntitySpecs() {
		if string(spec.Type) == strings.ToUpper(strings.TrimSpace(entity)) {
			return spec, true
		}
	}
	return dataexchange.EntitySpec{}, false
}

func analysisRevision(sourceSHA, entity string, mapping map[string]string) string {
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(strings.ToUpper(strings.TrimSpace(entity)))
	builder.WriteByte('\x00')
	builder.WriteString(sourceSHA)
	for _, key := range keys {
		builder.WriteByte('\x00')
		builder.WriteString(strings.TrimSpace(key))
		builder.WriteByte('=')
		builder.WriteString(strings.TrimSpace(mapping[key]))
	}
	digest := sha256.Sum256([]byte(builder.String()))
	return fmt.Sprintf("%x", digest[:])
}

type catalogAdapter struct {
	pool              database.Querier
	companyID         string
	entity            string
	actorID           string
	allowOpeningStock bool
}

func (a catalogAdapter) Validate(ctx context.Context, input dataexchange.ValidationInput) (dataexchange.ValidationResult, error) {
	if a.pool == nil {
		return dataexchange.ValidationResult{}, errors.New("katalog doğrulaması yapılandırılmamış")
	}
	issues := make([]dataexchange.Issue, 0)
	for _, row := range input.Rows {
		var exists bool
		check := func(query, field, code, message string, args ...any) error {
			if err := a.pool.QueryRow(ctx, query, args...).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				issues = append(issues, dataexchange.Issue{RowNumber: row.RowNumber, Field: field, Code: code, Severity: dataexchange.SeverityError, Message: message})
			}
			return nil
		}
		productCode := strings.ToUpper(strings.TrimSpace(row.Values["product_code"]))
		if productCode != "" && a.entity != string(dataexchange.EntityProduct) {
			if err := check(`SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND code=$2)`, "product_code", "unknown_product", "ürün bulunamadı", a.companyID, productCode); err != nil {
				return dataexchange.ValidationResult{}, err
			}
		}
		variantCode := strings.ToUpper(strings.TrimSpace(row.Values["variant_code"]))
		if variantCode != "" && productCode != "" && a.entity != string(dataexchange.EntityVariant) && a.entity != string(dataexchange.EntityProduct) {
			if err := check(`SELECT EXISTS(SELECT 1 FROM product_variants v JOIN products p ON p.company_id=v.company_id AND p.id=v.product_id WHERE v.company_id=$1 AND p.code=$2 AND v.variant_code=$3)`, "variant_code", "unknown_variant", "varyant bulunamadı", a.companyID, productCode, variantCode); err != nil {
				return dataexchange.ValidationResult{}, err
			}
		}
		if a.entity == string(dataexchange.EntityVariant) && productCode != "" && variantCode != "" {
			var productID, existingProductID string
			productErr := a.pool.QueryRow(ctx, `SELECT id::text FROM products WHERE company_id=$1 AND code=$2`, a.companyID, productCode).Scan(&productID)
			if productErr == nil {
				variantErr := a.pool.QueryRow(ctx, `SELECT product_id::text FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3`, a.companyID, productID, variantCode).Scan(&existingProductID)
				if variantErr != nil && !errors.Is(variantErr, pgx.ErrNoRows) {
					return dataexchange.ValidationResult{}, variantErr
				}
				if variantErr == nil && existingProductID != productID {
					issues = append(issues, dataexchange.Issue{RowNumber: row.RowNumber, Field: "variant_code", Code: "existing_variant_conflict", Severity: dataexchange.SeverityError, Message: "varyant kodu başka bir üründe kullanılıyor"})
				}
			}
		}
		if a.entity == string(dataexchange.EntityBarcode) && productCode != "" && strings.TrimSpace(row.Values["barcode"]) != "" {
			var productID, variantID string
			productErr := a.pool.QueryRow(ctx, `SELECT id::text FROM products WHERE company_id=$1 AND code=$2`, a.companyID, productCode).Scan(&productID)
			if productErr == nil {
				if variantCode != "" {
					variantErr := a.pool.QueryRow(ctx, `SELECT id::text FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3`, a.companyID, productID, variantCode).Scan(&variantID)
					if variantErr != nil && !errors.Is(variantErr, pgx.ErrNoRows) {
						return dataexchange.ValidationResult{}, variantErr
					}
				}
				var existingProductID, existingVariantID string
				barcodeErr := a.pool.QueryRow(ctx, `SELECT product_id::text,COALESCE(variant_id::text,'') FROM product_barcodes WHERE company_id=$1 AND barcode=$2`, a.companyID, strings.TrimSpace(row.Values["barcode"])).Scan(&existingProductID, &existingVariantID)
				if barcodeErr != nil && !errors.Is(barcodeErr, pgx.ErrNoRows) {
					return dataexchange.ValidationResult{}, barcodeErr
				}
				if barcodeErr == nil && (existingProductID != productID || existingVariantID != variantID) {
					issues = append(issues, dataexchange.Issue{RowNumber: row.RowNumber, Field: "barcode", Code: "existing_barcode_conflict", Severity: dataexchange.SeverityError, Message: "barkod başka bir ürün veya varyanta bağlı"})
				}
			}
		}
		if listCode := strings.ToUpper(strings.TrimSpace(row.Values["price_list_code"])); listCode != "" && a.entity == string(dataexchange.EntityPriceList) {
			if err := check(`SELECT EXISTS(SELECT 1 FROM price_lists WHERE company_id=$1 AND code=$2)`, "price_list_code", "unknown_price_list", "fiyat listesi bulunamadı", a.companyID, listCode); err != nil {
				return dataexchange.ValidationResult{}, err
			}
		}
		if warehouseCode := strings.ToUpper(strings.TrimSpace(row.Values["warehouse_code"])); warehouseCode != "" && a.entity == string(dataexchange.EntityOpeningStock) {
			if err := check(`SELECT EXISTS(SELECT 1 FROM warehouses WHERE company_id=$1 AND code=$2 AND is_active AND warehouse_type='STANDARD')`, "warehouse_code", "unknown_warehouse", "aktif standart depo bulunamadı", a.companyID, warehouseCode); err != nil {
				return dataexchange.ValidationResult{}, err
			}
		}
		if unit := strings.ToUpper(strings.TrimSpace(row.Values["unit"])); unit != "" && a.entity == string(dataexchange.EntityProduct) {
			if err := check(`SELECT EXISTS(SELECT 1 FROM units WHERE code=$1)`, "unit", "unknown_unit", "birim bulunamadı", unit); err != nil {
				return dataexchange.ValidationResult{}, err
			}
		}
		extraIssues, err := a.validateExtendedRow(ctx, row)
		if err != nil {
			return dataexchange.ValidationResult{}, err
		}
		issues = append(issues, extraIssues...)
	}
	return dataexchange.ValidationResult{Issues: issues}, nil
}
func (catalogAdapter) Commit(context.Context, dataexchange.CommitInput) error {
	return errors.New("alan aktarım bağlayıcısı yapılandırılmamış")
}

func requiredValues(fields []dataexchange.FieldSpec) dataexchange.ValidatorFunc {
	return func(rows []dataexchange.MappedRow) []dataexchange.Issue {
		var issues []dataexchange.Issue
		for _, row := range rows {
			for _, field := range fields {
				if field.Required && strings.TrimSpace(row.Values[field.Name]) == "" {
					issues = append(issues, dataexchange.Issue{RowNumber: row.RowNumber, Field: field.Name, Code: "required_value", Severity: dataexchange.SeverityError, Message: "zorunlu alan boş bırakılamaz"})
				}
			}
		}
		return issues
	}
}

func duplicateKeyValidator(field string) dataexchange.ValidatorFunc {
	return duplicateCompositeValidator(field)
}

func duplicateCompositeValidator(fields ...string) dataexchange.ValidatorFunc {
	return func(rows []dataexchange.MappedRow) []dataexchange.Issue {
		seen := make(map[string]int, len(rows))
		issues := make([]dataexchange.Issue, 0)
		for _, row := range rows {
			parts := make([]string, len(fields))
			empty := true
			for index, field := range fields {
				parts[index] = normalizedDuplicateValue(field, row.Values[field])
				if parts[index] != "" {
					empty = false
				}
			}
			if empty {
				continue
			}
			key := strings.Join(parts, "\x1f")
			if previous, exists := seen[key]; exists {
				issues = append(issues, dataexchange.Issue{RowNumber: row.RowNumber, Field: strings.Join(fields, ","), Code: "duplicate_row", Severity: dataexchange.SeverityError, Message: fmt.Sprintf("satır, %d numaralı satırın tekrarıdır", previous)})
				continue
			}
			seen[key] = row.RowNumber
		}
		return issues
	}
}

func normalizedDuplicateValue(field, raw string) string {
	value := strings.TrimSpace(raw)
	switch field {
	case "product_code", "variant_code", "warehouse_code", "price_list_code", "code", "party_code":
		return strings.ToUpper(value)
	default:
		return value
	}
}

func (s *Service) Analyze(ctx context.Context, companyID, id string, input AnalyzeInput) (Analysis, error) {
	job, err := s.claimAnalyze(ctx, companyID, id)
	if err != nil {
		return Analysis{}, err
	}
	analysisStored := false
	defer func() {
		if !analysisStored {
			_, _ = s.pool.Exec(context.WithoutCancel(ctx), `UPDATE import_jobs SET status='FAILED',last_error='ANALYZE_FAILED',updated_at=now() WHERE company_id=$1 AND id=$2 AND status='ANALYZING'`, companyID, id)
		}
	}()
	spec, ok := specFor(job.EntityType)
	if !ok {
		return Analysis{}, fmt.Errorf("geçersiz aktarım türü")
	}
	reader, _, err := s.store.Open(ctx, "imports/"+companyID+"/"+id+"/source"+strings.ToLower(filepath.Ext(job.SourceFilename)))
	if err != nil {
		return Analysis{}, err
	}
	defer reader.Close()
	table, err := dataexchange.ReadTable(reader, job.SourceFilename)
	if err != nil {
		return Analysis{}, err
	}
	validators := []dataexchange.Validator{requiredValues(spec.Fields)}
	switch job.EntityType {
	case string(dataexchange.EntityProduct):
		validators = append(validators, duplicateKeyValidator("product_code"))
	case string(dataexchange.EntityVariant):
		validators = append(validators, duplicateCompositeValidator("product_code", "variant_code"))
	case string(dataexchange.EntityBarcode):
		validators = append(validators, duplicateKeyValidator("barcode"))
	case string(dataexchange.EntityWarehouse):
		validators = append(validators, duplicateKeyValidator("warehouse_code"))
	case string(dataexchange.EntityParty):
		validators = append(validators, duplicateKeyValidator("code"))
	case string(dataexchange.EntityOpeningStock):
		validators = append(validators, duplicateCompositeValidator("warehouse_code", "product_code", "variant_code"))
	case string(dataexchange.EntityStockCount):
		validators = append(validators, duplicateKeyValidator("line_no"))
	}
	for _, field := range spec.Fields {
		priceField := (job.EntityType == string(dataexchange.EntityProduct) || job.EntityType == string(dataexchange.EntityVariant)) && (field.Name == "purchase_price" || field.Name == "sales_price")
		quantityField := (job.EntityType == string(dataexchange.EntityOpeningStock) && field.Name == "quantity") || (job.EntityType == string(dataexchange.EntityStockCount) && field.Name == "counted_quantity") || (job.EntityType == string(dataexchange.EntityPriceList) && field.Name == "price")
		if priceField || quantityField {
			validators = append(validators, dataexchange.QuantityRule{Field: field.Name, Required: job.EntityType == string(dataexchange.EntityOpeningStock) || job.EntityType == string(dataexchange.EntityPriceList), MaxScale: 8})
		}
	}
	engine, err := dataexchange.NewEngine(catalogAdapter{pool: s.pool, companyID: companyID, entity: job.EntityType, actorID: input.ActorUserID, allowOpeningStock: input.AllowOpeningStock}, spec.Fields, validators...)
	if err != nil {
		return Analysis{}, err
	}
	preview, err := engine.Preview(ctx, dataexchange.ProcessRequest{Job: dataexchange.ImportJob{ID: job.ID, CompanyID: job.CompanyID, State: dataexchange.JobStatePending}, Table: table, Mapping: dataexchange.MappingOptions{Manual: input.ManualMapping}})
	if err != nil {
		return Analysis{}, err
	}
	errorCount, warningCount := 0, 0
	for _, row := range preview.Rows {
		for _, issue := range row.Issues {
			if issue.IsError() {
				errorCount++
			}
			if issue.IsWarning() {
				warningCount++
			}
		}
	}
	status := "READY"
	if !preview.CanCommit {
		status = "FAILED"
	}
	revision := analysisRevision(job.SourceSHA256, job.EntityType, input.ManualMapping)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Analysis{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	result, err := tx.Exec(ctx, `UPDATE import_jobs SET status=$1,dry_run=$2,row_count=$3,error_count=$4,warning_count=$5,analysis_revision=$6,preview_hash=$6,last_error='',updated_at=now() WHERE company_id=$7 AND id=$8 AND status='ANALYZING'`, status, input.DryRun, preview.TotalRows, errorCount, warningCount, revision, companyID, id)
	if err != nil {
		return Analysis{}, err
	}
	if result.RowsAffected() != 1 {
		return Analysis{}, ErrCommitInProgress
	}
	if _, err = tx.Exec(ctx, `DELETE FROM import_mappings WHERE company_id=$1 AND import_job_id=$2`, companyID, id); err != nil {
		return Analysis{}, err
	}
	for _, column := range preview.Mapping.Columns {
		if _, err = tx.Exec(ctx, `INSERT INTO import_mappings(company_id,import_job_id,source_column,target_field,mapping_source) VALUES($1,$2,$3,$4,$5)`, companyID, id, column.SourceHeader, column.Field, column.Method); err != nil {
			return Analysis{}, err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM import_row_results WHERE company_id=$1 AND import_job_id=$2`, companyID, id); err != nil {
		return Analysis{}, err
	}
	for _, row := range preview.Rows {
		errorsJSON, warningsJSON := issueJSON(row.Issues, dataexchange.SeverityError), issueJSON(row.Issues, dataexchange.SeverityWarning)
		rowKey := strings.TrimSpace(row.Values["line_no"])
		if rowKey == "" {
			rowKey = strconv.Itoa(row.RowNumber)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO import_row_results(company_id,import_job_id,row_number,row_key,status,values,errors,warnings) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, companyID, id, row.RowNumber, rowKey, persistedRowStatus(row), row.Values, errorsJSON, warningsJSON); err != nil {
			return Analysis{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Analysis{}, err
	}
	analysisStored = true
	job, err = s.Get(ctx, companyID, id)
	if err != nil {
		return Analysis{}, err
	}
	preview.Job = dataexchange.ImportJob{ID: job.ID, CompanyID: job.CompanyID, State: dataexchange.JobStatePreviewed}
	return Analysis{Job: job, AnalysisRevision: job.AnalysisRevision, Preview: preview, Mapping: preview.Mapping.Columns}, nil
}

func (s *Service) claimAnalyze(ctx context.Context, companyID, id string) (Job, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM import_jobs WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Job{}, inventory.ErrNotFound
		}
		return Job{}, err
	}
	switch status {
	case "COMMITTED":
		return Job{}, fmt.Errorf("%w: aktarım zaten tamamlandı", ErrNotReady)
	case "COMMITTING", "ANALYZING":
		return Job{}, ErrCommitInProgress
	case "UPLOADED", "READY", "FAILED":
	default:
		return Job{}, fmt.Errorf("%w: aktarım analiz edilemez", ErrNotReady)
	}
	if _, err := tx.Exec(ctx, `UPDATE import_jobs SET status='ANALYZING',last_error='',updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, id); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Job{}, err
	}
	return s.Get(ctx, companyID, id)
}

// Commit applies one complete, company-scoped adapter batch. Stock-affecting
// entities delegate to inventory command boundaries; catalog entities use the
// same transaction and deterministic row identities for safe retries.
func (s *Service) Commit(ctx context.Context, companyID, actorID, id string, allowOpeningStock bool, requestedRevision ...string) (Job, error) {
	if s == nil || s.pool == nil {
		return Job{}, errors.New("aktarım hizmeti yapılandırılmamış")
	}
	expectedRevision := ""
	if len(requestedRevision) > 0 {
		expectedRevision = strings.TrimSpace(requestedRevision[0])
	}
	job, commitToken, alreadyCommitted, err := s.claimCommit(ctx, companyID, id, expectedRevision)
	if err != nil {
		return Job{}, err
	}
	if alreadyCommitted {
		return job, nil
	}
	switch job.EntityType {
	case string(dataexchange.EntityStockCount):
		if s.stock == nil {
			err = errors.New("stok sayımı aktarım bağlayıcısı yapılandırılmamış")
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
		if job.TargetID == nil || strings.TrimSpace(*job.TargetID) == "" {
			err = fmt.Errorf("stok sayımı aktarımı için sayım kimliği zorunludur")
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
		lines, lineErr := s.stockCountImportLines(ctx, companyID, actorID, *job.TargetID, id)
		if lineErr != nil {
			_ = s.releaseCommit(ctx, companyID, id, commitToken, lineErr)
			return Job{}, lineErr
		}
		if err = s.stock.ApplyStockCountImport(ctx, companyID, *job.TargetID, actorID, lines); err != nil {
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
	case string(dataexchange.EntityOpeningStock):
		if s.stock == nil {
			err = errors.New("açılış stoku aktarım bağlayıcısı yapılandırılmamış")
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
		lines, lineErr := s.openingStockImportLines(ctx, companyID, id)
		if lineErr != nil {
			_ = s.releaseCommit(ctx, companyID, id, commitToken, lineErr)
			return Job{}, lineErr
		}
		if err = s.stock.PostOpeningStockImport(ctx, companyID, actorID, id, lines); err != nil {
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
	case string(dataexchange.EntityProduct), string(dataexchange.EntityVariant), string(dataexchange.EntityBarcode), string(dataexchange.EntityWarehouse), string(dataexchange.EntityPriceList), string(dataexchange.EntityParty):
		if err = s.commitCatalogImport(ctx, companyID, actorID, id, job.EntityType, allowOpeningStock); err != nil {
			_ = s.releaseCommit(ctx, companyID, id, commitToken, err)
			return Job{}, err
		}
	default:
		_ = s.releaseCommit(ctx, companyID, id, commitToken, errors.New("bu veri türü için alan aktarım bağlayıcısı yapılandırılmamış"))
		return Job{}, errors.New("bu veri türü için alan aktarım bağlayıcısı yapılandırılmamış")
	}
	result, err := s.pool.Exec(ctx, `UPDATE import_jobs SET status='COMMITTED',committed_at=now(),committing_at=NULL,commit_token=NULL,last_error='',updated_at=now() WHERE company_id=$1 AND id=$2 AND status='COMMITTING' AND commit_token=$3`, companyID, id, commitToken)
	if err != nil {
		// The domain transaction may already have committed when the metadata
		// connection reports an uncertain result. Never make the client repeat
		// a successful import just because this final state write was lost.
		if committed, statusErr := s.Get(ctx, companyID, id); statusErr == nil && committed.Status == "COMMITTED" {
			return committed, nil
		}
		return Job{}, err
	}
	if result.RowsAffected() != 1 {
		current, statusErr := s.Get(ctx, companyID, id)
		if statusErr == nil && current.Status == "COMMITTED" {
			return current, nil
		}
		if statusErr != nil {
			return Job{}, statusErr
		}
		return Job{}, ErrCommitInProgress
	}
	return s.Get(ctx, companyID, id)
}

// claimCommit atomically reserves the job for one caller. A stale COMMITTING
// claim is released after the timeout; catalog row identities are deterministic
// so a retry remains idempotent after an uncertain network result.
func (s *Service) claimCommit(ctx context.Context, companyID, id, expectedRevision string) (Job, string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Job{}, "", false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var job Job
	var target *string
	err = tx.QueryRow(ctx, `SELECT id,company_id,entity_type,direction,target_id,status,commit_mode,dry_run,source_filename,source_content_type,source_sha256,row_count,error_count,warning_count,analysis_revision,preview_hash,committing_at,last_error,created_at,updated_at FROM import_jobs WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, id).Scan(&job.ID, &job.CompanyID, &job.EntityType, &job.Direction, &target, &job.Status, &job.CommitMode, &job.DryRun, &job.SourceFilename, &job.SourceContentType, &job.SourceSHA256, &job.RowCount, &job.ErrorCount, &job.WarningCount, &job.AnalysisRevision, &job.PreviewHash, &job.CommittingAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, "", false, inventory.ErrNotFound
	}
	if err != nil {
		return Job{}, "", false, err
	}
	job.TargetID = target
	if job.Status == "COMMITTED" {
		return job, "", true, nil
	}
	if job.Status == "COMMITTING" {
		if job.CommittingAt == nil || time.Since(job.CommittingAt.UTC()) < commitClaimTimeout {
			return Job{}, "", false, ErrCommitInProgress
		}
		if _, err = tx.Exec(ctx, `UPDATE import_jobs SET status='READY',committing_at=NULL,commit_token=NULL,last_error='',updated_at=now() WHERE company_id=$1 AND id=$2`, companyID, id); err != nil {
			return Job{}, "", false, err
		}
		job.Status = "READY"
	}
	if job.Status != "READY" {
		return Job{}, "", false, fmt.Errorf("%w: aktarım işlemini tamamlamadan önce analiz edin", ErrNotReady)
	}
	if expectedRevision == "" || job.AnalysisRevision != expectedRevision {
		return Job{}, "", false, ErrStalePreview
	}
	if job.AnalysisRevision == "" {
		return Job{}, "", false, ErrStalePreview
	}
	token := uuid.NewString()
	if _, err = tx.Exec(ctx, `UPDATE import_jobs SET status='COMMITTING',committing_at=now(),commit_token=$3,updated_at=now() WHERE company_id=$1 AND id=$2 AND status='READY'`, companyID, id, token); err != nil {
		return Job{}, "", false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Job{}, "", false, err
	}
	job.Status = "COMMITTING"
	job.CommittingAt = timePtr(time.Now().UTC())
	return job, token, false, nil
}

func (s *Service) releaseCommit(ctx context.Context, companyID, id, token string, cause error) error {
	message := "COMMIT_FAILED"
	if cause != nil && errors.Is(cause, ErrStalePreview) {
		message = "STALE_PREVIEW"
	}
	_, err := s.pool.Exec(ctx, `UPDATE import_jobs SET status='READY',committing_at=NULL,commit_token=NULL,last_error=$4,updated_at=now() WHERE company_id=$1 AND id=$2 AND status='COMMITTING' AND commit_token=$3`, companyID, id, token, message)
	return err
}

func timePtr(value time.Time) *time.Time { return &value }

func (s *Service) stockCountImportLines(ctx context.Context, companyID, actorID, countID, jobID string) ([]inventory.StockCountImportLine, error) {
	count, err := s.stock.GetStockCountEngine(ctx, companyID, countID, actorID)
	if err != nil {
		return nil, err
	}
	scopes := make(map[string]inventory.StockCountEngineScope, len(count.Scopes))
	for _, scope := range count.Scopes {
		scopes[scope.ID] = scope
	}
	rows, err := s.pool.Query(ctx, `SELECT row_number,values FROM import_row_results WHERE company_id=$1 AND import_job_id=$2 AND status IN ('VALID','WARNING') ORDER BY row_number`, companyID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]inventory.StockCountImportLine, 0)
	for rows.Next() {
		var rowNumber int
		var raw []byte
		if err = rows.Scan(&rowNumber, &raw); err != nil {
			return nil, err
		}
		values := map[string]string{}
		if err = json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("sayım aktarım satırı okunamadı")
		}
		if strings.TrimSpace(values["counted_quantity"]) == "" {
			continue
		}
		lineNo, parseLineErr := strconv.Atoi(strings.TrimSpace(values["line_no"]))
		if parseLineErr != nil || lineNo < 1 {
			return nil, fmt.Errorf("%w: sayım satır numarası geçersiz", dataexchange.ErrInvalidJob)
		}
		var scope inventory.StockCountEngineScope
		found := false
		for _, candidate := range scopes {
			if candidate.LineNo == lineNo {
				scope, found = candidate, true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: sayım satır numarası değiştirilemez", dataexchange.ErrInvalidJob)
		}
		if productCode := strings.TrimSpace(values["product_code"]); productCode != "" && productCode != scope.ProductCode {
			return nil, fmt.Errorf("%w: ürün kimliği değiştirilemez", dataexchange.ErrInvalidJob)
		}
		if variantCode := strings.TrimSpace(values["variant_code"]); variantCode != "" && variantCode != scope.VariantCode {
			return nil, fmt.Errorf("%w: varyant kimliği değiştirilemez", dataexchange.ErrInvalidJob)
		}
		quantity, parseErr := dataexchange.ParseQuantity(values["counted_quantity"])
		if parseErr != nil || quantity.IsNegative() || quantity.Scale() > 8 {
			return nil, fmt.Errorf("%w: sayılan miktar geçersiz", dataexchange.ErrInvalidJob)
		}
		result = append(result, inventory.StockCountImportLine{ScopeID: scope.ID, Quantity: quantity.String(), RowNumber: rowNumber})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) openingStockImportLines(ctx context.Context, companyID, jobID string) ([]inventory.OpeningStockImportLine, error) {
	rows, err := s.pool.Query(ctx, `SELECT row_number,values FROM import_row_results WHERE company_id=$1 AND import_job_id=$2 AND status IN ('VALID','WARNING') ORDER BY row_number`, companyID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]inventory.OpeningStockImportLine, 0)
	for rows.Next() {
		var rowNumber int
		var raw []byte
		if err = rows.Scan(&rowNumber, &raw); err != nil {
			return nil, err
		}
		values := map[string]string{}
		if err = json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("açılış stok satırı okunamadı")
		}
		quantity, parseErr := dataexchange.ParseQuantity(values["quantity"])
		if parseErr != nil || quantity.IsNegative() || quantity.IsZero() || quantity.Scale() > 8 {
			return nil, fmt.Errorf("%w: açılış stok miktarı geçersiz", dataexchange.ErrInvalidJob)
		}
		result = append(result, inventory.OpeningStockImportLine{WarehouseCode: values["warehouse_code"], ProductCode: values["product_code"], VariantCode: values["variant_code"], Quantity: quantity.String(), RowNumber: rowNumber})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func issueJSON(issues []dataexchange.Issue, severity dataexchange.Severity) []dataexchange.Issue {
	result := make([]dataexchange.Issue, 0)
	for _, issue := range issues {
		if issue.Severity == severity {
			result = append(result, issue)
		}
	}
	return result
}

func persistedRowStatus(value any) string {
	row := dataexchange.RowResult{}
	switch item := value.(type) {
	case dataexchange.RowResult:
		row = item
	case dataexchange.RowStatus:
		row.Status = item
	default:
		return "ERROR"
	}
	// The exchange engine calls an invalid row INVALID, while the durable
	// import schema uses ERROR for the same terminal row outcome.
	if row.Status == dataexchange.RowStatusInvalid {
		return "ERROR"
	}
	if row.HasWarnings() {
		return "WARNING"
	}
	return "VALID"
}

func (s *Service) Rows(ctx context.Context, companyID, id string, limit, offset int) ([]map[string]any, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `SELECT row_number,row_key,status,values,errors,warnings FROM import_row_results WHERE company_id=$1 AND import_job_id=$2 ORDER BY row_number LIMIT $3 OFFSET $4`, companyID, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		var number int
		var key, status string
		var values, rowErrors, warnings []byte
		if err = rows.Scan(&number, &key, &status, &values, &rowErrors, &warnings); err != nil {
			return nil, err
		}
		result = append(result, map[string]any{"row_number": number, "row_key": key, "status": status, "values": json.RawMessage(values), "errors": json.RawMessage(rowErrors), "warnings": json.RawMessage(warnings)})
	}
	return result, rows.Err()
}

func (s *Service) Errors(ctx context.Context, companyID, id string) ([]map[string]any, error) {
	rows, err := s.Rows(ctx, companyID, id, 500, 0)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if status, _ := row["status"].(string); status != "VALID" {
			result = append(result, row)
		}
	}
	return result, nil
}

func (s *Service) ErrorReportTable(ctx context.Context, companyID, id string) (dataexchange.Table, error) {
	job, err := s.Get(ctx, companyID, id)
	if err != nil {
		return dataexchange.Table{}, err
	}
	rows, err := s.Rows(ctx, companyID, id, 5000, 0)
	if err != nil {
		return dataexchange.Table{}, err
	}
	records := make([][]string, 0)
	for _, row := range rows {
		status, _ := row["status"].(string)
		if status == "VALID" {
			continue
		}
		rowNumber, _ := row["row_number"].(int)
		appendIssues := func(raw any) error {
			message, ok := raw.(json.RawMessage)
			if !ok {
				return nil
			}
			var issues []dataexchange.Issue
			if err := json.Unmarshal(message, &issues); err != nil {
				return err
			}
			for _, item := range issues {
				records = append(records, []string{strconv.Itoa(rowNumber), localizedIssueField(job.EntityType, item.Field), item.Code, localizedIssueSeverity(item.Severity), item.Message})
			}
			return nil
		}
		if err := appendIssues(row["errors"]); err != nil {
			return dataexchange.Table{}, err
		}
		if err := appendIssues(row["warnings"]); err != nil {
			return dataexchange.Table{}, err
		}
	}
	return dataexchange.NewTable([]string{"Kaynak Satır", "Alan", "Kod", "Seviye", "Açıklama"}, records)
}

func localizedIssueField(entity, field string) string {
	spec, ok := specFor(entity)
	if !ok || strings.TrimSpace(field) == "" {
		return field
	}
	labels := strings.Split(field, ",")
	for index, value := range labels {
		name := strings.TrimSpace(value)
		for _, candidate := range spec.Fields {
			if candidate.Name == name {
				labels[index] = candidate.Label
				break
			}
		}
	}
	return strings.Join(labels, ", ")
}

func localizedIssueSeverity(severity dataexchange.Severity) string {
	switch severity {
	case dataexchange.SeverityError:
		return "Hata"
	case dataexchange.SeverityWarning:
		return "Uyarı"
	default:
		return strings.TrimSpace(string(severity))
	}
}

func (s *Service) CreateStockCountExport(ctx context.Context, companyID, actorID, countID, format string) (ExportJob, error) {
	if s.stock == nil || s.store == nil {
		return ExportJob{}, errors.New("dışa aktarım hizmeti yapılandırılmamış")
	}
	format = strings.ToUpper(strings.TrimSpace(format))
	if format == "" {
		format = "XLSX"
	}
	if format != "CSV" && format != "XLSX" {
		return ExportJob{}, fmt.Errorf("dosya biçimi CSV veya XLSX olmalıdır")
	}
	count, err := s.stock.GetStockCountEngine(ctx, companyID, countID, actorID)
	if err != nil {
		return ExportJob{}, err
	}
	headers := []string{"Sayım Satır No", "Ürün Kodu", "Ürün Adı", "Varyant Kodu", "Barkod", "Birim", "Sistem Miktarı", "Sayılan Miktar", "Fark", "Durum"}
	exceptionScopes := make(map[string]bool, len(count.Exceptions))
	for _, exception := range count.Exceptions {
		if exception.ScopeID != nil && strings.ToUpper(exception.Status) != "RESOLVED" {
			exceptionScopes[*exception.ScopeID] = true
		}
	}
	records := make([][]string, 0, len(count.Scopes))
	for _, line := range count.Scopes {
		counted := ""
		status := "Sayılmadı"
		if line.CountedQuantity != nil {
			counted = *line.CountedQuantity
			status = "Sayıldı"
		}
		if line.Difference != nil && trimExportDecimal(*line.Difference) != "0" {
			status = "Fark var"
		}
		if exceptionScopes[line.ID] {
			status = "İncele"
		}
		records = append(records, []string{strconv.Itoa(line.LineNo), line.ProductCode, line.ProductName, line.VariantCode, line.Barcode, line.UnitCode, valueOrEmptyExport(line.ExpectedQuantity), trimExportDecimal(counted), valueOrEmptyExport(line.Difference), status})
	}
	table, err := dataexchange.NewTable(headers, records)
	if err != nil {
		return ExportJob{}, err
	}
	var payload bytes.Buffer
	if format == "CSV" {
		err = dataexchange.WriteCSV(&payload, table)
	} else {
		err = dataexchange.WriteXLSX(&payload, table)
	}
	if err != nil {
		return ExportJob{}, err
	}
	id := uuid.NewString()
	ext := strings.ToLower(format)
	contentType := "text/csv"
	if format == "XLSX" {
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	key := fmt.Sprintf("exports/%s/%s/stock-count-%s.%s", companyID, id, countID, ext)
	object, err := s.store.Put(ctx, key, bytes.NewReader(payload.Bytes()), storage.PutOptions{ContentType: contentType})
	if err != nil {
		return ExportJob{}, err
	}
	filename := fmt.Sprintf("stok-sayimi-%s.%s", count.CountNo, ext)
	_, err = s.pool.Exec(ctx, `INSERT INTO export_jobs(id,company_id,entity_type,format,status,artifact_storage_key,filename,content_type,row_count,requested_by,completed_at) VALUES($1,$2,'STOCK_COUNT',$3,'COMPLETED',$4,$5,$6,$7,$8,now())`, id, companyID, format, key, filename, contentType, len(records), actorID)
	if err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), key)
		return ExportJob{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO export_artifacts(company_id,export_job_id,storage_key,sha256,size_bytes) VALUES($1,$2,$3,$4,$5)`, companyID, id, key, object.SHA256, object.Size)
	if err != nil {
		return ExportJob{}, err
	}
	return s.GetExport(ctx, companyID, id)
}

func valueOrEmptyExport(value *string) string {
	if value == nil {
		return ""
	}
	return trimExportDecimal(*value)
}

func trimExportDecimal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	parts := strings.SplitN(unsigned, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if fraction == "" {
		if integer == "0" {
			return "0"
		}
		if negative {
			return "-" + integer
		}
		return integer
	}
	result := integer + "." + fraction
	if negative && result != "0" {
		return "-" + result
	}
	return result
}

func (s *Service) GetExport(ctx context.Context, companyID, id string) (ExportJob, error) {
	var job ExportJob
	var completed *time.Time
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,entity_type,format,status,filename,row_count,created_at,completed_at FROM export_jobs WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&job.ID, &job.CompanyID, &job.EntityType, &job.Format, &job.Status, &job.Filename, &job.RowCount, &job.CreatedAt, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ExportJob{}, inventory.ErrNotFound
	}
	job.CompletedAt = completed
	return job, err
}

func (s *Service) OpenExport(ctx context.Context, companyID, id string) (io.ReadCloser, ExportJob, string, error) {
	job, err := s.GetExport(ctx, companyID, id)
	if err != nil {
		return nil, ExportJob{}, "", err
	}
	var key, contentType string
	if err = s.pool.QueryRow(ctx, `SELECT a.storage_key,e.content_type FROM export_artifacts a JOIN export_jobs e ON e.company_id=a.company_id AND e.id=a.export_job_id WHERE a.company_id=$1 AND a.export_job_id=$2`, companyID, id).Scan(&key, &contentType); err != nil {
		return nil, ExportJob{}, "", err
	}
	reader, _, err := s.store.Open(ctx, key)
	return reader, job, contentType, err
}
