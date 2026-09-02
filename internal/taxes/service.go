package taxes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/money"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type TaxDefinition struct {
	ID              string         `json:"id"`
	CompanyID       string         `json:"company_id"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Source          string         `json:"source"`
	SourceReference string         `json:"source_reference"`
	SourceVersion   string         `json:"source_version"`
	Rate            string         `json:"rate,omitempty"`
	CalculationType string         `json:"calculation_type,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	IsActive        bool           `json:"is_active"`
	Version         int64          `json:"version"`
}

type TaxRate struct {
	ID              string         `json:"id"`
	CompanyID       string         `json:"company_id"`
	TaxDefinitionID string         `json:"tax_definition_id"`
	Rate            string         `json:"rate"`
	CalculationType string         `json:"calculation_type"`
	ValidFrom       string         `json:"valid_from"`
	ValidTo         *string        `json:"valid_to,omitempty"`
	Source          string         `json:"source"`
	SourceReference string         `json:"source_reference"`
	SourceVersion   string         `json:"source_version"`
	Metadata        map[string]any `json:"metadata"`
	Version         int64          `json:"version"`
}

type Exemption struct {
	ID              string         `json:"id"`
	CompanyID       string         `json:"company_id"`
	Code            string         `json:"code"`
	Name            string         `json:"name"`
	LegalBasis      string         `json:"legal_basis"`
	Source          string         `json:"source"`
	SourceReference string         `json:"source_reference"`
	SourceVersion   string         `json:"source_version"`
	ValidFrom       string         `json:"valid_from"`
	ValidTo         *string        `json:"valid_to,omitempty"`
	Metadata        map[string]any `json:"metadata"`
	IsActive        bool           `json:"is_active"`
	Version         int64          `json:"version"`
}

type WithholdingRule struct {
	ID               string         `json:"id"`
	CompanyID        string         `json:"company_id"`
	Code             string         `json:"code"`
	Name             string         `json:"name"`
	Rate             string         `json:"rate"`
	RatioNumerator   *int           `json:"ratio_numerator,omitempty"`
	RatioDenominator *int           `json:"ratio_denominator,omitempty"`
	LegalBasis       string         `json:"legal_basis"`
	Source           string         `json:"source"`
	SourceReference  string         `json:"source_reference"`
	SourceVersion    string         `json:"source_version"`
	ValidFrom        string         `json:"valid_from"`
	ValidTo          *string        `json:"valid_to,omitempty"`
	Metadata         map[string]any `json:"metadata"`
	IsActive         bool           `json:"is_active"`
	Version          int64          `json:"version"`
}

var ErrNotFound = errors.New("tax record not found")
var ErrRateOverlap = errors.New("tax rate period overlaps another rate")

