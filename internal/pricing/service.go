package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Service struct{ pool database.Querier }

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Currency struct {
	CompanyID string `json:"company_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Symbol    string `json:"symbol"`
	MinorUnit int    `json:"minor_unit"`
	IsCustom  bool   `json:"is_custom"`
	Source    string `json:"source"`
	IsActive  bool   `json:"is_active"`
	Version   int64  `json:"version"`
}

type PriceList struct {
	ID                     string      `json:"id"`
	CompanyID              string      `json:"company_id"`
	Code                   string      `json:"code"`
	Name                   string      `json:"name"`
	Description            string      `json:"description"`
	AppliesToAllCategories bool        `json:"applies_to_all_categories"`
	ScopeCategoryID        *string     `json:"scope_category_id,omitempty"`
	CurrencyCode           string      `json:"currency_code"`
	TaxMode                TaxMode     `json:"tax_mode"`
	RoundPolicy            RoundPolicy `json:"round_policy"`
	RoundScale             int         `json:"round_scale"`
	IsActive               bool        `json:"is_active"`
	Version                int64       `json:"version"`
}

type PriceEntry struct {
	ID          string  `json:"id"`
	CompanyID   string  `json:"company_id"`
	PriceListID string  `json:"price_list_id"`
	ItemID      string  `json:"item_id"`
	VariantID   *string `json:"variant_id,omitempty"`
	ValidFrom   string  `json:"valid_from"`
	ValidTo     *string `json:"valid_to,omitempty"`
	UnitPrice   string  `json:"unit_price"`
	Version     int64   `json:"version"`
}

var (
	ErrNotFound = errors.New("pricing record not found")
	ErrOverlap  = errors.New("effective price period overlaps another price")
)

func (s *Service) ListCurrencies(ctx context.Context, session identity.Session, includeInactive bool) ([]Currency, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `SELECT company_id,code,name,symbol,minor_unit,is_custom,source,is_active,version FROM pricing_currencies WHERE company_id=$1`
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY code`
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Currency{}
	for rows.Next() {
		var item Currency
		if err = rows.Scan(&item.CompanyID, &item.Code, &item.Name, &item.Symbol, &item.MinorUnit, &item.IsCustom, &item.Source, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateCurrency(ctx context.Context, session identity.Session, input Currency, meta identity.RequestMeta) (Currency, error) {
	if !canManage(session) {
		return Currency{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name, input.Symbol, input.Source = strings.TrimSpace(input.Name), strings.TrimSpace(input.Symbol), strings.TrimSpace(input.Source)
	if !validCurrencyCode(input.Code) || input.Name == "" || input.MinorUnit < 0 || input.MinorUnit > 6 || input.Source == "" {
		return Currency{}, fmt.Errorf("%w: para birimi kodu, adı, hassasiyeti ve kaynağı geçerli olmalıdır", identity.ErrValidation)
	}
	input.CompanyID = session.CurrentCompanyID
	input.IsCustom, input.IsActive, input.Version = true, true, 1
	if _, err := s.pool.Exec(ctx, `INSERT INTO pricing_currencies(company_id,code,name,symbol,minor_unit,is_custom,source) VALUES($1,$2,$3,$4,$5,true,$6)`, input.CompanyID, input.Code, input.Name, input.Symbol, input.MinorUnit, input.Source); err != nil {
		return Currency{}, err
	}
	input, err := s.currency(ctx, session, input.Code)
	if err != nil {
		return Currency{}, err
	}
	return input, s.writeEvent(ctx, session, "PRICING_CURRENCY_CREATED", "pricing.currency.created", "", meta, map[string]any{"code": input.Code})
}

func (s *Service) UpdateCurrency(ctx context.Context, session identity.Session, code string, expectedVersion int64, input Currency, meta identity.RequestMeta) (Currency, error) {
	if !canManage(session) {
		return Currency{}, identity.ErrForbidden
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	input.Name, input.Symbol, input.Source = strings.TrimSpace(input.Name), strings.TrimSpace(input.Symbol), strings.TrimSpace(input.Source)
	if !validCurrencyCode(code) || expectedVersion < 1 || input.Name == "" || input.MinorUnit < 0 || input.MinorUnit > 6 || input.Source == "" {
		return Currency{}, fmt.Errorf("%w: para birimi güncellemesi geçersiz", identity.ErrValidation)
	}
	var item Currency
	err := s.pool.QueryRow(ctx, `UPDATE pricing_currencies SET name=$1,symbol=$2,minor_unit=$3,source=$4,updated_at=now(),version=version+1 WHERE company_id=$5 AND code=$6 AND version=$7 RETURNING company_id,code,name,symbol,minor_unit,is_custom,source,is_active,version`, input.Name, input.Symbol, input.MinorUnit, input.Source, session.CurrentCompanyID, code, expectedVersion).Scan(&item.CompanyID, &item.Code, &item.Name, &item.Symbol, &item.MinorUnit, &item.IsCustom, &item.Source, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return Currency{}, identity.ErrConflict
	}
	if err != nil {
		return Currency{}, err
	}
	return item, s.writeEvent(ctx, session, "PRICING_CURRENCY_UPDATED", "pricing.currency.updated", "", meta, map[string]any{"code": code, "version": item.Version})
}

func (s *Service) ListPriceLists(ctx context.Context, session identity.Session, includeInactive bool) ([]PriceList, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	query := `SELECT id,company_id,code,name,description,applies_to_all_categories,scope_category_id,currency_code,tax_mode,round_policy,round_scale,is_active,version FROM price_lists WHERE company_id=$1`
	if !includeInactive {
		query += ` AND is_active`
	}
	query += ` ORDER BY code`
	rows, err := s.pool.Query(ctx, query, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PriceList{}
	for rows.Next() {
		var item PriceList
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.AppliesToAllCategories, &item.ScopeCategoryID, &item.CurrencyCode, &item.TaxMode, &item.RoundPolicy, &item.RoundScale, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreatePriceList(ctx context.Context, session identity.Session, input PriceList, meta identity.RequestMeta) (PriceList, error) {
	if !canManage(session) {
		return PriceList{}, identity.ErrForbidden
	}
	input.Code, input.Name, input.Description, input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.Code)), strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "TRY"
	}
	if input.TaxMode == "" {
		input.TaxMode = TaxExclusive
	}
	if input.RoundPolicy == "" {
		input.RoundPolicy = RoundHalfUp
	}
	if input.Code == "" {
		input.Code = "PL-" + strings.ToUpper(uuid.NewString()[:8])
	}
	if input.ScopeCategoryID == nil {
		input.AppliesToAllCategories = true
	}
	if input.AppliesToAllCategories {
		input.ScopeCategoryID = nil
	}
	if !input.AppliesToAllCategories {
		if input.ScopeCategoryID == nil {
			return PriceList{}, fmt.Errorf("%w: fiyat tanımı için kategori seçin", identity.ErrValidation)
		}
		if _, err := uuid.Parse(strings.TrimSpace(*input.ScopeCategoryID)); err != nil {
			return PriceList{}, fmt.Errorf("%w: fiyat tanımı kategorisi geçersiz", identity.ErrValidation)
		}
	}
	if err := validatePriceList(input); err != nil {
		return PriceList{}, err
	}
	if err := s.requireCurrency(ctx, session, input.CurrencyCode); err != nil {
		return PriceList{}, err
	}
	input.ID, input.CompanyID, input.IsActive, input.Version = uuid.NewString(), session.CurrentCompanyID, true, 1
	if _, err := s.pool.Exec(ctx, `INSERT INTO price_lists(id,company_id,code,name,description,applies_to_all_categories,scope_category_id,currency_code,tax_mode,round_policy,round_scale) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, input.ID, input.CompanyID, input.Code, input.Name, input.Description, input.AppliesToAllCategories, input.ScopeCategoryID, input.CurrencyCode, input.TaxMode, input.RoundPolicy, input.RoundScale); err != nil {
		return PriceList{}, err
	}
	return input, s.writeEvent(ctx, session, "PRICING_LIST_CREATED", "pricing.price_list.created", input.ID, meta, nil)
}

func (s *Service) UpdatePriceList(ctx context.Context, session identity.Session, id string, expectedVersion int64, input PriceList, meta identity.RequestMeta) (PriceList, error) {
	if !canManage(session) {
		return PriceList{}, identity.ErrForbidden
	}
	input.Name, input.Description, input.CurrencyCode = strings.TrimSpace(input.Name), strings.TrimSpace(input.Description), strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "TRY"
	}
	if input.TaxMode == "" {
		input.TaxMode = TaxExclusive
	}
	if input.RoundPolicy == "" {
		input.RoundPolicy = RoundHalfUp
	}
	if input.ScopeCategoryID == nil {
		input.AppliesToAllCategories = true
	}
	if input.AppliesToAllCategories {
		input.ScopeCategoryID = nil
	}
	if !input.AppliesToAllCategories {
		if input.ScopeCategoryID == nil {
			return PriceList{}, fmt.Errorf("%w: fiyat tanımı için kategori seçin", identity.ErrValidation)
		}
		if _, err := uuid.Parse(strings.TrimSpace(*input.ScopeCategoryID)); err != nil {
			return PriceList{}, fmt.Errorf("%w: fiyat tanımı kategorisi geçersiz", identity.ErrValidation)
		}
	}
	if id == "" || expectedVersion < 1 || input.Name == "" || !validCurrencyCode(input.CurrencyCode) || !validTaxMode(input.TaxMode) || !validRoundPolicy(input.RoundPolicy) || input.RoundScale < 0 || input.RoundScale > 8 {
		return PriceList{}, fmt.Errorf("%w: fiyat listesi güncellemesi geçersiz", identity.ErrValidation)
	}
	if err := s.requireCurrency(ctx, session, input.CurrencyCode); err != nil {
		return PriceList{}, err
	}
	var currentCurrency string
	if err := s.pool.QueryRow(ctx, `SELECT currency_code FROM price_lists WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, id).Scan(&currentCurrency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PriceList{}, ErrNotFound
		}
		return PriceList{}, err
	}
	if currentCurrency != input.CurrencyCode {
		var hasEntries bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_list_entries WHERE company_id=$1 AND price_list_id=$2)`, session.CurrentCompanyID, id).Scan(&hasEntries); err != nil {
			return PriceList{}, err
		}
		if hasEntries {
			return PriceList{}, fmt.Errorf("%w: satırı olan fiyat listesinin para birimi değiştirilemez", identity.ErrValidation)
		}
	}
	var result PriceList
	err := s.pool.QueryRow(ctx, `UPDATE price_lists SET name=$1,description=$2,applies_to_all_categories=$3,scope_category_id=$4,currency_code=$5,tax_mode=$6,round_policy=$7,round_scale=$8,updated_at=now(),version=version+1 WHERE company_id=$9 AND id=$10 AND version=$11 RETURNING id,company_id,code,name,description,applies_to_all_categories,scope_category_id,currency_code,tax_mode,round_policy,round_scale,is_active,version`, input.Name, input.Description, input.AppliesToAllCategories, input.ScopeCategoryID, input.CurrencyCode, input.TaxMode, input.RoundPolicy, input.RoundScale, session.CurrentCompanyID, id, expectedVersion).Scan(&result.ID, &result.CompanyID, &result.Code, &result.Name, &result.Description, &result.AppliesToAllCategories, &result.ScopeCategoryID, &result.CurrencyCode, &result.TaxMode, &result.RoundPolicy, &result.RoundScale, &result.IsActive, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceList{}, identity.ErrConflict
	}
	if err != nil {
		return PriceList{}, err
	}
	return result, s.writeEvent(ctx, session, "PRICING_LIST_UPDATED", "pricing.price_list.updated", id, meta, map[string]any{"version": result.Version})
}

func (s *Service) DeactivatePriceList(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (PriceList, error) {
	if !canManage(session) {
		return PriceList{}, identity.ErrForbidden
	}
	var item PriceList
	err := s.pool.QueryRow(ctx, `UPDATE price_lists SET is_active=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND is_active RETURNING id,company_id,code,name,description,applies_to_all_categories,scope_category_id,currency_code,tax_mode,round_policy,round_scale,is_active,version`, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.AppliesToAllCategories, &item.ScopeCategoryID, &item.CurrencyCode, &item.TaxMode, &item.RoundPolicy, &item.RoundScale, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceList{}, identity.ErrConflict
	}
	if err != nil {
		return PriceList{}, err
	}
	return item, s.writeEvent(ctx, session, "PRICING_LIST_DEACTIVATED", "pricing.price_list.deactivated", id, meta, nil)
}

func (s *Service) ActivatePriceList(ctx context.Context, session identity.Session, id string, expectedVersion int64, meta identity.RequestMeta) (PriceList, error) {
	if !canManage(session) {
		return PriceList{}, identity.ErrForbidden
	}
	var item PriceList
	err := s.pool.QueryRow(ctx, `UPDATE price_lists SET is_active=true,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND NOT is_active RETURNING id,company_id,code,name,description,applies_to_all_categories,scope_category_id,currency_code,tax_mode,round_policy,round_scale,is_active,version`, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.CompanyID, &item.Code, &item.Name, &item.Description, &item.AppliesToAllCategories, &item.ScopeCategoryID, &item.CurrencyCode, &item.TaxMode, &item.RoundPolicy, &item.RoundScale, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceList{}, identity.ErrConflict
	}
	if err != nil {
		return PriceList{}, err
	}
	return item, s.writeEvent(ctx, session, "PRICING_LIST_ACTIVATED", "pricing.price_list.activated", id, meta, nil)
}

func (s *Service) ListPriceEntries(ctx context.Context, session identity.Session, priceListID, itemID, variantID, on string) ([]PriceEntry, error) {
	if !canRead(session) {
		return nil, identity.ErrForbidden
	}
	args := []any{session.CurrentCompanyID, priceListID}
	query := `SELECT id,company_id,price_list_id,item_id,variant_id,valid_from::text,valid_to::text,unit_price::text,version FROM price_list_entries WHERE company_id=$1 AND price_list_id=$2`
	if itemID != "" {
		args = append(args, itemID)
		query += fmt.Sprintf(" AND item_id=$%d", len(args))
	}
	if variantID != "" {
		if _, err := uuid.Parse(variantID); err != nil {
			return nil, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
		}
		args = append(args, variantID)
		query += fmt.Sprintf(" AND variant_id=$%d", len(args))
	}
	if on != "" {
		if _, err := parseDate(on); err != nil {
			return nil, fmt.Errorf("%w: geçerlilik tarihi geçersiz", identity.ErrValidation)
		}
		args = append(args, on)
		n := len(args)
		query += fmt.Sprintf(" AND valid_from <= $%d::date AND (valid_to IS NULL OR valid_to >= $%d::date)", n, n)
	}
	query += ` ORDER BY item_id,variant_id NULLS FIRST,valid_from DESC,id`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PriceEntry{}
	for rows.Next() {
		var item PriceEntry
		if err = rows.Scan(&item.ID, &item.CompanyID, &item.PriceListID, &item.ItemID, &item.VariantID, &item.ValidFrom, &item.ValidTo, &item.UnitPrice, &item.Version); err != nil {
			return nil, err
		}
		item.UnitPrice = normalizeDecimal(item.UnitPrice)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreatePriceEntry(ctx context.Context, session identity.Session, input PriceEntry, meta identity.RequestMeta) (PriceEntry, error) {
	if !canManage(session) {
		return PriceEntry{}, identity.ErrForbidden
	}
	if err := validateEntry(input); err != nil {
		return PriceEntry{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PriceEntry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := s.lockAndCheckOverlap(ctx, tx, session, input.PriceListID, input.ItemID, input.VariantID, input.ValidFrom, input.ValidTo, ""); err != nil {
		return PriceEntry{}, err
	}
	if err := s.validateEntryTarget(ctx, tx, session.CurrentCompanyID, input.ItemID, input.VariantID); err != nil {
		return PriceEntry{}, err
	}
	input.ID, input.CompanyID, input.Version = uuid.NewString(), session.CurrentCompanyID, 1
	if _, err := tx.Exec(ctx, `INSERT INTO price_list_entries(id,company_id,price_list_id,item_id,variant_id,valid_from,valid_to,unit_price) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, input.ID, input.CompanyID, input.PriceListID, input.ItemID, input.VariantID, input.ValidFrom, input.ValidTo, input.UnitPrice); err != nil {
		return PriceEntry{}, err
	}
	if err := s.writeEventTx(ctx, tx, session, "PRICING_ENTRY_CREATED", "pricing.entry.created", input.ID, meta, nil); err != nil {
		return PriceEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PriceEntry{}, err
	}
	return input, nil
}

func (s *Service) UpdatePriceEntry(ctx context.Context, session identity.Session, id string, expectedVersion int64, input PriceEntry, meta identity.RequestMeta) (PriceEntry, error) {
	if !canManage(session) {
		return PriceEntry{}, identity.ErrForbidden
	}
	if id == "" || expectedVersion < 1 {
		return PriceEntry{}, fmt.Errorf("%w: fiyat satırı sürümü geçersiz", identity.ErrValidation)
	}
	if err := validateEntry(input); err != nil {
		return PriceEntry{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PriceEntry{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := s.lockAndCheckOverlap(ctx, tx, session, input.PriceListID, input.ItemID, input.VariantID, input.ValidFrom, input.ValidTo, id); err != nil {
		return PriceEntry{}, err
	}
	if err := s.validateEntryTarget(ctx, tx, session.CurrentCompanyID, input.ItemID, input.VariantID); err != nil {
		return PriceEntry{}, err
	}
	var result PriceEntry
	err = tx.QueryRow(ctx, `UPDATE price_list_entries SET price_list_id=$1,item_id=$2,variant_id=$3,valid_from=$4,valid_to=$5,unit_price=$6,updated_at=now(),version=version+1 WHERE company_id=$7 AND id=$8 AND version=$9 RETURNING id,company_id,price_list_id,item_id,variant_id,valid_from::text,valid_to::text,unit_price::text,version`, input.PriceListID, input.ItemID, input.VariantID, input.ValidFrom, input.ValidTo, input.UnitPrice, session.CurrentCompanyID, id, expectedVersion).Scan(&result.ID, &result.CompanyID, &result.PriceListID, &result.ItemID, &result.VariantID, &result.ValidFrom, &result.ValidTo, &result.UnitPrice, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceEntry{}, identity.ErrConflict
	}
	if err != nil {
		return PriceEntry{}, err
	}
	result.UnitPrice = normalizeDecimal(result.UnitPrice)
	if err := s.writeEventTx(ctx, tx, session, "PRICING_ENTRY_UPDATED", "pricing.entry.updated", id, meta, map[string]any{"version": result.Version}); err != nil {
		return PriceEntry{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PriceEntry{}, err
	}
	return result, nil
}

func (s *Service) ResolvePrice(ctx context.Context, session identity.Session, priceListID, itemID, variantID, on string) (PriceEntry, error) {
	if !canRead(session) {
		return PriceEntry{}, identity.ErrForbidden
	}
	if _, err := parseDate(on); err != nil {
		return PriceEntry{}, fmt.Errorf("%w: geçerlilik tarihi geçersiz", identity.ErrValidation)
	}
	if variantID != "" {
		if _, err := uuid.Parse(variantID); err != nil {
			return PriceEntry{}, fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
		}
		var belongs bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3)`, session.CurrentCompanyID, variantID, itemID).Scan(&belongs); err != nil {
			return PriceEntry{}, err
		}
		if !belongs {
			return PriceEntry{}, fmt.Errorf("%w: varyant ürünle eşleşmiyor", identity.ErrValidation)
		}
	}
	var item PriceEntry
	err := s.pool.QueryRow(ctx, `SELECT e.id,e.company_id,e.price_list_id,e.item_id,e.variant_id,e.valid_from::text,e.valid_to::text,e.unit_price::text,e.version FROM price_list_entries e JOIN price_lists p ON p.company_id=e.company_id AND p.id=e.price_list_id AND p.is_active WHERE e.company_id=$1 AND e.price_list_id=$2 AND e.item_id=$3 AND (e.variant_id=$4::uuid OR e.variant_id IS NULL) AND e.valid_from <= $5::date AND (e.valid_to IS NULL OR e.valid_to >= $5::date) ORDER BY (e.variant_id IS NULL),e.valid_from DESC,e.id LIMIT 1`, session.CurrentCompanyID, priceListID, itemID, nullableUUID(variantID), on).Scan(&item.ID, &item.CompanyID, &item.PriceListID, &item.ItemID, &item.VariantID, &item.ValidFrom, &item.ValidTo, &item.UnitPrice, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return PriceEntry{}, ErrNotFound
	}
	item.UnitPrice = normalizeDecimal(item.UnitPrice)
	return item, err
}

func (s *Service) currency(ctx context.Context, session identity.Session, code string) (Currency, error) {
	var item Currency
	err := s.pool.QueryRow(ctx, `SELECT company_id,code,name,symbol,minor_unit,is_custom,source,is_active,version FROM pricing_currencies WHERE company_id=$1 AND code=$2`, session.CurrentCompanyID, code).Scan(&item.CompanyID, &item.Code, &item.Name, &item.Symbol, &item.MinorUnit, &item.IsCustom, &item.Source, &item.IsActive, &item.Version)
	return item, err
}
func (s *Service) requireCurrency(ctx context.Context, session identity.Session, code string) error {
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT is_active FROM pricing_currencies WHERE company_id=$1 AND code=$2`, session.CurrentCompanyID, code).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: para birimi bulunamadı", identity.ErrValidation)
	}
	if err != nil {
		return err
	}
	if !active {
		return fmt.Errorf("%w: pasif para birimi kullanılamaz", identity.ErrValidation)
	}
	return nil
}
func (s *Service) lockAndCheckOverlap(ctx context.Context, tx pgx.Tx, session identity.Session, listID, itemID string, variantID *string, from string, to *string, exclude string) error {
	target := ""
	if variantID != nil {
		target = strings.TrimSpace(*variantID)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, session.CurrentCompanyID+":"+listID+":"+itemID+":"+target); err != nil {
		return err
	}
	var overlap bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_list_entries WHERE company_id=$1 AND price_list_id=$2 AND item_id=$3 AND variant_id IS NOT DISTINCT FROM NULLIF($4,'')::uuid AND (NULLIF($7,'') IS NULL OR id<>NULLIF($7,'')::uuid) AND valid_from <= COALESCE($6::date,'9999-12-31') AND COALESCE(valid_to,'9999-12-31') >= $5::date)`, session.CurrentCompanyID, listID, itemID, target, from, to, exclude).Scan(&overlap)
	if err != nil {
		return err
	}
	if overlap {
		return ErrOverlap
	}
	return nil
}

func (s *Service) validateEntryTarget(ctx context.Context, tx pgx.Tx, companyID, itemID string, variantID *string) error {
	if variantID == nil || strings.TrimSpace(*variantID) == "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND id=$2)`, companyID, itemID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: ürün bulunamadı", identity.ErrValidation)
		}
		return nil
	}
	if _, err := uuid.Parse(strings.TrimSpace(*variantID)); err != nil {
		return fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants v JOIN products p ON p.company_id=v.company_id AND p.id=v.product_id WHERE v.company_id=$1 AND v.id=$2 AND v.product_id=$3)`, companyID, *variantID, itemID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: varyant ürünle eşleşmiyor", identity.ErrValidation)
	}
	return nil
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
	for k, v := range extra {
		payload[k] = v
	}
	bytes, _ := json.Marshal(payload)
	_, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent)VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7,$8,$9,$10)`, uuid.NewString(), session.CurrentCompanyID, session.User.ID, eventType, "pricing", entityID, bytes, meta.TraceID, meta.IP, meta.UserAgent)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload)VALUES($1,$2,1,$3,$4,$5)`, uuid.NewString(), outboxType, session.CurrentCompanyID, meta.TraceID, bytes)
	return err
}

func canRead(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && (session.HasPermission("pricing.read") || session.HasPermission("pricing.manage"))
}
func canManage(session identity.Session) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission("pricing.manage")
}
func validCurrencyCode(value string) bool {
	if len(value) < 3 || len(value) > 8 {
		return false
	}
	for i, r := range value {
		if (r < 'A' || r > 'Z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}
func validatePriceList(item PriceList) error {
	if item.Code == "" || item.Name == "" || !validCurrencyCode(item.CurrencyCode) || !validTaxMode(item.TaxMode) || !validRoundPolicy(item.RoundPolicy) || item.RoundScale < 0 || item.RoundScale > 8 {
		return fmt.Errorf("%w: fiyat listesi bilgileri geçersiz", identity.ErrValidation)
	}
	if !item.AppliesToAllCategories {
		if item.ScopeCategoryID == nil {
			return fmt.Errorf("%w: fiyat tanımı için kategori seçin", identity.ErrValidation)
		}
		if _, err := uuid.Parse(strings.TrimSpace(*item.ScopeCategoryID)); err != nil {
			return fmt.Errorf("%w: fiyat tanımı kategorisi geçersiz", identity.ErrValidation)
		}
	}
	return nil
}
func validateEntry(item PriceEntry) error {
	if item.PriceListID == "" || item.ItemID == "" || item.UnitPrice == "" {
		return fmt.Errorf("%w: fiyat satırı bilgileri eksik", identity.ErrValidation)
	}
	if _, err := uuid.Parse(item.PriceListID); err != nil {
		return fmt.Errorf("%w: fiyat listesi kimliği geçersiz", identity.ErrValidation)
	}
	if _, err := uuid.Parse(item.ItemID); err != nil {
		return fmt.Errorf("%w: ürün/hizmet kimliği geçersiz", identity.ErrValidation)
	}
	if item.VariantID != nil {
		if _, err := uuid.Parse(strings.TrimSpace(*item.VariantID)); err != nil {
			return fmt.Errorf("%w: varyant kimliği geçersiz", identity.ErrValidation)
		}
	}
	if _, err := parseDate(item.ValidFrom); err != nil {
		return fmt.Errorf("%w: başlangıç tarihi geçersiz", identity.ErrValidation)
	}
	if item.ValidTo != nil {
		if _, err := parseDate(*item.ValidTo); err != nil {
			return fmt.Errorf("%w: bitiş tarihi geçersiz", identity.ErrValidation)
		}
		if *item.ValidTo < item.ValidFrom {
			return fmt.Errorf("%w: tarih aralığı geçersiz", identity.ErrValidation)
		}
	}
	if err := validateUnitPrice(item.UnitPrice); err != nil {
		return err
	}
	return nil
}
func validateUnitPrice(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: birim fiyat zorunludur", identity.ErrValidation)
	}
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%w: birim fiyat negatif olamaz", identity.ErrValidation)
	}
	if _, err := parseAmount(value, true); err != nil {
		return fmt.Errorf("%w: birim fiyat decimal olmalıdır", identity.ErrValidation)
	}
	return nil
}
func parseDate(value string) (time.Time, error) {
	return time.Parse("2006-01-02", strings.TrimSpace(value))
}

func normalizeDecimal(value string) string {
	ratio, err := parseAmount(value, true)
	if err != nil {
		return value
	}
	return formatExactScale(ratio, 18)
}

func nullableUUID(value string) any {
	if uuid.Validate(strings.TrimSpace(value)) != nil {
		return nil
	}
	return strings.TrimSpace(value)
}
