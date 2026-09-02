package products

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxVariantDefinitions  = 5
	maxVariantCombinations = 1000
)

type VariantValidationError struct{ Code, Message string }

func (e *VariantValidationError) Error() string { return e.Code + ": " + e.Message }
func (e *VariantValidationError) Unwrap() error { return identity.ErrValidation }

type VariantDefinition struct {
	ID        string          `json:"id"`
	CompanyID string          `json:"company_id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	IsActive  bool            `json:"is_active"`
	Options   []VariantOption `json:"options"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Version   int64           `json:"version"`
}
type VariantOption struct {
	ID           string    `json:"id"`
	CompanyID    string    `json:"company_id"`
	DefinitionID string    `json:"definition_id"`
	Code         string    `json:"code"`
	Name         string    `json:"name"`
	ShortCode    string    `json:"short_code"`
	SortOrder    int       `json:"sort_order"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Version      int64     `json:"version"`
}
type VariantDefinitionInput struct {
	Code        string               `json:"code"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	IsActive    *bool                `json:"is_active,omitempty"`
	Options     []VariantOptionInput `json:"options,omitempty"`
}
type VariantOptionInput struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortCode string `json:"short_code"`
	SortOrder int    `json:"sort_order"`
	IsActive  *bool  `json:"is_active,omitempty"`
}
type VariantPackage struct {
	Code        string                     `json:"code"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Definitions []VariantPackageDefinition `json:"definitions"`
}
type VariantPackageDefinition struct {
	Code     string                 `json:"code"`
	Name     string                 `json:"name"`
	Position int                    `json:"position"`
	Options  []VariantPackageOption `json:"options"`
}
type VariantPackageOption struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortCode string `json:"short_code"`
	Position  int    `json:"position"`
}

type ProductVariantDefinition struct {
	DefinitionID string          `json:"definition_id"`
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	Position     int             `json:"position"`
	IsActive     bool            `json:"is_active"`
	Options      []VariantOption `json:"options"`
}
type ProductVariantConfig struct {
	ProductID        string                     `json:"product_id"`
	VariantsEnabled  bool                       `json:"variants_enabled"`
	IdentityLocked   bool                       `json:"identity_locked"`
	MovementStarted  bool                       `json:"movement_started"`
	Version          int64                      `json:"version"`
	Definitions      []ProductVariantDefinition `json:"definitions"`
	CombinationCount int                        `json:"combination_count"`
}
type ProductVariantDefinitionInput struct {
	DefinitionID string   `json:"definition_id"`
	Position     int      `json:"position"`
	OptionIDs    []string `json:"option_ids"`
}
type ProductVariantConfigInput struct {
	VariantsEnabled *bool                           `json:"variants_enabled,omitempty"`
	Definitions     []ProductVariantDefinitionInput `json:"definitions,omitempty"`
}
type VariantValueInput struct {
	DefinitionID string `json:"definition_id"`
	OptionID     string `json:"option_id"`
}
type VariantValue struct {
	DefinitionID    string `json:"definition_id"`
	DefinitionCode  string `json:"definition_code"`
	DefinitionName  string `json:"definition_name"`
	OptionID        string `json:"option_id"`
	OptionCode      string `json:"option_code"`
	OptionName      string `json:"option_name"`
	OptionShortCode string `json:"option_short_code"`
}
type Variant struct {
	ID                     string                 `json:"id"`
	CompanyID              string                 `json:"company_id"`
	ProductID              string                 `json:"product_id"`
	VariantCode            string                 `json:"variant_code"`
	Values                 []VariantValue         `json:"values"`
	Barcodes               []ProductBarcode       `json:"barcodes"`
	IsActive               bool                   `json:"is_active"`
	Locked                 bool                   `json:"identity_locked"`
	PurchasePriceOverride  string                 `json:"purchase_price_override,omitempty"`
	SalesPriceOverride     string                 `json:"sales_price_override,omitempty"`
	PurchasePrice          string                 `json:"purchase_price,omitempty"`
	SalesPrice             string                 `json:"sales_price,omitempty"`
	PurchasePriceInherited bool                   `json:"purchase_price_inherited"`
	SalesPriceInherited    bool                   `json:"sales_price_inherited"`
	PriceEntries           []VariantPriceEntry    `json:"price_entries"`
	PhysicalQuantity       string                 `json:"physical_quantity"`
	ReservedQuantity       string                 `json:"reserved_quantity"`
	AvailableQuantity      string                 `json:"available_quantity"`
	StockUnit              string                 `json:"stock_unit,omitempty"`
	StockPositions         []VariantStockPosition `json:"stock_positions"`
	CreatedAt              time.Time              `json:"created_at"`
	UpdatedAt              time.Time              `json:"updated_at"`
	Version                int64                  `json:"version"`
}
type VariantInput struct {
	VariantCode           string                   `json:"variant_code"`
	Values                []VariantValueInput      `json:"values"`
	Barcodes              []BarcodeInput           `json:"barcodes,omitempty"`
	IsActive              *bool                    `json:"is_active,omitempty"`
	PurchasePriceOverride *string                  `json:"purchase_price_override,omitempty"`
	SalesPriceOverride    *string                  `json:"sales_price_override,omitempty"`
	PriceEntries          []VariantPriceEntryInput `json:"price_entries,omitempty"`
}

type VariantPriceEntry struct {
	PriceListID string  `json:"price_list_id"`
	EntryID     string  `json:"entry_id,omitempty"`
	UnitPrice   string  `json:"unit_price,omitempty"`
	ValidFrom   string  `json:"valid_from,omitempty"`
	ValidTo     *string `json:"valid_to,omitempty"`
	Version     int64   `json:"version,omitempty"`
}

type VariantPriceEntryInput struct {
	PriceListID string  `json:"price_list_id"`
	EntryID     string  `json:"entry_id,omitempty"`
	UnitPrice   string  `json:"unit_price,omitempty"`
	ValidFrom   string  `json:"valid_from,omitempty"`
	ValidTo     *string `json:"valid_to,omitempty"`
	Version     int64   `json:"version,omitempty"`
}

type VariantStockPosition struct {
	WarehouseID   string `json:"warehouse_id"`
	WarehouseCode string `json:"warehouse_code,omitempty"`
	WarehouseName string `json:"warehouse_name,omitempty"`
	LocationID    string `json:"location_id,omitempty"`
	LocationCode  string `json:"location_code,omitempty"`
	LocationName  string `json:"location_name,omitempty"`
	Physical      string `json:"physical_quantity"`
	Reserved      string `json:"reserved_quantity"`
	Available     string `json:"available_quantity"`
	StockUnit     string `json:"stock_unit,omitempty"`
}

type variantConfigDefinition struct {
	DefinitionID, Code, Name string
	Position                 int
	Options                  []VariantOption
}

func variantValidation(code, message string) error {
	return &VariantValidationError{Code: code, Message: message}
}

func (s *Service) ListVariantPackages(ctx context.Context) ([]VariantPackage, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.code,p.name,p.description,d.definition_code,d.definition_name,d.position,o.option_code,o.option_name,o.short_code,o.position FROM variant_definition_packages p LEFT JOIN variant_definition_package_definitions d ON d.package_code=p.code LEFT JOIN variant_definition_package_options o ON o.package_code=d.package_code AND o.definition_code=d.definition_code WHERE p.is_active ORDER BY p.code,d.position,o.position,o.option_code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]VariantPackage, 0)
	pi := map[string]int{}
	di := map[string]int{}
	for rows.Next() {
		var pc, pn, desc string
		var dc, dn, oc, on, sc *string
		var dp, op *int
		if err = rows.Scan(&pc, &pn, &desc, &dc, &dn, &dp, &oc, &on, &sc, &op); err != nil {
			return nil, err
		}
		i, ok := pi[pc]
		if !ok {
			i = len(items)
			pi[pc] = i
			items = append(items, VariantPackage{Code: pc, Name: pn, Description: desc, Definitions: []VariantPackageDefinition{}})
		}
		if dc == nil {
			continue
		}
		key := pc + "\x00" + *dc
		j, ok := di[key]
		if !ok {
			j = len(items[i].Definitions)
			di[key] = j
			items[i].Definitions = append(items[i].Definitions, VariantPackageDefinition{Code: *dc, Name: *dn, Position: *dp, Options: []VariantPackageOption{}})
		}
		if oc != nil {
			items[i].Definitions[j].Options = append(items[i].Definitions[j].Options, VariantPackageOption{Code: *oc, Name: *on, ShortCode: *sc, Position: *op})
		}
	}
	return items, rows.Err()
}

func (s *Service) ListVariantDefinitions(ctx context.Context, session identity.Session) ([]VariantDefinition, error) {
	if !authorized(session, "product.read") && !authorized(session, "product.variant_definition.manage") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT d.id,d.company_id,d.code,d.name,d.is_active,d.created_at,d.updated_at,d.version,o.id,o.company_id,o.definition_id,o.code,o.name,o.short_code,o.sort_order,o.is_active,o.created_at,o.updated_at,o.version FROM variant_definitions d LEFT JOIN variant_definition_options o ON o.company_id=d.company_id AND o.definition_id=d.id WHERE d.company_id=$1 ORDER BY lower(d.name),d.id,o.sort_order,o.id`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []VariantDefinition{}
	idx := map[string]int{}
	for rows.Next() {
		var d VariantDefinition
		var oid, ocid, odid, ocode, oname, oshort *string
		var order *int
		var active *bool
		var ca, ua *time.Time
		var ver *int64
		if err = rows.Scan(&d.ID, &d.CompanyID, &d.Code, &d.Name, &d.IsActive, &d.CreatedAt, &d.UpdatedAt, &d.Version, &oid, &ocid, &odid, &ocode, &oname, &oshort, &order, &active, &ca, &ua, &ver); err != nil {
			return nil, err
		}
		i, ok := idx[d.ID]
		if !ok {
			i = len(items)
			idx[d.ID] = i
			d.Options = []VariantOption{}
			items = append(items, d)
		}
		if oid != nil {
			items[i].Options = append(items[i].Options, VariantOption{ID: *oid, CompanyID: *ocid, DefinitionID: *odid, Code: *ocode, Name: *oname, ShortCode: *oshort, SortOrder: *order, IsActive: *active, CreatedAt: *ca, UpdatedAt: *ua, Version: *ver})
		}
	}
	return items, rows.Err()
}