func (s *Service) ListDefinitions(ctx context.Context, session identity.Session, includeInactive bool) ([]TaxDefinition, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `SELECT d.id,d.company_id,d.code,d.name,d.description,d.source,d.source_reference,d.source_version,
		COALESCE((SELECT r.rate::text FROM tax_rates r WHERE r.company_id=d.company_id AND r.tax_definition_id=d.id
			AND r.valid_from <= CURRENT_DATE AND (r.valid_to IS NULL OR r.valid_to >= CURRENT_DATE)
			ORDER BY r.valid_from DESC,r.id LIMIT 1),'') AS rate,
		COALESCE((SELECT r.calculation_type FROM tax_rates r WHERE r.company_id=d.company_id AND r.tax_definition_id=d.id
			AND r.valid_from <= CURRENT_DATE AND (r.valid_to IS NULL OR r.valid_to >= CURRENT_DATE)
			ORDER BY r.valid_from DESC,r.id LIMIT 1),'PERCENTAGE') AS calculation_type,
		d.metadata,d.is_active,d.version FROM tax_definitions d WHERE d.company_id=$1`
	if !includeInactive {
		query += ` AND d.is_active`
	}
	query += ` ORDER BY d.code`
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaxDefinition{}
	for rows.Next() {
		var item TaxDefinition
		var metadata []byte
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.Source, &item.SourceReference, &item.SourceVersion, &item.Rate, &item.CalculationType, &metadata, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		item.Rate = normalizeDecimal(item.Rate)
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateDefinition(ctx context.Context, session identity.Session, input TaxDefinition, meta identity.RequestMeta) (TaxDefinition, error) {
	if !canManage(session) {
		return TaxDefinition{}, identity.ErrForbidden
	}
	if err := validateDefinition(input); err != nil {
		return TaxDefinition{}, err
	}
	input.CalculationType = normalizeCalculationType(input.CalculationType)
	if input.Rate != "" {
		var err error
		input.Rate, err = normalizeTaxValue(input.Rate, input.CalculationType)
		if err != nil {
			return TaxDefinition{}, err
		}
	}
	input.ID, input.CompanyID, input.IsActive, input.Version = uuid.NewString(), session.CurrentCompanyID, true, 1
	metadata := jsonBytes(input.Metadata)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TaxDefinition{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `INSERT INTO tax_definitions(id,company_id,code,name,description,source,source_reference,source_version,metadata)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, input.ID, input.CompanyID, input.Code, input.Name, input.Description, input.Source, input.SourceReference, input.SourceVersion, metadata); err != nil {
		return TaxDefinition{}, err
	}
	if input.Rate != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO tax_rates(id,company_id,tax_definition_id,rate,calculation_type,valid_from,source,source_reference,source_version,metadata) VALUES($1,$2,$3,$4,$5,CURRENT_DATE,$6,$7,$8,$9)`, uuid.NewString(), input.CompanyID, input.ID, input.Rate, input.CalculationType, input.Source, input.SourceReference, input.SourceVersion, metadata); err != nil {
			return TaxDefinition{}, err
		}
	}
	if err := s.writeEventTx(ctx, tx, session, "TAX_DEFINITION_CREATED", "tax.definition.created", input.ID, meta, nil); err != nil {
		return TaxDefinition{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaxDefinition{}, err
	}
	return input, nil
}

func (s *Service) UpdateDefinition(ctx context.Context, session identity.Session, id string, expectedVersion int64, input TaxDefinition, meta identity.RequestMeta) (TaxDefinition, error) {
	if !canManage(session) {
		return TaxDefinition{}, identity.ErrForbidden
	}
	if _, err := uuid.Parse(id); err != nil || expectedVersion < 1 || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Source) == "" {
		return TaxDefinition{}, fmt.Errorf("%w: vergi tanımı güncellemesi geçersiz", identity.ErrValidation)
	}
	var item TaxDefinition
	metadata := jsonBytes(input.Metadata)
	err := s.pool.QueryRow(ctx, `UPDATE tax_definitions SET name=$1,description=$2,source=$3,source_reference=$4,source_version=$5,metadata=$6,updated_at=now(),version=version+1 WHERE company_id=$7 AND id=$8 AND version=$9 RETURNING id,company_id,code,name,description,source,source_reference,source_version,metadata,is_active,version`, strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.TrimSpace(input.Source), strings.TrimSpace(input.SourceReference), strings.TrimSpace(input.SourceVersion), metadata, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.Source, &item.SourceReference, &item.SourceVersion, &metadata, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaxDefinition{}, identity.ErrConflict
	}
	if err != nil {
		return TaxDefinition{}, err
	}
	_ = json.Unmarshal(metadata, &item.Metadata)
	return item, s.writeEvent(ctx, session, "TAX_DEFINITION_UPDATED", "tax.definition.updated", id, meta, map[string]any{"version": item.Version})
}

func (s *Service) DeactivateDefinition(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (TaxDefinition, error) {
	if !canManage(session) {
		return TaxDefinition{}, identity.ErrForbidden
	}
	var item TaxDefinition
	var metadata []byte
	err := s.pool.QueryRow(ctx, `UPDATE tax_definitions SET is_active=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND is_active RETURNING id,company_id,code,name,description,source,source_reference,source_version,metadata,is_active,version`, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.Source, &item.SourceReference, &item.SourceVersion, &metadata, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return TaxDefinition{}, identity.ErrConflict
	}
	if err != nil {
		return TaxDefinition{}, err
	}
	_ = json.Unmarshal(metadata, &item.Metadata)
	return item, s.writeEvent(ctx, session, "TAX_DEFINITION_DEACTIVATED", "tax.definition.deactivated", id, meta, nil)
}

func (s *Service) ListRates(ctx context.Context, session identity.Session, definitionID, on string) ([]TaxRate, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	args := []any{session.CurrentCompanyID}
	query := `SELECT id,company_id,tax_definition_id,rate::text,calculation_type,valid_from::text,valid_to::text,source,source_reference,source_version,metadata,version FROM tax_rates WHERE company_id=$1`
	if definitionID != "" {
		if _, err := uuid.Parse(definitionID); err != nil {
			return nil, fmt.Errorf("%w: vergi tanımı kimliği geçersiz", identity.ErrValidation)
		}
		args = append(args, definitionID)
		query += fmt.Sprintf(" AND tax_definition_id=$%d", len(args))
	}
	if on != "" {
		if _, err := parseDate(on); err != nil {
			return nil, fmt.Errorf("%w: geçerlilik tarihi geçersiz", identity.ErrValidation)
		}
		args = append(args, on)
		n := len(args)
		query += fmt.Sprintf(" AND valid_from <= $%d::date AND (valid_to IS NULL OR valid_to >= $%d::date)", n, n)
	}
	query += ` ORDER BY tax_definition_id,valid_from DESC,id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []TaxRate{}
	for rows.Next() {
		var item TaxRate
		var metadata []byte
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.TaxDefinitionID, &item.Rate, &item.CalculationType, &item.ValidFrom, &item.ValidTo, &item.Source, &item.SourceReference, &item.SourceVersion, &metadata, &item.Version); err != nil {
			return nil, err
		}
		item.Rate = normalizeDecimal(item.Rate)
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateRate(ctx context.Context, session identity.Session, input TaxRate, meta identity.RequestMeta) (TaxRate, error) {
	if !canManage(session) {
		return TaxRate{}, identity.ErrForbidden
	}
	input.CalculationType = normalizeCalculationType(input.CalculationType)
	normalizedRate, err := normalizeTaxValue(input.Rate, input.CalculationType)
	if err != nil {
		return TaxRate{}, err
	}
	input.Rate = normalizedRate
	if err := validateRate(input); err != nil {
		return TaxRate{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TaxRate{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, session.CurrentCompanyID+":"+input.TaxDefinitionID); err != nil {
		return TaxRate{}, err
	}
	var overlap bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_rates WHERE company_id=$1 AND tax_definition_id=$2 AND valid_from <= COALESCE($4::date,'9999-12-31') AND COALESCE(valid_to,'9999-12-31') >= $3::date)`, session.CurrentCompanyID, input.TaxDefinitionID, input.ValidFrom, input.ValidTo).Scan(&overlap)
	if err != nil {
		return TaxRate{}, err
	}
	if overlap {
		return TaxRate{}, ErrRateOverlap
	}
	input.ID, input.CompanyID, input.Version = uuid.NewString(), session.CurrentCompanyID, 1
	if _, err = tx.Exec(ctx, `INSERT INTO tax_rates(id,company_id,tax_definition_id,rate,calculation_type,valid_from,valid_to,source,source_reference,source_version,metadata)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, input.ID, input.CompanyID, input.TaxDefinitionID, input.Rate, input.CalculationType, input.ValidFrom, input.ValidTo, strings.TrimSpace(input.Source), strings.TrimSpace(input.SourceReference), strings.TrimSpace(input.SourceVersion), jsonBytes(input.Metadata)); err != nil {
		return TaxRate{}, err
	}
	if err := s.writeEventTx(ctx, tx, session, "TAX_RATE_CREATED", "tax.rate.created", input.ID, meta, nil); err != nil {
		return TaxRate{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TaxRate{}, err
	}
	return input, nil
}

func (s *Service) ListExemptions(ctx context.Context, session identity.Session, includeInactive bool) ([]Exemption, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `SELECT id,company_id,code,name,legal_basis,source,source_reference,source_version,valid_from::text,valid_to::text,metadata,is_active,version FROM tax_exemptions WHERE company_id=$1`
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY code`
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Exemption{}
	for rows.Next() {
		var item Exemption
		var metadata []byte
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.LegalBasis, &item.Source, &item.SourceReference, &item.SourceVersion, &item.ValidFrom, &item.ValidTo, &metadata, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListWithholdingRules(ctx context.Context, session identity.Session, includeInactive bool) ([]WithholdingRule, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `SELECT id,company_id,code,name,rate::text,ratio_numerator,ratio_denominator,legal_basis,source,source_reference,source_version,valid_from::text,valid_to::text,metadata,is_active,version FROM tax_withholding_rules WHERE company_id=$1`
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY code`
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WithholdingRule{}
	for rows.Next() {
		var item WithholdingRule
		var metadata []byte
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Rate, &item.RatioNumerator, &item.RatioDenominator, &item.LegalBasis, &item.Source, &item.SourceReference, &item.SourceVersion, &item.ValidFrom, &item.ValidTo, &metadata, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		item.Rate = normalizeDecimal(item.Rate)
		_ = json.Unmarshal(metadata, &item.Metadata)
		if item.Metadata == nil {
			item.Metadata = map[string]any{}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validateDefinition(item TaxDefinition) error {
	if strings.TrimSpace(item.Code) == "" || strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Source) == "" {
		return fmt.Errorf("%w: vergi tanımı kodu, adı ve kaynağı gereklidir", identity.ErrValidation)
	}
	calculationType := normalizeCalculationType(item.CalculationType)
	if calculationType != "PERCENTAGE" && calculationType != "QUANTITY_BASED" {
		return fmt.Errorf("%w: vergi hesaplama türü geçersiz", identity.ErrValidation)
	}
	return nil
}

func normalizeCalculationType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "PERCENTAGE"
	}
	return value
}

func normalizeTaxValue(value, calculationType string) (string, error) {
	raw := strings.ReplaceAll(strings.TrimSpace(value), " ", "")
	if strings.Contains(raw, ",") {
		raw = strings.ReplaceAll(raw, ".", "")
		raw = strings.Replace(raw, ",", ".", 1)
	}
	parsed, err := money.ParseDecimal(raw, 8)
	if err != nil {
		return "", fmt.Errorf("%w: vergi değeri decimal olmalıdır", identity.ErrValidation)
	}
	if parsed.Sign() < 0 {
		return "", fmt.Errorf("%w: vergi değeri negatif olamaz", identity.ErrValidation)
	}
	if calculationType == "PERCENTAGE" {
		ratio, ok := new(big.Rat).SetString(parsed.String())
		if !ok || ratio.Cmp(big.NewRat(100, 1)) > 0 {
			return "", fmt.Errorf("%w: vergi oranı 100 değerini aşamaz", identity.ErrValidation)
		}
	}
	if calculationType != "PERCENTAGE" && calculationType != "QUANTITY_BASED" {
		return "", fmt.Errorf("%w: vergi hesaplama türü geçersiz", identity.ErrValidation)
	}
	return parsed.String(), nil
}

func validateRate(item TaxRate) error {
	if _, err := uuid.Parse(item.TaxDefinitionID); err != nil {
		return fmt.Errorf("%w: vergi tanımı kimliği geçersiz", identity.ErrValidation)
	}
	if _, err := parseDate(item.ValidFrom); err != nil {
		return fmt.Errorf("%w: başlangıç tarihi geçersiz", identity.ErrValidation)
	}
	if item.ValidTo != nil {
		if _, err := parseDate(*item.ValidTo); err != nil || *item.ValidTo < item.ValidFrom {
			return fmt.Errorf("%w: tarih aralığı geçersiz", identity.ErrValidation)
		}
	}
	if normalizeCalculationType(item.CalculationType) != "PERCENTAGE" && normalizeCalculationType(item.CalculationType) != "QUANTITY_BASED" {
		return fmt.Errorf("%w: vergi hesaplama türü geçersiz", identity.ErrValidation)
	}
	if _, err := normalizeTaxValue(item.Rate, normalizeCalculationType(item.CalculationType)); err != nil {
		return err
	}
	return nil
}
func parseDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(value))
}
func jsonBytes(value map[string]any) []byte {
	if value == nil {
		return []byte(`{}`)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return []byte(`{}`)
	}
	return encoded
}

func normalizeDecimal(value string) string {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok {
		return value
	}
	formatted := ratio.FloatString(18)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "-0" || formatted == "" {
		return "0"
	}
	return formatted
}
func canRead(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && (session.HasPermission("tax.read") || session.HasPermission("tax.manage"))
}
func canManage(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission("tax.manage")
}
func (s *Service) writeEvent(ctx context.Context, session identity.Session, eventType, outboxType, entityID string, meta identity.RequestMeta, extra map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := s.writeEventTx(ctx, tx, session, eventType, outboxType, entityID, meta, extra); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) writeEventTx(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, outboxType, entityID string, meta identity.RequestMeta, extra map[string]any) error {
	payload := map[string]any{"id": entityID}
	for key, value := range extra {
		payload[key] = value
	}
	details := jsonBytes(payload)
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, "tax", entityID, details, meta.TraceID, meta.IP, meta.UserAgent); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload)VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, details)
	return err
}