func (s *Service) CreateVariantDefinition(ctx context.Context, session identity.Session, input VariantDefinitionInput, meta identity.RequestMeta) (VariantDefinition, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantDefinition{}, identity.ErrForbidden
	}
	code := normalizeVariantCode(input.Code)
	name := strings.TrimSpace(input.Name)
	if code == "" || name == "" {
		return VariantDefinition{}, fmt.Errorf("%w: varyant tanımı kodu ve adı gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantDefinition{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO variant_definitions(id,company_id,code,name) VALUES($1,$2,$3,$4)`, id, session.CurrentCompanyID, code, name); err != nil {
		return VariantDefinition{}, mapConstraint(err)
	}
	if err = insertDefinitionOptions(ctx, tx, session.CurrentCompanyID, id, input.Options); err != nil {
		return VariantDefinition{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "VARIANT_DEFINITION_CREATED", "product.variant_definition.created", id, map[string]any{"code": code}, meta); err != nil {
		return VariantDefinition{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantDefinition{}, err
	}
	return s.getVariantDefinition(ctx, session, id)
}

func (s *Service) UpdateVariantDefinition(ctx context.Context, session identity.Session, id string, version int64, input VariantDefinitionInput, meta identity.RequestMeta) (VariantDefinition, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantDefinition{}, identity.ErrForbidden
	}
	name := strings.TrimSpace(input.Name)
	if version < 1 || name == "" {
		return VariantDefinition{}, fmt.Errorf("%w: varyant tanımı adı ve sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantDefinition{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var active bool
	if err = tx.QueryRow(ctx, `SELECT is_active FROM variant_definitions WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VariantDefinition{}, identity.ErrForbidden
		}
		return VariantDefinition{}, err
	}
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tag, err := tx.Exec(ctx, `UPDATE variant_definitions SET name=$1,is_active=$2,updated_at=now(),version=version+1 WHERE company_id=$3 AND id=$4 AND version=$5`, name, active, session.CurrentCompanyID, id, version)
	if err != nil {
		return VariantDefinition{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return VariantDefinition{}, identity.ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantDefinition{}, err
	}
	return s.getVariantDefinition(ctx, session, id)
}

func (s *Service) CreateVariantOption(ctx context.Context, session identity.Session, definitionID string, input VariantOptionInput, meta identity.RequestMeta) (VariantOption, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantOption{}, identity.ErrForbidden
	}
	o, err := normalizeOptionInput(input)
	if err != nil {
		return VariantOption{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantOption{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var active bool
	if err = tx.QueryRow(ctx, `SELECT is_active FROM variant_definitions WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, definitionID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VariantOption{}, identity.ErrForbidden
		}
		return VariantOption{}, err
	}
	if !active {
		return VariantOption{}, variantValidation("VARIANT_DEFINITION_INACTIVE", "Pasif varyant tanımına seçenek eklenemez")
	}
	id := uuid.NewString()
	if o.SortOrder == 0 {
		o.SortOrder = 1
	}
	if _, err = tx.Exec(ctx, `INSERT INTO variant_definition_options(id,company_id,definition_id,code,name,short_code,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, session.CurrentCompanyID, definitionID, o.Code, o.Name, o.ShortCode, o.SortOrder); err != nil {
		return VariantOption{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "VARIANT_OPTION_CREATED", "product.variant_option.created", id, map[string]any{"definition_id": definitionID, "code": o.Code}, meta); err != nil {
		return VariantOption{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantOption{}, err
	}
	return s.getVariantOption(ctx, session.CurrentCompanyID, id)
}

func (s *Service) UpdateVariantOption(ctx context.Context, session identity.Session, definitionID, optionID string, version int64, input VariantOptionInput, meta identity.RequestMeta) (VariantOption, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantOption{}, identity.ErrForbidden
	}
	if version < 1 {
		return VariantOption{}, fmt.Errorf("%w: varyant seçeneği sürümü gereklidir", identity.ErrValidation)
	}
	o, err := normalizeOptionInput(input)
	if err != nil {
		return VariantOption{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantOption{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var oldCode, oldShort string
	var active bool
	if err = tx.QueryRow(ctx, `SELECT code,short_code,is_active FROM variant_definition_options WHERE company_id=$1 AND definition_id=$2 AND id=$3 FOR UPDATE`, session.CurrentCompanyID, definitionID, optionID).Scan(&oldCode, &oldShort, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VariantOption{}, identity.ErrForbidden
		}
		return VariantOption{}, err
	}
	var used bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variant_values WHERE company_id=$1 AND option_id=$2)`, session.CurrentCompanyID, optionID).Scan(&used); err != nil {
		return VariantOption{}, err
	}
	if used && (oldCode != o.Code || oldShort != o.ShortCode) {
		return VariantOption{}, variantValidation("VARIANT_OPTION_LOCKED", "Kullanılmış varyant seçeneğinin kodu değiştirilemez")
	}
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tag, err := tx.Exec(ctx, `UPDATE variant_definition_options SET code=$1,name=$2,short_code=$3,sort_order=$4,is_active=$5,updated_at=now(),version=version+1 WHERE company_id=$6 AND definition_id=$7 AND id=$8 AND version=$9`, o.Code, o.Name, o.ShortCode, o.SortOrder, active, session.CurrentCompanyID, definitionID, optionID, version)
	if err != nil {
		return VariantOption{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return VariantOption{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "VARIANT_OPTION_UPDATED", "product.variant_option.updated", optionID, map[string]any{"definition_id": definitionID}, meta); err != nil {
		return VariantOption{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantOption{}, err
	}
	return s.getVariantOption(ctx, session.CurrentCompanyID, optionID)
}

func (s *Service) GetVariantConfig(ctx context.Context, session identity.Session, productID string) (ProductVariantConfig, error) {
	if !authorized(session, "product.read") {
		return ProductVariantConfig{}, identity.ErrForbidden
	}
	if err := s.ensureProduct(ctx, session.CurrentCompanyID, productID); err != nil {
		return ProductVariantConfig{}, err
	}
	return loadVariantConfig(ctx, s.pool, session.CurrentCompanyID, productID)
}

func (s *Service) UpdateVariantConfig(ctx context.Context, session identity.Session, productID string, input ProductVariantConfigInput, expectedVersion int64, meta identity.RequestMeta) (ProductVariantConfig, error) {
	if !authorized(session, "product.variant.manage") {
		return ProductVariantConfig{}, identity.ErrForbidden
	}
	if expectedVersion < 1 {
		return ProductVariantConfig{}, fmt.Errorf("%w: varyant ayarları için güncel ürün sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProductVariantConfig{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var current bool
	var kind string
	var productVersion int64
	if err = tx.QueryRow(ctx, `SELECT variants_enabled,kind::text,version FROM products WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, productID).Scan(&current, &kind, &productVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductVariantConfig{}, identity.ErrForbidden
		}
		return ProductVariantConfig{}, err
	}
	if expectedVersion != productVersion {
		return ProductVariantConfig{}, identity.ErrConflict
	}
	desired := current
	if input.VariantsEnabled != nil {
		desired = *input.VariantsEnabled
	}
	if desired && kind != "PHYSICAL" {
		return ProductVariantConfig{}, variantValidation("VARIANT_PRODUCT_MUST_BE_PHYSICAL", "Varyant modu yalnızca fiziksel ürünlerde kullanılabilir")
	}
	if desired && !current {
		var productBarcodeCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id IS NULL`, session.CurrentCompanyID, productID).Scan(&productBarcodeCount); err != nil {
			return ProductVariantConfig{}, err
		}
		if productBarcodeCount > 0 {
			return ProductVariantConfig{}, variantValidation("VARIANT_MODE_REQUIRES_EMPTY_PRODUCT_BARCODES", "Varyant moduna geçmeden önce ürün üzerindeki barkodları kaldırın; barkodları varyant kartlarına ekleyin")
		}
	}
	definitionsChanged := false
	if input.Definitions != nil {
		currentConfig, loadErr := loadVariantConfig(ctx, tx, session.CurrentCompanyID, productID)
		if loadErr != nil {
			return ProductVariantConfig{}, loadErr
		}
		definitionsChanged = variantConfigInputChanged(currentConfig, input.Definitions)
		if definitionsChanged {
			if err = validateAndReplaceProductVariantConfig(ctx, tx, session.CurrentCompanyID, productID, input.Definitions, desired); err != nil {
				return ProductVariantConfig{}, err
			}
		}
	} else if desired {
		var n int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM product_variant_definitions WHERE company_id=$1 AND product_id=$2`, session.CurrentCompanyID, productID).Scan(&n); err != nil {
			return ProductVariantConfig{}, err
		}
		if n == 0 {
			return ProductVariantConfig{}, variantValidation("VARIANT_DEFINITIONS_REQUIRED", "Varyant modu için en az bir varyant tanımı seçin")
		}
	}
	changed := definitionsChanged || desired != current
	if changed {
		if _, err = tx.Exec(ctx, `UPDATE products SET variants_enabled=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4`, desired, session.CurrentCompanyID, productID, productVersion); err != nil {
			return ProductVariantConfig{}, mapConstraint(err)
		}
	}
	if changed {
		if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANT_CONFIG_UPDATED", "product.variant_config.updated", productID, map[string]any{"variants_enabled": desired, "definitions_changed": definitionsChanged}, meta); err != nil {
			return ProductVariantConfig{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return ProductVariantConfig{}, err
	}
	return s.GetVariantConfig(ctx, session, productID)
}

func (s *Service) GenerateVariants(ctx context.Context, session identity.Session, productID string, meta identity.RequestMeta) ([]Variant, error) {
	if !authorized(session, "product.variant.manage") {
		return nil, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var code string
	var enabled bool
	var kind string
	if err = tx.QueryRow(ctx, `SELECT code,variants_enabled,kind::text FROM products WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, productID).Scan(&code, &enabled, &kind); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, identity.ErrForbidden
		}
		return nil, err
	}
	if kind != "PHYSICAL" || !enabled {
		return nil, variantValidation("VARIANT_MODE_REQUIRED", "Kombinasyon üretmek için varyant modu açık olmalıdır")
	}
	config, err := loadVariantConfig(ctx, tx, session.CurrentCompanyID, productID)
	if err != nil {
		return nil, err
	}
	selections, err := allVariantSelections(config)
	if err != nil {
		return nil, err
	}
	for _, values := range selections {
		sig := makeVariantSignature(values)
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_signature=$3)`, session.CurrentCompanyID, productID, sig).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		id := uuid.NewString()
		if _, err = tx.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature) VALUES($1,$2,$3,$4,$5)`, id, session.CurrentCompanyID, productID, generatedVariantCode(code, values), sig); err != nil {
			return nil, mapConstraint(err)
		}
		if err = insertVariantValues(ctx, tx, session.CurrentCompanyID, id, values); err != nil {
			return nil, mapConstraint(err)
		}
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANTS_GENERATED", "product.variants.generated", productID, map[string]any{"combination_count": len(selections)}, meta); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.ListVariants(ctx, session, productID)
}

func (s *Service) ListVariants(ctx context.Context, session identity.Session, productID string) ([]Variant, error) {
	if !authorized(session, "product.read") {
		return nil, identity.ErrForbidden
	}
	if err := s.ensureProduct(ctx, session.CurrentCompanyID, productID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id,company_id,product_id,variant_code,is_active,created_at,updated_at,version,EXISTS(SELECT 1 FROM stock_movements sm WHERE sm.company_id=pv.company_id AND sm.variant_id=pv.id) FROM product_variants pv WHERE company_id=$1 AND product_id=$2 ORDER BY lower(variant_code),id`, session.CurrentCompanyID, productID)
	if err != nil {
		return nil, err
	}
	items := []Variant{}
	for rows.Next() {
		item, e := scanVariant(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, item)
	}
	if e := rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	rows.Close()
	// Hydrate after the cursor is closed: on a request-pinned connection a nested
	// query while rows is still open fails with "conn busy".
	for i := range items {
		if e := s.loadVariantDetails(ctx, s.pool, &items[i]); e != nil {
			return nil, e
		}
	}
	return items, nil
}

func (s *Service) CreateVariant(ctx context.Context, session identity.Session, productID string, input VariantInput, meta identity.RequestMeta) (Variant, error) {
	if !authorized(session, "product.variant.manage") {
		return Variant{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Variant{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	productCode, err := lockVariantProduct(ctx, tx, session.CurrentCompanyID, productID)
	if err != nil {
		return Variant{}, err
	}
	values, sig, err := resolveVariantValues(ctx, tx, session.CurrentCompanyID, productID, input.Values)
	if err != nil {
		return Variant{}, err
	}
	code, err := createVariantCode(input.VariantCode, productCode, values)
	if err != nil {
		return Variant{}, err
	}
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature,is_active) VALUES($1,$2,$3,$4,$5,$6)`, id, session.CurrentCompanyID, productID, code, sig, boolDefault(input.IsActive, true)); err != nil {
		return Variant{}, mapConstraint(err)
	}
	if err = insertVariantValues(ctx, tx, session.CurrentCompanyID, id, values); err != nil {
		return Variant{}, mapConstraint(err)
	}
	if input.Barcodes != nil {
		if err = replaceVariantBarcodes(ctx, tx, session.CurrentCompanyID, productID, id, input.Barcodes); err != nil {
			return Variant{}, mapConstraint(err)
		}
	}
	if err = updateVariantPriceOverrides(ctx, tx, session.CurrentCompanyID, id, input.PurchasePriceOverride, input.SalesPriceOverride); err != nil {
		return Variant{}, err
	}
	if input.PriceEntries != nil {
		if err = syncVariantPriceEntries(ctx, tx, session.CurrentCompanyID, productID, id, input.PriceEntries); err != nil {
			return Variant{}, err
		}
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANT_CREATED", "product.variant.created", id, map[string]any{"product_id": productID, "variant_code": code}, meta); err != nil {
		return Variant{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Variant{}, err
	}
	return s.getVariant(ctx, session.CurrentCompanyID, id)
}

func (s *Service) UpdateVariant(ctx context.Context, session identity.Session, productID, variantID string, version int64, input VariantInput, meta identity.RequestMeta) (Variant, error) {
	if !authorized(session, "product.variant.manage") {
		return Variant{}, identity.ErrForbidden
	}
	if version < 1 {
		return Variant{}, fmt.Errorf("%w: varyant sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Variant{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = lockVariantProduct(ctx, tx, session.CurrentCompanyID, productID); err != nil {
		return Variant{}, err
	}
	var oldCode, oldSig string
	var oldActive bool
	if err = tx.QueryRow(ctx, `SELECT variant_code,variant_signature,is_active FROM product_variants WHERE company_id=$1 AND product_id=$2 AND id=$3 FOR UPDATE`, session.CurrentCompanyID, productID, variantID).Scan(&oldCode, &oldSig, &oldActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Variant{}, identity.ErrForbidden
		}
		return Variant{}, err
	}
	moved, err := variantHasMovement(ctx, tx, session.CurrentCompanyID, variantID)
	if err != nil {
		return Variant{}, err
	}
	var values []VariantValue
	var sig string
	if input.Values == nil {
		current := Variant{ID: variantID, CompanyID: session.CurrentCompanyID, ProductID: productID}
		if err = s.loadVariantDetails(ctx, tx, &current); err != nil {
			return Variant{}, err
		}
		values, sig = current.Values, oldSig
	} else {
		values, sig, err = resolveVariantValues(ctx, tx, session.CurrentCompanyID, productID, input.Values)
		if err != nil {
			return Variant{}, err
		}
	}
	code, err := variantUpdateCode(input.VariantCode, oldCode)
	if err != nil {
		return Variant{}, err
	}
	if moved && (code != oldCode || sig != oldSig) {
		return Variant{}, variantValidation("VARIANT_IDENTITY_LOCKED", "Stok hareketi olan varyantın SKU ve özellikleri değiştirilemez")
	}
	active := oldActive
	if input.IsActive != nil {
		active = *input.IsActive
	}
	tag, err := tx.Exec(ctx, `UPDATE product_variants SET variant_code=$1,variant_signature=$2,is_active=$3,updated_at=now(),version=version+1 WHERE company_id=$4 AND product_id=$5 AND id=$6 AND version=$7`, code, sig, active, session.CurrentCompanyID, productID, variantID, version)
	if err != nil {
		return Variant{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Variant{}, identity.ErrConflict
	}
	if !moved {
		if _, err = tx.Exec(ctx, `DELETE FROM product_variant_values WHERE company_id=$1 AND variant_id=$2`, session.CurrentCompanyID, variantID); err != nil {
			return Variant{}, err
		}
		if err = insertVariantValues(ctx, tx, session.CurrentCompanyID, variantID, values); err != nil {
			return Variant{}, mapConstraint(err)
		}
	}
	if input.Barcodes != nil {
		if err = replaceVariantBarcodes(ctx, tx, session.CurrentCompanyID, productID, variantID, input.Barcodes); err != nil {
			return Variant{}, mapConstraint(err)
		}
	}
	if err = updateVariantPriceOverrides(ctx, tx, session.CurrentCompanyID, variantID, input.PurchasePriceOverride, input.SalesPriceOverride); err != nil {
		return Variant{}, err
	}
	if input.PriceEntries != nil {
		if err = syncVariantPriceEntries(ctx, tx, session.CurrentCompanyID, productID, variantID, input.PriceEntries); err != nil {
			return Variant{}, err
		}
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANT_UPDATED", "product.variant.updated", variantID, map[string]any{"product_id": productID}, meta); err != nil {
		return Variant{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Variant{}, err
	}
	return s.getVariant(ctx, session.CurrentCompanyID, variantID)
}

func (s *Service) DeactivateVariant(ctx context.Context, session identity.Session, productID, variantID string, version int64, meta identity.RequestMeta) error {
	if !authorized(session, "product.variant.manage") {
		return identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE product_variants SET is_active=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND product_id=$2 AND id=$3 AND version=$4 AND is_active`, session.CurrentCompanyID, productID, variantID, version)
	if err != nil {
		return mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANT_DEACTIVATED", "product.variant.deactivated", variantID, map[string]any{"product_id": productID}, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) SetVariantDefinitionActive(ctx context.Context, session identity.Session, id string, version int64, active bool, meta identity.RequestMeta) (VariantDefinition, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantDefinition{}, identity.ErrForbidden
	}
	if version < 1 {
		return VariantDefinition{}, fmt.Errorf("%w: varyant tanımı sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantDefinition{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var current bool
	if err = tx.QueryRow(ctx, `SELECT is_active FROM variant_definitions WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return VariantDefinition{}, identity.ErrForbidden
		}
		return VariantDefinition{}, err
	}
	tag, err := tx.Exec(ctx, `UPDATE variant_definitions SET is_active=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4`, active, session.CurrentCompanyID, id, version)
	if err != nil {
		return VariantDefinition{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return VariantDefinition{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "VARIANT_DEFINITION_ACTIVE_CHANGED", "product.variant_definition.active_changed", id, map[string]any{"active": active}, meta); err != nil {
		return VariantDefinition{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantDefinition{}, err
	}
	return s.getVariantDefinition(ctx, session, id)
}

func (s *Service) SetVariantOptionActive(ctx context.Context, session identity.Session, definitionID, optionID string, version int64, active bool, meta identity.RequestMeta) (VariantOption, error) {
	if !authorized(session, "product.variant_definition.manage") {
		return VariantOption{}, identity.ErrForbidden
	}
	if version < 1 {
		return VariantOption{}, fmt.Errorf("%w: varyant seçeneği sürümü gereklidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VariantOption{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE variant_definition_options SET is_active=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND definition_id=$3 AND id=$4 AND version=$5`, active, session.CurrentCompanyID, definitionID, optionID, version)
	if err != nil {
		return VariantOption{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return VariantOption{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "VARIANT_OPTION_ACTIVE_CHANGED", "product.variant_option.active_changed", optionID, map[string]any{"active": active}, meta); err != nil {
		return VariantOption{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VariantOption{}, err
	}
	return s.getVariantOption(ctx, session.CurrentCompanyID, optionID)
}

func (s *Service) ensureProduct(ctx context.Context, companyID, productID string) error {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND id=$2)`, companyID, productID).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return identity.ErrForbidden
	}
	return nil
}
func (s *Service) getVariantDefinition(ctx context.Context, session identity.Session, id string) (VariantDefinition, error) {
	items, err := s.ListVariantDefinitions(ctx, session)
	if err != nil {
		return VariantDefinition{}, err
	}
	for _, i := range items {
		if i.ID == id {
			return i, nil
		}
	}
	return VariantDefinition{}, identity.ErrForbidden
}
func (s *Service) getVariantOption(ctx context.Context, companyID, id string) (VariantOption, error) {
	var o VariantOption
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,definition_id,code,name,short_code,sort_order,is_active,created_at,updated_at,version FROM variant_definition_options WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&o.ID, &o.CompanyID, &o.DefinitionID, &o.Code, &o.Name, &o.ShortCode, &o.SortOrder, &o.IsActive, &o.CreatedAt, &o.UpdatedAt, &o.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return VariantOption{}, identity.ErrForbidden
	}
	return o, err
}
func (s *Service) getVariant(ctx context.Context, companyID, id string) (Variant, error) {
	var v Variant
	err := s.pool.QueryRow(ctx, `SELECT id,company_id,product_id,variant_code,is_active,created_at,updated_at,version,EXISTS(SELECT 1 FROM stock_movements sm WHERE sm.company_id=pv.company_id AND sm.variant_id=pv.id) FROM product_variants pv WHERE company_id=$1 AND id=$2`, companyID, id).Scan(&v.ID, &v.CompanyID, &v.ProductID, &v.VariantCode, &v.IsActive, &v.CreatedAt, &v.UpdatedAt, &v.Version, &v.Locked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Variant{}, identity.ErrForbidden
	}
	if err != nil {
		return Variant{}, err
	}
	if err = s.loadVariantDetails(ctx, s.pool, &v); err != nil {
		return Variant{}, err
	}
	return v, nil
}
func (s *Service) loadVariantDetails(ctx context.Context, q rowQuerier, v *Variant) error {
	if err := q.QueryRow(ctx, `SELECT COALESCE((SELECT unit_price::text FROM product_variant_price_overrides WHERE company_id=$1 AND variant_id=$2 AND direction='PURCHASE'),''),COALESCE((SELECT unit_price::text FROM product_variant_price_overrides WHERE company_id=$1 AND variant_id=$2 AND direction='SALES'),''),p.purchase_price::text,p.sales_price::text FROM products p WHERE p.company_id=$1 AND p.id=$3`, v.CompanyID, v.ID, v.ProductID).Scan(&v.PurchasePriceOverride, &v.SalesPriceOverride, &v.PurchasePrice, &v.SalesPrice); err != nil {
		return err
	}
	v.PurchasePriceInherited = v.PurchasePriceOverride == ""
	v.SalesPriceInherited = v.SalesPriceOverride == ""
	// Trailing fraction zeros are storage detail; present overrides the same
	// trimmed way price-list entries are (see item.UnitPrice above).
	if v.PurchasePriceOverride != "" {
		v.PurchasePriceOverride = trimDecimalFractionZeros(v.PurchasePriceOverride)
	}
	if v.SalesPriceOverride != "" {
		v.SalesPriceOverride = trimDecimalFractionZeros(v.SalesPriceOverride)
	}
	if err := loadVariantStock(ctx, q, v); err != nil {
		return err
	}
	v.PriceEntries = []VariantPriceEntry{}
	priceRows, err := q.Query(ctx, `SELECT DISTINCT ON (e.price_list_id) e.price_list_id,e.id,e.unit_price::text,e.valid_from::text,e.valid_to::text,e.version
		FROM price_list_entries e
		JOIN price_lists p ON p.company_id=e.company_id AND p.id=e.price_list_id AND p.is_active
		WHERE e.company_id=$1 AND e.item_id=$2 AND e.variant_id=$3
		  AND e.valid_from <= CURRENT_DATE AND (e.valid_to IS NULL OR e.valid_to >= CURRENT_DATE)
		ORDER BY e.price_list_id,e.valid_from DESC,e.id`, v.CompanyID, v.ProductID, v.ID)
	if err != nil {
		return err
	}
	for priceRows.Next() {
		var item VariantPriceEntry
		if err = priceRows.Scan(&item.PriceListID, &item.EntryID, &item.UnitPrice, &item.ValidFrom, &item.ValidTo, &item.Version); err != nil {
			priceRows.Close()
			return err
		}
		item.UnitPrice = normalizeDecimal(item.UnitPrice)
		v.PriceEntries = append(v.PriceEntries, item)
	}
	if err = priceRows.Err(); err != nil {
		priceRows.Close()
		return err
	}
	priceRows.Close()
	v.Values = []VariantValue{}
	rows, err := q.Query(ctx, `SELECT v.definition_id,d.code,d.name,v.option_id,o.code,o.name,o.short_code FROM product_variant_values v JOIN variant_definitions d ON d.company_id=v.company_id AND d.id=v.definition_id JOIN variant_definition_options o ON o.company_id=v.company_id AND o.definition_id=v.definition_id AND o.id=v.option_id WHERE v.company_id=$1 AND v.variant_id=$2 ORDER BY d.code,o.sort_order,o.id`, v.CompanyID, v.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var x VariantValue
		if err = rows.Scan(&x.DefinitionID, &x.DefinitionCode, &x.DefinitionName, &x.OptionID, &x.OptionCode, &x.OptionName, &x.OptionShortCode); err != nil {
			rows.Close()
			return err
		}
		v.Values = append(v.Values, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	v.Barcodes = []ProductBarcode{}
	rows, err = q.Query(ctx, `SELECT id,variant_id,barcode,barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id=$3 ORDER BY is_primary DESC,barcode`, v.CompanyID, v.ProductID, v.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b ProductBarcode
		if err = rows.Scan(&b.ID, &b.VariantID, &b.Barcode, &b.BarcodeType, &b.IsPrimary); err != nil {
			rows.Close()
			return err
		}
		v.Barcodes = append(v.Barcodes, b)
	}
	err = rows.Err()
	rows.Close()
	return err
}

func loadVariantStock(ctx context.Context, q rowQuerier, v *Variant) error {
	if err := q.QueryRow(ctx, `SELECT COALESCE(SUM(sp.physical_quantity),0)::text,COALESCE(SUM(sp.reserved_quantity),0)::text,COALESCE(SUM(sp.available_quantity),0)::text,COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=sp.company_id AND pu.product_id=sp.product_id AND pu.is_base LIMIT 1),'') FROM stock_positions sp JOIN warehouses w ON w.company_id=sp.company_id AND w.id=sp.warehouse_id WHERE sp.company_id=$1 AND sp.product_id=$2 AND sp.variant_id=$3 AND w.is_active AND w.warehouse_type='STANDARD' GROUP BY sp.company_id,sp.product_id`, v.CompanyID, v.ProductID, v.ID).Scan(&v.PhysicalQuantity, &v.ReservedQuantity, &v.AvailableQuantity, &v.StockUnit); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			v.PhysicalQuantity, v.ReservedQuantity, v.AvailableQuantity = "0", "0", "0"
			v.StockPositions = []VariantStockPosition{}
		} else {
			return err
		}
	}
	v.StockPositions = []VariantStockPosition{}
	rows, err := q.Query(ctx, `SELECT sp.warehouse_id,w.code,w.name,sp.location_id,COALESCE(l.code,''),COALESCE(l.name,''),sp.physical_quantity::text,sp.reserved_quantity::text,sp.available_quantity::text,COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=sp.company_id AND pu.product_id=sp.product_id AND pu.is_base LIMIT 1),'') FROM stock_positions sp JOIN warehouses w ON w.company_id=sp.company_id AND w.id=sp.warehouse_id LEFT JOIN locations l ON l.company_id=sp.company_id AND l.id=sp.location_id WHERE sp.company_id=$1 AND sp.product_id=$2 AND sp.variant_id=$3 AND w.is_active AND w.warehouse_type='STANDARD' ORDER BY w.code,l.code,sp.id`, v.CompanyID, v.ProductID, v.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item VariantStockPosition
		var locationID *string
		if err := rows.Scan(&item.WarehouseID, &item.WarehouseCode, &item.WarehouseName, &locationID, &item.LocationCode, &item.LocationName, &item.Physical, &item.Reserved, &item.Available, &item.StockUnit); err != nil {
			return err
		}
		if locationID != nil {
			item.LocationID = *locationID
		}
		v.StockPositions = append(v.StockPositions, item)
	}
	return rows.Err()
}

func updateVariantPriceOverrides(ctx context.Context, tx pgx.Tx, companyID, variantID string, purchase, sales *string) error {
	for direction, value := range map[string]*string{"PURCHASE": purchase, "SALES": sales} {
		if value == nil {
			continue
		}
		trimmed := strings.TrimSpace(*value)
		if trimmed == "" {
			if _, err := tx.Exec(ctx, `DELETE FROM product_variant_price_overrides WHERE company_id=$1 AND variant_id=$2 AND direction=$3`, companyID, variantID, direction); err != nil {
				return err
			}
			continue
		}
		normalized := normalizePrice(trimmed)
		if normalized == "" {
			return fmt.Errorf("%w: varyant fiyatı geçersiz", identity.ErrValidation)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_price_overrides(company_id,variant_id,direction,unit_price) VALUES($1,$2,$3,$4) ON CONFLICT(company_id,variant_id,direction) DO UPDATE SET unit_price=EXCLUDED.unit_price,updated_at=now()`, companyID, variantID, direction, normalized); err != nil {
			return mapConstraint(err)
		}
	}
	return nil
}

func syncVariantPriceEntries(ctx context.Context, tx pgx.Tx, companyID, productID, variantID string, inputs []VariantPriceEntryInput) error {
	for _, input := range inputs {
		listID := strings.TrimSpace(input.PriceListID)
		if uuid.Validate(listID) != nil {
			return fmt.Errorf("%w: varyant fiyat listesi kimliği geçersiz", identity.ErrValidation)
		}
		if input.EntryID != "" && uuid.Validate(strings.TrimSpace(input.EntryID)) != nil {
			return fmt.Errorf("%w: varyant fiyat satırı kimliği geçersiz", identity.ErrValidation)
		}
		var listActive bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM price_lists WHERE company_id=$1 AND id=$2`, companyID, listID).Scan(&listActive); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("%w: fiyat listesi bulunamadı", identity.ErrValidation)
			}
			return err
		}
		if !listActive {
			return fmt.Errorf("%w: pasif fiyat listesine varyant fiyatı yazılamaz", identity.ErrValidation)
		}

		if strings.TrimSpace(input.UnitPrice) == "" {
			if input.EntryID == "" {
				continue
			}
			if input.Version < 1 {
				return fmt.Errorf("%w: silinecek varyant fiyat satırı sürümü gereklidir", identity.ErrValidation)
			}
			tag, err := tx.Exec(ctx, `DELETE FROM price_list_entries WHERE company_id=$1 AND id=$2 AND price_list_id=$3 AND item_id=$4 AND variant_id=$5 AND version=$6`, companyID, input.EntryID, listID, productID, variantID, input.Version)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return identity.ErrConflict
			}
			continue
		}
		unitPrice := normalizePrice(input.UnitPrice)
		if unitPrice == "" {
			return fmt.Errorf("%w: varyant fiyatı geçersiz", identity.ErrValidation)
		}

		validFrom := strings.TrimSpace(input.ValidFrom)
		if validFrom == "" {
			validFrom = time.Now().UTC().Format("2006-01-02")
		}
		from, err := time.Parse("2006-01-02", validFrom)
		if err != nil {
			return fmt.Errorf("%w: varyant fiyat başlangıç tarihi geçersiz", identity.ErrValidation)
		}
		var validTo *string
		if input.ValidTo != nil && strings.TrimSpace(*input.ValidTo) != "" {
			to := strings.TrimSpace(*input.ValidTo)
			parsedTo, parseErr := time.Parse("2006-01-02", to)
			if parseErr != nil || parsedTo.Before(from) {
				return fmt.Errorf("%w: varyant fiyat tarih aralığı geçersiz", identity.ErrValidation)
			}
			validTo = &to
		}
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, companyID+":"+listID+":"+productID+":"+variantID); err != nil {
			return err
		}
		var overlap bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM price_list_entries
			 WHERE company_id=$1 AND price_list_id=$2 AND item_id=$3 AND variant_id=$4
			   AND (NULLIF($5,'') IS NULL OR id<>NULLIF($5,'')::uuid)
			   AND valid_from <= COALESCE($7::date,'9999-12-31')
			   AND COALESCE(valid_to,'9999-12-31') >= $6::date)`, companyID, listID, productID, variantID, input.EntryID, validTo, validFrom).Scan(&overlap); err != nil {
			return err
		}
		if overlap {
			return fmt.Errorf("%w: varyant fiyat dönemi başka bir satırla çakışıyor", identity.ErrValidation)
		}
		if input.EntryID == "" {
			_, err = tx.Exec(ctx, `INSERT INTO price_list_entries(id,company_id,price_list_id,item_id,variant_id,valid_from,valid_to,unit_price) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, uuid.NewString(), companyID, listID, productID, variantID, validFrom, validTo, unitPrice)
		} else {
			if input.Version < 1 {
				return fmt.Errorf("%w: güncellenecek varyant fiyat satırı sürümü gereklidir", identity.ErrValidation)
			}
			var tag pgconn.CommandTag
			tag, err = tx.Exec(ctx, `UPDATE price_list_entries SET valid_from=$1,valid_to=$2,unit_price=$3,updated_at=now(),version=version+1 WHERE company_id=$4 AND id=$5 AND price_list_id=$6 AND item_id=$7 AND variant_id=$8 AND version=$9`, validFrom, validTo, unitPrice, companyID, input.EntryID, listID, productID, variantID, input.Version)
			if err == nil && tag.RowsAffected() == 0 {
				err = identity.ErrConflict
			}
		}
		if err != nil {
			return mapConstraint(err)
		}
	}
	return nil
}
func scanVariant(row interface{ Scan(...any) error }) (Variant, error) {
	var v Variant
	if err := row.Scan(&v.ID, &v.CompanyID, &v.ProductID, &v.VariantCode, &v.IsActive, &v.CreatedAt, &v.UpdatedAt, &v.Version, &v.Locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Variant{}, identity.ErrForbidden
		}
		return Variant{}, err
	}
	v.Values = []VariantValue{}
	v.Barcodes = []ProductBarcode{}
	return v, nil
}

func loadVariantConfig(ctx context.Context, q rowQuerier, companyID, productID string) (ProductVariantConfig, error) {
	var c ProductVariantConfig
	c.ProductID = productID
	c.Definitions = []ProductVariantDefinition{}
	if err := q.QueryRow(ctx, `SELECT variants_enabled,version,EXISTS(SELECT 1 FROM stock_movements sm WHERE sm.company_id=products.company_id AND sm.product_id=products.id),EXISTS(SELECT 1 FROM product_variants pv WHERE pv.company_id=products.company_id AND pv.product_id=products.id) FROM products WHERE company_id=$1 AND id=$2`, companyID, productID).Scan(&c.VariantsEnabled, &c.Version, &c.MovementStarted, &c.IdentityLocked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProductVariantConfig{}, identity.ErrForbidden
		}
		return ProductVariantConfig{}, err
	}
	rows, err := q.Query(ctx, `SELECT d.id,d.code,d.name,d.is_active,pd.position,o.id,o.company_id,o.definition_id,o.code,o.name,o.short_code,o.sort_order,o.is_active,o.created_at,o.updated_at,o.version FROM product_variant_definitions pd JOIN variant_definitions d ON d.company_id=pd.company_id AND d.id=pd.definition_id JOIN product_variant_allowed_options a ON a.company_id=pd.company_id AND a.product_id=pd.product_id AND a.definition_id=pd.definition_id JOIN variant_definition_options o ON o.company_id=a.company_id AND o.definition_id=a.definition_id AND o.id=a.option_id WHERE pd.company_id=$1 AND pd.product_id=$2 ORDER BY pd.position,o.sort_order,o.id`, companyID, productID)
	if err != nil {
		return ProductVariantConfig{}, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var did, code, name string
		var definitionActive bool
		var pos int
		var o VariantOption
		if err = rows.Scan(&did, &code, &name, &definitionActive, &pos, &o.ID, &o.CompanyID, &o.DefinitionID, &o.Code, &o.Name, &o.ShortCode, &o.SortOrder, &o.IsActive, &o.CreatedAt, &o.UpdatedAt, &o.Version); err != nil {
			return ProductVariantConfig{}, err
		}
		i, ok := idx[did]
		if !ok {
			i = len(c.Definitions)
			idx[did] = i
			c.Definitions = append(c.Definitions, ProductVariantDefinition{DefinitionID: did, Code: code, Name: name, IsActive: definitionActive, Position: pos, Options: []VariantOption{}})
		}
		c.Definitions[i].Options = append(c.Definitions[i].Options, o)
	}
	if err = rows.Err(); err != nil {
		return ProductVariantConfig{}, err
	}
	c.CombinationCount = combinationCount(c.Definitions)
	c.IdentityLocked = c.IdentityLocked || c.MovementStarted
	return c, nil
}

func validateAndReplaceProductVariantConfig(ctx context.Context, tx pgx.Tx, companyID, productID string, inputs []ProductVariantDefinitionInput, enabled bool) error {
	if len(inputs) > maxVariantDefinitions {
		return variantValidation("VARIANT_DEFINITION_LIMIT", "Bir ürün en fazla 5 varyant boyutu kullanabilir")
	}
	if enabled && len(inputs) == 0 {
		return variantValidation("VARIANT_DEFINITIONS_REQUIRED", "Varyant modu için en az bir varyant tanımı seçin")
	}
	defs := []variantConfigDefinition{}
	seen := map[string]bool{}
	positions := map[int]bool{}
	for i, in := range inputs {
		did := strings.TrimSpace(in.DefinitionID)
		pos := in.Position
		if pos == 0 {
			pos = i + 1
		}
		if uuid.Validate(did) != nil || seen[did] || pos < 1 || pos > 5 || positions[pos] {
			return variantValidation("VARIANT_DEFINITION_INVALID", "Varyant tanımı seçimi geçersiz veya tekrarlı")
		}
		seen[did] = true
		positions[pos] = true
		var code, name string
		var active bool
		if err := tx.QueryRow(ctx, `SELECT code,name,is_active FROM variant_definitions WHERE company_id=$1 AND id=$2`, companyID, did).Scan(&code, &name, &active); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return identity.ErrForbidden
			}
			return err
		}
		if !active {
			return variantValidation("VARIANT_DEFINITION_INACTIVE", "Pasif varyant tanımı kullanılamaz")
		}
		if len(in.OptionIDs) == 0 {
			return variantValidation("VARIANT_OPTIONS_REQUIRED", "Her varyant boyutu için en az bir seçenek seçin")
		}
		opts := []VariantOption{}
		used := map[string]bool{}
		for _, raw := range in.OptionIDs {
			oid := strings.TrimSpace(raw)
			if uuid.Validate(oid) != nil || used[oid] {
				return variantValidation("VARIANT_OPTION_INVALID", "Varyant seçeneği seçimi geçersiz veya tekrarlı")
			}
			used[oid] = true
			var o VariantOption
			if err := tx.QueryRow(ctx, `SELECT id,company_id,definition_id,code,name,short_code,sort_order,is_active,created_at,updated_at,version FROM variant_definition_options WHERE company_id=$1 AND definition_id=$2 AND id=$3`, companyID, did, oid).Scan(&o.ID, &o.CompanyID, &o.DefinitionID, &o.Code, &o.Name, &o.ShortCode, &o.SortOrder, &o.IsActive, &o.CreatedAt, &o.UpdatedAt, &o.Version); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return identity.ErrForbidden
				}
				return err
			}
			if !o.IsActive {
				return variantValidation("VARIANT_OPTION_INACTIVE", "Pasif varyant seçeneği kullanılamaz")
			}
			opts = append(opts, o)
		}
		defs = append(defs, variantConfigDefinition{DefinitionID: did, Code: code, Name: name, Position: pos, Options: opts})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Position < defs[j].Position })
	if combinationCountInternal(defs) > maxVariantCombinations {
		return variantValidation("VARIANT_COMBINATION_LIMIT", "Varyant kombinasyonu 1000 adedi aşamaz")
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM product_variants WHERE company_id=$1 AND product_id=$2`, companyID, productID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return variantValidation("VARIANT_CONFIG_LOCKED", "Varyant oluştuktan sonra boyut seçimi değiştirilemez")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_variant_allowed_options WHERE company_id=$1 AND product_id=$2`, companyID, productID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_variant_definitions WHERE company_id=$1 AND product_id=$2`, companyID, productID); err != nil {
		return err
	}
	for _, d := range defs {
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_definitions(company_id,product_id,definition_id,position) VALUES($1,$2,$3,$4)`, companyID, productID, d.DefinitionID, d.Position); err != nil {
			return mapConstraint(err)
		}
		for _, o := range d.Options {
			if _, err := tx.Exec(ctx, `INSERT INTO product_variant_allowed_options(company_id,product_id,definition_id,option_id) VALUES($1,$2,$3,$4)`, companyID, productID, d.DefinitionID, o.ID); err != nil {
				return mapConstraint(err)
			}
		}
	}
	return nil
}

func resolveVariantValues(ctx context.Context, tx pgx.Tx, companyID, productID string, inputs []VariantValueInput) ([]VariantValue, string, error) {
	c, err := loadVariantConfig(ctx, tx, companyID, productID)
	if err != nil {
		return nil, "", err
	}
	if !c.VariantsEnabled {
		return nil, "", variantValidation("VARIANT_MODE_REQUIRED", "Ürün varyant modu açık değil")
	}
	if len(inputs) != len(c.Definitions) {
		return nil, "", variantValidation("VARIANT_VALUES_REQUIRED", "Her varyant boyutu için bir seçenek seçilmelidir")
	}
	allowed := map[string]map[string]VariantOption{}
	for _, d := range c.Definitions {
		allowed[d.DefinitionID] = map[string]VariantOption{}
		for _, o := range d.Options {
			allowed[d.DefinitionID][o.ID] = o
		}
	}
	seen := map[string]bool{}
	values := []VariantValue{}
	for _, in := range inputs {
		if seen[in.DefinitionID] {
			return nil, "", variantValidation("VARIANT_VALUES_INVALID", "Varyant boyutları tekrarlı")
		}
		o, ok := allowed[in.DefinitionID][in.OptionID]
		if !ok || !o.IsActive {
			return nil, "", variantValidation("VARIANT_OPTION_INVALID", "Seçilen varyant seçeneği ürün konfigürasyonunda yok")
		}
		seen[in.DefinitionID] = true
		values = append(values, VariantValue{DefinitionID: in.DefinitionID, OptionID: in.OptionID, OptionCode: o.Code, OptionName: o.Name, OptionShortCode: o.ShortCode})
	}
	if len(seen) != len(c.Definitions) {
		return nil, "", variantValidation("VARIANT_VALUES_REQUIRED", "Her varyant boyutu için bir seçenek seçilmelidir")
	}
	sort.Slice(values, func(i, j int) bool { return values[i].DefinitionID < values[j].DefinitionID })
	return values, makeVariantSignature(values), nil
}
func insertVariantValues(ctx context.Context, tx pgx.Tx, companyID, variantID string, values []VariantValue) error {
	for _, v := range values {
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_values(company_id,variant_id,definition_id,option_id) VALUES($1,$2,$3,$4)`, companyID, variantID, v.DefinitionID, v.OptionID); err != nil {
			return err
		}
	}
	return nil
}
func replaceVariantBarcodes(ctx context.Context, tx pgx.Tx, companyID, productID, variantID string, inputs []BarcodeInput) error {
	normalized, err := normalizeBarcodes(inputs)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id=$3`, companyID, productID, variantID); err != nil {
		return err
	}
	for _, b := range normalized {
		if _, err = tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), companyID, productID, variantID, b.Barcode, b.BarcodeType, b.IsPrimary); err != nil {
			return err
		}
	}
	return nil
}
func insertDefinitionOptions(ctx context.Context, tx pgx.Tx, companyID, definitionID string, inputs []VariantOptionInput) error {
	codes := map[string]bool{}
	shorts := map[string]bool{}
	for i, in := range inputs {
		o, err := normalizeOptionInput(in)
		if err != nil {
			return err
		}
		if o.SortOrder == 0 {
			o.SortOrder = i + 1
		}
		if codes[o.Code] || shorts[o.ShortCode] {
			return variantValidation("VARIANT_OPTION_DUPLICATE", "Varyant seçenek kodları ve kısa kodları tekil olmalıdır")
		}
		codes[o.Code] = true
		shorts[o.ShortCode] = true
		if _, err = tx.Exec(ctx, `INSERT INTO variant_definition_options(id,company_id,definition_id,code,name,short_code,sort_order) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), companyID, definitionID, o.Code, o.Name, o.ShortCode, o.SortOrder); err != nil {
			return err
		}
	}
	return nil
}
func lockVariantProduct(ctx context.Context, tx pgx.Tx, companyID, productID string) (string, error) {
	var code, kind string
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT code,kind::text,variants_enabled FROM products WHERE company_id=$1 AND id=$2 FOR UPDATE`, companyID, productID).Scan(&code, &kind, &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", identity.ErrForbidden
		}
		return "", err
	}
	if kind != "PHYSICAL" || !enabled {
		return "", variantValidation("VARIANT_MODE_REQUIRED", "Ürün varyant modu açık değil")
	}
	return code, nil
}
func variantHasMovement(ctx context.Context, tx pgx.Tx, companyID, variantID string) (bool, error) {
	var b bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM stock_movements WHERE company_id=$1 AND variant_id=$2)`, companyID, variantID).Scan(&b)
	return b, err
}
func normalizeOptionInput(i VariantOptionInput) (VariantOptionInput, error) {
	i.Code = normalizeVariantCode(i.Code)
	i.ShortCode = normalizeVariantCode(i.ShortCode)
	i.Name = strings.TrimSpace(i.Name)
	if i.Code == "" || i.ShortCode == "" || i.Name == "" {
		return VariantOptionInput{}, fmt.Errorf("%w: varyant seçeneği kodu, adı ve kısa kodu gereklidir", identity.ErrValidation)
	}
	if i.SortOrder < 0 {
		return VariantOptionInput{}, fmt.Errorf("%w: varyant seçenek sırası geçersiz", identity.ErrValidation)
	}
	return i, nil
}
func generatedVariantCode(product string, values []VariantValue) string {
	parts := []string{normalizeVariantCode(product)}
	for _, v := range values {
		parts = append(parts, normalizeVariantCode(v.OptionShortCode))
	}
	return normalizeVariantCode(strings.Join(parts, "-"))
}

func createVariantCode(raw, product string, values []VariantValue) (string, error) {
	trimmed := strings.TrimSpace(raw)
	code := normalizeVariantCode(trimmed)
	if trimmed != "" && code == "" {
		return "", variantValidation("VARIANT_CODE_INVALID", "Varyant SKU yalnızca harf, rakam, tire veya alt çizgi içerebilir")
	}
	if code == "" {
		code = generatedVariantCode(product, values)
	}
	return code, nil
}

func variantUpdateCode(raw, current string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return current, nil
	}
	code := normalizeVariantCode(raw)
	if code == "" {
		return "", variantValidation("VARIANT_CODE_INVALID", "Varyant SKU yalnızca harf, rakam, tire veya alt çizgi içerebilir")
	}
	return code, nil
}
func makeVariantSignature(values []VariantValue) string {
	p := make([]string, 0, len(values))
	for _, v := range values {
		p = append(p, v.DefinitionID+"="+v.OptionID)
	}
	sort.Strings(p)
	return strings.Join(p, "|")
}
func allVariantSelections(c ProductVariantConfig) ([][]VariantValue, error) {
	if len(c.Definitions) == 0 {
		return nil, variantValidation("VARIANT_DEFINITIONS_REQUIRED", "Kombinasyon üretmek için varyant tanımı seçilmelidir")
	}
	defs := make([]variantConfigDefinition, 0, len(c.Definitions))
	for _, d := range c.Definitions {
		if !d.IsActive {
			return nil, variantValidation("VARIANT_DEFINITION_INACTIVE", "Pasif varyant tanımından kombinasyon üretilemez")
		}
		if len(d.Options) == 0 {
			return nil, variantValidation("VARIANT_OPTIONS_REQUIRED", "Her varyant boyutunda seçenek olmalıdır")
		}
		for _, o := range d.Options {
			if !o.IsActive {
				return nil, variantValidation("VARIANT_OPTION_INACTIVE", "Pasif varyant seçeneğinden kombinasyon üretilemez")
			}
		}
		defs = append(defs, variantConfigDefinition{DefinitionID: d.DefinitionID, Code: d.Code, Name: d.Name, Position: d.Position, Options: d.Options})
	}
	if combinationCountInternal(defs) > maxVariantCombinations {
		return nil, variantValidation("VARIANT_COMBINATION_LIMIT", "Varyant kombinasyonu 1000 adedi aşamaz")
	}
	out := [][]VariantValue{}
	var visit func(int, []VariantValue)
	visit = func(i int, current []VariantValue) {
		if i == len(defs) {
			out = append(out, append([]VariantValue(nil), current...))
			return
		}
		for _, o := range defs[i].Options {
			visit(i+1, append(current, VariantValue{DefinitionID: defs[i].DefinitionID, DefinitionCode: defs[i].Code, DefinitionName: defs[i].Name, OptionID: o.ID, OptionCode: o.Code, OptionName: o.Name, OptionShortCode: o.ShortCode}))
		}
	}
	visit(0, nil)
	return out, nil
}

func variantConfigInputChanged(current ProductVariantConfig, inputs []ProductVariantDefinitionInput) bool {
	if len(current.Definitions) != len(inputs) {
		return true
	}
	currentByDefinition := make(map[string]ProductVariantDefinition, len(current.Definitions))
	for _, definition := range current.Definitions {
		currentByDefinition[definition.DefinitionID] = definition
	}
	seenDefinitions := make(map[string]struct{}, len(inputs))
	for i, input := range inputs {
		definitionID := strings.TrimSpace(input.DefinitionID)
		if _, duplicate := seenDefinitions[definitionID]; duplicate {
			return true
		}
		seenDefinitions[definitionID] = struct{}{}
		position := input.Position
		if position == 0 {
			position = i + 1
		}
		definition, ok := currentByDefinition[definitionID]
		if !ok || definition.Position != position || len(definition.Options) != len(input.OptionIDs) {
			return true
		}
		options := make(map[string]struct{}, len(definition.Options))
		for _, option := range definition.Options {
			options[option.ID] = struct{}{}
		}
		for _, optionID := range input.OptionIDs {
			optionID = strings.TrimSpace(optionID)
			if _, duplicate := options[optionID]; !duplicate {
				return true
			}
			delete(options, optionID)
		}
		if len(options) != 0 {
			return true
		}
	}
	return false
}
func combinationCount(ds []ProductVariantDefinition) int {
	n := 1
	if len(ds) == 0 {
		return 0
	}
	for _, d := range ds {
		n *= len(d.Options)
	}
	return n
}
func combinationCountInternal(ds []variantConfigDefinition) int {
	n := 1
	if len(ds) == 0 {
		return 0
	}
	for _, d := range ds {
		n *= len(d.Options)
	}
	return n
}
func normalizeVariantCode(value string) string {
	value = strings.TrimSpace(strings.ToUpper(value))
	var b strings.Builder
	sep := false
	for _, r := range value {
		switch r {
		case 'İ', 'I', 'ı':
			r = 'I'
		case 'Ş':
			r = 'S'
		case 'Ğ':
			r = 'G'
		case 'Ü':
			r = 'U'
		case 'Ö':
			r = 'O'
		case 'Ç':
			r = 'C'
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			sep = false
		} else if (r == '-' || r == '_') && b.Len() > 0 && !sep {
			b.WriteRune(r)
			sep = true
		}
	}
	return strings.Trim(strings.TrimSuffix(b.String(), ""), "-_")[:minInt(len(strings.Trim(b.String(), "-_")), 100)]
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}
