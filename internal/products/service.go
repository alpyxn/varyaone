package products

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Service struct{ pool database.Querier }

// TaxValidationError keeps tax-profile API failures machine-readable while
// retaining the common validation sentinel used by the rest of the product
// module.
type TaxValidationError struct {
	Code    string
	Message string
}

func (e *TaxValidationError) Error() string { return e.Code + ": " + e.Message }
func (e *TaxValidationError) Unwrap() error { return identity.ErrValidation }

func ErrorCode(err error) string {
	var taxErr *TaxValidationError
	if errors.As(err, &taxErr) {
		return taxErr.Code
	}
	var variantErr *VariantValidationError
	if errors.As(err, &variantErr) {
		return variantErr.Code
	}
	return ""
}

func taxValidation(code, message string) error {
	return &TaxValidationError{Code: code, Message: message}
}

func NewService(pool database.Querier) *Service { return &Service{pool: pool} }

type Scope struct {
	BranchID    string
	WarehouseID string
}

type ListOptions struct {
	Scope Scope
	Query string
	// Kind limits the list to one product card kind.  It is intentionally a
	// server-side filter: commercial editors use it for picker UX, while the
	// document services still validate the selected card again on write.
	Kind            string
	CategoryID      string
	BrandID         string
	Cursor          string
	Limit           int
	IncludeInactive bool
}

type Product struct {
	ID                  string      `json:"id"`
	Code                string      `json:"code"`
	SKU                 string      `json:"sku"`
	Name                string      `json:"name"`
	Kind                string      `json:"kind"`
	Description         string      `json:"description"`
	PurchasePrice       string      `json:"purchase_price"`
	SalesPrice          string      `json:"sales_price"`
	CustomDesc1         string      `json:"custom_description_1"`
	CustomDesc2         string      `json:"custom_description_2"`
	CustomDesc3         string      `json:"custom_description_3"`
	PurchaseTaxType     string      `json:"purchase_tax_type"`
	SalesTaxType        string      `json:"sales_tax_type"`
	PurchaseTaxRate     string      `json:"purchase_tax_rate"`
	SalesTaxRate        string      `json:"sales_tax_rate"`
	PurchaseTaxIncluded bool        `json:"purchase_tax_included"`
	SalesTaxIncluded    bool        `json:"sales_tax_included"`
	ExciseTaxRate       string      `json:"excise_tax_rate"`
	WithholdingCode     string      `json:"withholding_code"`
	WithholdingRate     string      `json:"withholding_rate"`
	ExemptionCode       string      `json:"exemption_code"`
	TaxNote             string      `json:"tax_note"`
	PurchaseTaxProfile  *TaxProfile `json:"purchase_tax_profile,omitempty"`
	SalesTaxProfile     *TaxProfile `json:"sales_tax_profile,omitempty"`
	CategoryID          *string     `json:"category_id,omitempty"`
	CategoryName        string      `json:"category_name,omitempty"`
	BrandID             *string     `json:"brand_id,omitempty"`
	BrandName           string      `json:"brand_name,omitempty"`
	BarcodeSummary      string      `json:"barcode_summary,omitempty"`
	UnitSummary         string      `json:"unit_summary,omitempty"`
	// Keep these collections present in the API contract, including when they
	// are empty.  Product detail forms render their rows directly and should
	// never have to distinguish an omitted/null collection from an empty one.
	Units             []ProductUnit    `json:"units"`
	Barcodes          []ProductBarcode `json:"barcodes"`
	PhysicalQuantity  string           `json:"physical_quantity"`
	ReservedQuantity  string           `json:"reserved_quantity"`
	AvailableQuantity string           `json:"available_quantity"`
	StockUnit         string           `json:"stock_unit"`
	NetPrice          string           `json:"net_price"`
	// PurchaseTaxComponents/SalesTaxComponents are the card's additional taxes
	// (ÖTV, ÖİV, a company-defined tax) with their value already resolved, so
	// a document line can price them without another catalog round trip.
	PurchaseTaxComponents []TaxLineComponent `json:"purchase_tax_components"`
	SalesTaxComponents    []TaxLineComponent `json:"sales_tax_components"`
	IsActive              bool               `json:"is_active"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
	Version               int64              `json:"version"`
	VariantsEnabled       bool               `json:"variants_enabled"`
	VariantSummary        VariantSummary     `json:"variant_summary"`
}

type VariantSummary struct {
	Count       int `json:"count"`
	ActiveCount int `json:"active_count"`
}

type Input struct {
	Code                string           `json:"code"`
	SKU                 string           `json:"sku,omitempty"`
	AutoCode            bool             `json:"auto_code"`
	Name                string           `json:"name"`
	Kind                string           `json:"kind"`
	Description         string           `json:"description"`
	PurchasePrice       string           `json:"purchase_price"`
	SalesPrice          string           `json:"sales_price"`
	CustomDesc1         string           `json:"custom_description_1"`
	CustomDesc2         string           `json:"custom_description_2"`
	CustomDesc3         string           `json:"custom_description_3"`
	PurchaseTaxType     string           `json:"purchase_tax_type"`
	SalesTaxType        string           `json:"sales_tax_type"`
	PurchaseTaxRate     string           `json:"purchase_tax_rate"`
	SalesTaxRate        string           `json:"sales_tax_rate"`
	PurchaseTaxIncluded bool             `json:"purchase_tax_included"`
	SalesTaxIncluded    bool             `json:"sales_tax_included"`
	ExciseTaxRate       string           `json:"excise_tax_rate"`
	WithholdingCode     string           `json:"withholding_code"`
	WithholdingRate     string           `json:"withholding_rate"`
	ExemptionCode       string           `json:"exemption_code"`
	TaxNote             string           `json:"tax_note"`
	PurchaseTaxProfile  *TaxProfileInput `json:"purchase_tax_profile,omitempty"`
	SalesTaxProfile     *TaxProfileInput `json:"sales_tax_profile,omitempty"`
	CategoryID          string           `json:"category_id,omitempty"`
	BrandID             string           `json:"brand_id,omitempty"`
	IsActive            *bool            `json:"is_active,omitempty"`
	BaseUnit            string           `json:"base_unit"`
	Units               []UnitInput      `json:"units,omitempty"`
	Barcodes            []BarcodeInput   `json:"barcodes,omitempty"`
	VariantsEnabled     *bool            `json:"variants_enabled,omitempty"`
}

// TaxTreatment is deliberately provider-neutral.  Turkish tax codes and
// rates remain data in the central tax catalogs; this field only describes
// how a product profile participates in a purchase or sales document.
const (
	TaxTreatmentStandard      = "STANDARD"
	TaxTreatmentWithholding   = "WITHHOLDING"
	TaxTreatmentExempt        = "EXEMPT"
	TaxTreatmentNotApplicable = "NOT_APPLICABLE"
)

type TaxProfileInput struct {
	Treatment              string                     `json:"treatment"`
	TaxDefinitionID        string                     `json:"tax_definition_id,omitempty"`
	TaxRateID              string                     `json:"tax_rate_id,omitempty"`
	TaxCode                string                     `json:"tax_code"`
	Rate                   string                     `json:"rate"`
	TaxIncluded            bool                       `json:"tax_included"`
	WithholdingRuleID      string                     `json:"withholding_rule_id,omitempty"`
	WithholdingCode        string                     `json:"withholding_code"`
	WithholdingRate        string                     `json:"withholding_rate"`
	WithholdingNumerator   *int                       `json:"withholding_numerator,omitempty"`
	WithholdingDenominator *int                       `json:"withholding_denominator,omitempty"`
	ExemptionID            string                     `json:"exemption_id,omitempty"`
	ExemptionCode          string                     `json:"exemption_code"`
	TaxNote                string                     `json:"tax_note"`
	Components             []TaxProfileComponentInput `json:"components,omitempty"`
}

type TaxProfileComponentInput struct {
	TaxDefinitionID   string         `json:"tax_definition_id"`
	TaxRateID         string         `json:"tax_rate_id,omitempty"`
	RateID            string         `json:"rate_id,omitempty"` // frontend compatibility alias
	CalculationType   string         `json:"calculation_type"`
	IncludedInTaxBase bool           `json:"included_in_tax_base"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

// TaxLineComponent is the document-ready form of one additional tax on a
// product card. Rate is a percentage, a per-unit amount or a flat amount
// depending on CalculationType; IncludedInBase marks a tax that belongs to the
// VAT base, which is where ÖTV sits.
type TaxLineComponent struct {
	Code            string `json:"code"`
	Name            string `json:"name"`
	CalculationType string `json:"calculation_type"`
	Rate            string `json:"rate"`
	IncludedInBase  bool   `json:"included_in_base"`
}

type TaxProfile struct {
	Direction              string                `json:"direction"`
	Treatment              string                `json:"treatment"`
	TaxDefinitionID        *string               `json:"tax_definition_id,omitempty"`
	TaxRateID              *string               `json:"tax_rate_id,omitempty"`
	TaxCode                string                `json:"tax_code"`
	Rate                   string                `json:"rate"`
	TaxIncluded            bool                  `json:"tax_included"`
	WithholdingRuleID      *string               `json:"withholding_rule_id,omitempty"`
	WithholdingCode        string                `json:"withholding_code"`
	WithholdingRate        string                `json:"withholding_rate"`
	WithholdingNumerator   *int                  `json:"withholding_numerator,omitempty"`
	WithholdingDenominator *int                  `json:"withholding_denominator,omitempty"`
	ExemptionID            *string               `json:"exemption_id,omitempty"`
	ExemptionCode          string                `json:"exemption_code"`
	TaxNote                string                `json:"tax_note"`
	Components             []TaxProfileComponent `json:"components,omitempty"`
	Version                int64                 `json:"version"`
}

type TaxProfileComponent struct {
	TaxDefinitionID   string         `json:"tax_definition_id"`
	TaxDefinitionCode string         `json:"tax_definition_code"`
	TaxDefinitionName string         `json:"tax_definition_name"`
	TaxRateID         *string        `json:"tax_rate_id,omitempty"`
	CalculationType   string         `json:"calculation_type"`
	IncludedInTaxBase bool           `json:"included_in_tax_base"`
	Metadata          map[string]any `json:"metadata,omitempty"`
}

type UnitInput struct {
	Code             string `json:"code"`
	IsBase           bool   `json:"is_base"`
	ConversionFactor string `json:"conversion_factor"`
	DecimalScale     *int   `json:"decimal_scale,omitempty"`
}

type ProductUnit struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	IsBase           bool   `json:"is_base"`
	ConversionFactor string `json:"conversion_factor"`
	DecimalScale     int    `json:"decimal_scale"`
}

type BarcodeInput struct {
	Barcode     string `json:"barcode"`
	BarcodeType string `json:"barcode_type,omitempty"`
	IsPrimary   bool   `json:"is_primary"`
}

type ProductBarcode struct {
	ID          string  `json:"id"`
	VariantID   *string `json:"variant_id,omitempty"`
	Barcode     string  `json:"barcode"`
	BarcodeType string  `json:"barcode_type"`
	IsPrimary   bool    `json:"is_primary"`
}

type ProductCategory struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
	Version  int64  `json:"version"`
}

type ProductBrand struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
	Version  int64  `json:"version"`
}

type ReferenceInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type ListResult struct {
	Items      []Product `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type CodeSequence struct {
	Prefix string `json:"prefix"`
	Digits int    `json:"digits"`
}

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func productSelect(session identity.Session, scope Scope) (string, []any, error) {
	branchID, err := scopeUUIDValue("branch_id", scope.BranchID)
	if err != nil {
		return "", nil, err
	}
	warehouseID, err := scopeUUIDValue("warehouse_id", scope.WarehouseID)
	if err != nil {
		return "", nil, err
	}
	return `
WITH stock_summary AS (
    SELECT sp.product_id,
           SUM(sp.physical_quantity)::text AS physical_quantity,
           SUM(sp.reserved_quantity)::text AS reserved_quantity,
           SUM(sp.available_quantity)::text AS available_quantity
	    FROM stock_positions sp
	    JOIN warehouses sw ON sw.company_id=sp.company_id AND sw.id=sp.warehouse_id
	    WHERE sp.company_id=$1
	      AND sw.is_active
	      AND sw.warehouse_type='STANDARD'
      AND ($3::uuid IS NULL OR sw.branch_id=$3::uuid)
      AND ($4::uuid IS NULL OR sp.warehouse_id=$4::uuid)
      AND (
        sw.is_system OR (
          (sw.branch_id IS NULL OR NOT EXISTS (
            SELECT 1 FROM membership_branch_scopes bs
            WHERE bs.company_id=$1 AND bs.user_id=$2
          ) OR EXISTS (
            SELECT 1 FROM membership_branch_scopes bs
            WHERE bs.company_id=$1 AND bs.user_id=$2 AND bs.branch_id=sw.branch_id
          ))
          AND (
            NOT EXISTS (
              SELECT 1 FROM membership_warehouse_scopes ws
              WHERE ws.company_id=$1 AND ws.user_id=$2
            ) OR EXISTS (
              SELECT 1 FROM membership_warehouse_scopes ws
              WHERE ws.company_id=$1 AND ws.user_id=$2 AND ws.warehouse_id=sw.id
            )
          )
        )
      )
    GROUP BY sp.product_id
),
sales_tax_summary AS (
    SELECT ptp.product_id,
           ptp.treatment,
           ptp.rate,
           ptp.tax_included,
           COALESCE(SUM(CASE WHEN c.calculation_type = 'PERCENTAGE' AND c.included_in_tax_base
                             THEN component_rate.value ELSE 0 END), 0) AS base_percentage,
           COALESCE(SUM(CASE WHEN c.calculation_type = 'PERCENTAGE' AND NOT c.included_in_tax_base
                             THEN component_rate.value ELSE 0 END), 0) AS additional_percentage,
           COALESCE(SUM(CASE WHEN c.calculation_type <> 'PERCENTAGE' AND c.included_in_tax_base
                             THEN component_rate.value ELSE 0 END), 0) AS base_amount,
           COALESCE(SUM(CASE WHEN c.calculation_type <> 'PERCENTAGE' AND NOT c.included_in_tax_base
                             THEN component_rate.value ELSE 0 END), 0) AS additional_amount
    FROM product_tax_profiles ptp
    LEFT JOIN product_tax_profile_components c
      ON c.company_id = ptp.company_id
     AND c.product_id = ptp.product_id
     AND c.direction = ptp.direction
    LEFT JOIN tax_definitions ctd
      ON ctd.company_id = c.company_id
     AND ctd.id = c.tax_definition_id
    LEFT JOIN LATERAL (SELECT ` + productComponentRateSQL + `) component_rate ON true
    WHERE ptp.company_id = $1
      AND ptp.direction = 'SALES'
      AND (c.tax_definition_id IS NULL OR UPPER(ctd.code) NOT LIKE 'KDV%')
    GROUP BY ptp.product_id, ptp.treatment, ptp.rate, ptp.tax_included
)
SELECT p.id,p.code,p.name,p.kind::text,p.description,p.purchase_price::text,p.sales_price::text,p.custom_description_1,p.custom_description_2,p.custom_description_3,p.purchase_tax_type,p.sales_tax_type,p.purchase_tax_rate::text,p.sales_tax_rate::text,p.purchase_tax_included,p.sales_tax_included,p.excise_tax_rate::text,p.withholding_code,p.withholding_rate::text,p.exemption_code,p.tax_note,p.category_id,
	       COALESCE(pc.name,''),p.brand_id,COALESCE(pb.name,''),p.is_active,p.variants_enabled,
	       COALESCE((SELECT count(*) FROM product_variants pv WHERE pv.company_id=p.company_id AND pv.product_id=p.id),0),
	       COALESCE((SELECT count(*) FROM product_variants pv WHERE pv.company_id=p.company_id AND pv.product_id=p.id AND pv.is_active),0),
	       COALESCE((SELECT string_agg(pb2.barcode, ', ' ORDER BY pb2.is_primary DESC,pb2.barcode) FROM product_barcodes pb2 WHERE pb2.company_id=p.company_id AND pb2.product_id=p.id AND pb2.variant_id IS NULL),''),
       COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=p.company_id AND pu.product_id=p.id ORDER BY pu.is_base DESC,pu.unit_code LIMIT 1),''),
       p.created_at,p.updated_at,p.version,
       COALESCE(ss.physical_quantity,'0'),
       COALESCE(ss.reserved_quantity,'0'),
       COALESCE(ss.available_quantity,'0'),
       COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=p.company_id AND pu.product_id=p.id ORDER BY pu.is_base DESC,pu.unit_code LIMIT 1),''),
       CASE
         WHEN p.sales_tax_included OR COALESCE(st.tax_included, false) THEN p.sales_price
         ELSE ROUND(
           (p.sales_price * (1 + COALESCE(st.base_percentage, 0) / 100) + COALESCE(st.base_amount, 0))
             * (1 + (
               CASE
                 WHEN COALESCE(st.treatment, p.sales_tax_type) IN ('NOT_APPLICABLE','EXEMPT','YOK','NONE','MUAF','ISTISNA') THEN 0
                 ELSE COALESCE(st.rate, p.sales_tax_rate, 0)
               END + COALESCE(st.additional_percentage, 0)
             ) / 100)
           + COALESCE(st.additional_amount, 0), 2
         )
       END::text,
       ` + productComponentsSQL("PURCHASE") + `,
       ` + productComponentsSQL("SALES") + `
FROM products p
LEFT JOIN product_categories pc ON pc.company_id=p.company_id AND pc.id=p.category_id
LEFT JOIN product_brands pb ON pb.company_id=p.company_id AND pb.id=p.brand_id
LEFT JOIN sales_tax_summary st ON st.product_id=p.id
LEFT JOIN stock_summary ss ON ss.product_id=p.id`,
		[]any{session.CurrentCompanyID, session.User.ID, branchID, warehouseID}, nil
}

func (s *Service) List(ctx context.Context, session identity.Session, options ListOptions) (ListResult, error) {
	if !authorized(session, "product.read") {
		return ListResult{}, identity.ErrForbidden
	}
	if err := s.authorizeScope(ctx, session, options.Scope); err != nil {
		return ListResult{}, err
	}
	if options.Limit < 1 || options.Limit > 100 {
		options.Limit = 50
	}
	afterName, afterID, err := decodeCursor(options.Cursor)
	if err != nil {
		return ListResult{}, fmt.Errorf("%w: geçersiz ürün listesi cursor bilgisi", identity.ErrValidation)
	}
	rawQuery := strings.TrimSpace(options.Query)
	query := normalizeSearchQuery(rawQuery)
	if rawQuery != "" && query == "" {
		return ListResult{Items: []Product{}}, nil
	}

	statement, args, err := productSelect(session, options.Scope)
	if err != nil {
		return ListResult{}, err
	}
	statement += ` WHERE p.company_id=$1`
	if !options.IncludeInactive {
		statement += ` AND p.is_active`
	}
	if query != "" {
		args = append(args, query)
		statement += fmt.Sprintf(` AND p.search_vector @@ to_tsquery('simple',$%d)`, len(args))
	}
	if strings.TrimSpace(options.CategoryID) != "" {
		categoryID, categoryErr := uuid.Parse(strings.TrimSpace(options.CategoryID))
		if categoryErr != nil {
			return ListResult{}, fmt.Errorf("%w: geçersiz kategori filtresi", identity.ErrValidation)
		}
		args = append(args, categoryID.String())
		statement += fmt.Sprintf(` AND p.category_id=$%d::uuid`, len(args))
	}
	if strings.TrimSpace(options.BrandID) != "" {
		brandID, brandErr := uuid.Parse(strings.TrimSpace(options.BrandID))
		if brandErr != nil {
			return ListResult{}, fmt.Errorf("%w: geçersiz marka filtresi", identity.ErrValidation)
		}
		args = append(args, brandID.String())
		statement += fmt.Sprintf(` AND p.brand_id=$%d::uuid`, len(args))
	}
	if kind := strings.ToUpper(strings.TrimSpace(options.Kind)); kind != "" {
		if kind != "PHYSICAL" && kind != "SERVICE" {
			return ListResult{}, fmt.Errorf("%w: geçersiz ürün türü filtresi", identity.ErrValidation)
		}
		args = append(args, kind)
		statement += fmt.Sprintf(` AND p.kind=$%d::product_kind`, len(args))
	}
	if options.Cursor != "" {
		nameParam := len(args) + 1
		idParam := nameParam + 1
		args = append(args, afterName, afterID)
		statement += fmt.Sprintf(` AND (lower(p.name),p.id) > ($%d,$%d::uuid)`, nameParam, idParam)
	}
	limitParam := len(args) + 1
	statement += fmt.Sprintf(` ORDER BY lower(p.name),p.id LIMIT $%d`, limitParam)
	args = append(args, options.Limit+1)
	rows, err := s.pool.Query(ctx, statement, args...)
	if err != nil {
		return ListResult{}, err
	}
	defer rows.Close()
	items := []Product{}
	for rows.Next() {
		item, scanErr := scanProduct(rows)
		if scanErr != nil {
			return ListResult{}, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return ListResult{}, err
	}
	result := ListResult{Items: items}
	if len(items) > options.Limit {
		last := items[options.Limit-1]
		result.Items = items[:options.Limit]
		result.NextCursor = encodeCursor(strings.ToLower(last.Name), last.ID)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, session identity.Session, id string, scope Scope) (Product, error) {
	if !authorized(session, "product.read") {
		return Product{}, identity.ErrForbidden
	}
	if err := s.authorizeScope(ctx, session, scope); err != nil {
		return Product{}, err
	}
	item, err := loadProduct(ctx, s.pool, session, id, scope)
	if errors.Is(err, pgx.ErrNoRows) {
		return Product{}, identity.ErrForbidden
	}
	if err != nil {
		return Product{}, err
	}
	return item, nil
}

func (s *Service) Create(ctx context.Context, session identity.Session, input Input, scope Scope, meta identity.RequestMeta) (Product, error) {
	if !authorized(session, "product.create") {
		return Product{}, identity.ErrForbidden
	}
	if err := s.authorizeScope(ctx, session, scope); err != nil {
		return Product{}, err
	}
	normalized, units, barcodes, err := normalizeInput(input)
	if err != nil {
		return Product{}, err
	}
	if boolDefault(normalized.VariantsEnabled, false) && len(barcodes) > 0 {
		return Product{}, fmt.Errorf("%w: varyantlı üründe barkod varyant kartına eklenmelidir", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Product{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = validateReferences(ctx, tx, session.CurrentCompanyID, normalized.CategoryID, normalized.BrandID, units); err != nil {
		return Product{}, err
	}
	if err = lockProductCodeSequence(ctx, tx, session.CurrentCompanyID); err != nil {
		return Product{}, err
	}
	// Kod boş bırakıldığında şirket serisinden üret. AutoCode eski istemcilerin
	// gönderdiği uyumluluk alanıdır; yeni akışta manuel kodu ezmemelidir.
	if normalized.Code == "" {
		normalized.Code, err = nextCode(ctx, tx, session.CurrentCompanyID)
		if err != nil {
			return Product{}, err
		}
	}
	if normalized.Code == "" {
		return Product{}, fmt.Errorf("%w: stok kodu oluşturulamadı", identity.ErrValidation)
	}
	id := uuid.NewString()
	active := true
	if normalized.IsActive != nil {
		active = *normalized.IsActive
	}
	if _, err = tx.Exec(ctx, `INSERT INTO products(id,company_id,code,name,kind,description,purchase_price,sales_price,custom_description_1,custom_description_2,custom_description_3,purchase_tax_type,sales_tax_type,purchase_tax_rate,sales_tax_rate,purchase_tax_included,sales_tax_included,excise_tax_rate,withholding_code,withholding_rate,exemption_code,tax_note,category_id,brand_id,is_active,variants_enabled) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`, id, session.CurrentCompanyID, normalized.Code, normalized.Name, normalized.Kind, normalized.Description, normalized.PurchasePrice, normalized.SalesPrice, normalized.CustomDesc1, normalized.CustomDesc2, normalized.CustomDesc3, normalized.PurchaseTaxType, normalized.SalesTaxType, normalized.PurchaseTaxRate, normalized.SalesTaxRate, normalized.PurchaseTaxIncluded, normalized.SalesTaxIncluded, normalized.ExciseTaxRate, normalized.WithholdingCode, normalized.WithholdingRate, normalized.ExemptionCode, normalized.TaxNote, nullableID(normalized.CategoryID), nullableID(normalized.BrandID), active, boolDefault(normalized.VariantsEnabled, false)); err != nil {
		return Product{}, mapConstraint(err)
	}
	if err = upsertTaxProfiles(ctx, tx, session.CurrentCompanyID, id, normalized.PurchaseTaxProfile, normalized.SalesTaxProfile); err != nil {
		return Product{}, mapConstraint(err)
	}
	if err = replaceDetails(ctx, tx, session.CurrentCompanyID, id, units, barcodes); err != nil {
		return Product{}, mapConstraint(err)
	}
	if err = refreshSearch(ctx, tx, session.CurrentCompanyID, id); err != nil {
		return Product{}, err
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_CREATED", "product.created", id, map[string]any{"code": normalized.Code, "kind": normalized.Kind, "tax_profiles": taxProfilesAudit(normalized.PurchaseTaxProfile, normalized.SalesTaxProfile)}, meta); err != nil {
		return Product{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Product{}, err
	}
	return s.Get(ctx, session, id, scope)
}

func (s *Service) Update(ctx context.Context, session identity.Session, id string, expectedVersion int64, input Input, scope Scope, meta identity.RequestMeta) (Product, error) {
	if !authorized(session, "product.edit") {
		return Product{}, identity.ErrForbidden
	}
	if err := s.authorizeScope(ctx, session, scope); err != nil {
		return Product{}, err
	}
	if expectedVersion < 1 {
		return Product{}, fmt.Errorf("%w: geçerli ürün sürümü gereklidir", identity.ErrValidation)
	}
	normalized, units, barcodes, err := normalizeInput(input)
	if err != nil {
		return Product{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Product{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err = validateReferences(ctx, tx, session.CurrentCompanyID, normalized.CategoryID, normalized.BrandID, units); err != nil {
		return Product{}, err
	}
	if err = lockProductCodeSequence(ctx, tx, session.CurrentCompanyID); err != nil {
		return Product{}, err
	}
	var active bool
	var currentVariantsEnabled bool
	if err = tx.QueryRow(ctx, `SELECT is_active,variants_enabled FROM products WHERE company_id=$1 AND id=$2 FOR UPDATE`, session.CurrentCompanyID, id).Scan(&active, &currentVariantsEnabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Product{}, identity.ErrForbidden
		}
		return Product{}, err
	}
	var currentBaseUnit string
	baseUnitErr := tx.QueryRow(ctx, `SELECT unit_code FROM product_units WHERE company_id=$1 AND product_id=$2 AND is_base LIMIT 1`, session.CurrentCompanyID, id).Scan(&currentBaseUnit)
	if baseUnitErr != nil && !errors.Is(baseUnitErr, pgx.ErrNoRows) {
		return Product{}, baseUnitErr
	}
	if baseUnitErr == nil {
		if err = validateBaseUnitImmutable(currentBaseUnit, units); err != nil {
			return Product{}, err
		}
	}
	if normalized.IsActive != nil {
		active = *normalized.IsActive
	}
	if normalized.Code == "" {
		return Product{}, fmt.Errorf("%w: stok kodu gereklidir; mevcut kodu korumak için karttan okuyup gönderin", identity.ErrValidation)
	}
	variantsEnabled := currentVariantsEnabled
	if normalized.VariantsEnabled != nil && *normalized.VariantsEnabled != currentVariantsEnabled {
		return Product{}, fmt.Errorf("%w: varyant modu yalnızca varyant ayarlarından değiştirilebilir", identity.ErrValidation)
	}
	if variantsEnabled && len(barcodes) > 0 {
		return Product{}, fmt.Errorf("%w: varyantlı üründe barkod varyant kartına eklenmelidir", identity.ErrValidation)
	}
	if variantsEnabled && normalized.Kind != "PHYSICAL" {
		return Product{}, fmt.Errorf("%w: varyant modu yalnızca fiziksel ürünlerde kullanılabilir", identity.ErrValidation)
	}
	tag, err := tx.Exec(ctx, `UPDATE products SET code=$1,name=$2,kind=$3,description=$4,purchase_price=$5,sales_price=$6,custom_description_1=$7,custom_description_2=$8,custom_description_3=$9,purchase_tax_type=$10,sales_tax_type=$11,purchase_tax_rate=$12,sales_tax_rate=$13,purchase_tax_included=$14,sales_tax_included=$15,excise_tax_rate=$16,withholding_code=$17,withholding_rate=$18,exemption_code=$19,tax_note=$20,category_id=$21,brand_id=$22,is_active=$23,variants_enabled=$24,updated_at=now(),version=version+1 WHERE company_id=$25 AND id=$26 AND version=$27`, normalized.Code, normalized.Name, normalized.Kind, normalized.Description, normalized.PurchasePrice, normalized.SalesPrice, normalized.CustomDesc1, normalized.CustomDesc2, normalized.CustomDesc3, normalized.PurchaseTaxType, normalized.SalesTaxType, normalized.PurchaseTaxRate, normalized.SalesTaxRate, normalized.PurchaseTaxIncluded, normalized.SalesTaxIncluded, normalized.ExciseTaxRate, normalized.WithholdingCode, normalized.WithholdingRate, normalized.ExemptionCode, normalized.TaxNote, nullableID(normalized.CategoryID), nullableID(normalized.BrandID), active, variantsEnabled, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Product{}, mapConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Product{}, identity.ErrConflict
	}
	if err = upsertTaxProfiles(ctx, tx, session.CurrentCompanyID, id, normalized.PurchaseTaxProfile, normalized.SalesTaxProfile); err != nil {
		return Product{}, mapConstraint(err)
	}
	if err = replaceDetails(ctx, tx, session.CurrentCompanyID, id, units, barcodes); err != nil {
		return Product{}, mapConstraint(err)
	}
	if err = refreshSearch(ctx, tx, session.CurrentCompanyID, id); err != nil {
		return Product{}, err
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_UPDATED", "product.updated", id, map[string]any{"code": normalized.Code, "version": expectedVersion + 1, "tax_profiles": taxProfilesAudit(normalized.PurchaseTaxProfile, normalized.SalesTaxProfile)}, meta); err != nil {
		return Product{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Product{}, err
	}
	return s.Get(ctx, session, id, scope)
}

func (s *Service) Deactivate(ctx context.Context, session identity.Session, id string, expectedVersion int64, scope Scope, meta identity.RequestMeta) (Product, error) {
	if !authorized(session, "product.deactivate") {
		return Product{}, identity.ErrForbidden
	}
	if err := s.authorizeScope(ctx, session, scope); err != nil {
		return Product{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Product{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	tag, err := tx.Exec(ctx, `UPDATE products SET is_active=false,updated_at=now(),version=version+1 WHERE company_id=$1 AND id=$2 AND version=$3 AND is_active`, session.CurrentCompanyID, id, expectedVersion)
	if err != nil {
		return Product{}, err
	}
	if tag.RowsAffected() == 0 {
		return Product{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_DEACTIVATED", "product.deactivated", id, map[string]any{"product_id": id}, meta); err != nil {
		return Product{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Product{}, err
	}
	return s.Get(ctx, session, id, scope)
}

func (s *Service) ListUnits(ctx context.Context, session identity.Session) ([]ProductUnit, error) {
	if !authorized(session, "product.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT code,name,false,1::numeric,decimal_scale FROM units WHERE is_active ORDER BY name,code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProductUnit{}
	for rows.Next() {
		var item ProductUnit
		if err = rows.Scan(&item.Code, &item.Name, &item.IsBase, &item.ConversionFactor, &item.DecimalScale); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) ListCategories(ctx context.Context, session identity.Session) ([]ProductCategory, error) {
	if !authorized(session, "product.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,code,name,is_active,version FROM product_categories WHERE company_id=$1 ORDER BY lower(name),id`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProductCategory{}
	for rows.Next() {
		var item ProductCategory
		if err = rows.Scan(&item.ID, &item.Code, &item.Name, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateCategory(ctx context.Context, session identity.Session, input ReferenceInput, meta identity.RequestMeta) (ProductCategory, error) {
	if !authorized(session, "product.reference.manage") {
		return ProductCategory{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return ProductCategory{}, fmt.Errorf("%w: kategori adı gereklidir", identity.ErrValidation)
	}
	id := uuid.NewString()
	if input.Code == "" {
		input.Code = "KAT-" + strings.ToUpper(id[:8])
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProductCategory{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO product_categories(id,company_id,code,name) VALUES($1,$2,$3,$4)`, id, session.CurrentCompanyID, input.Code, input.Name); err != nil {
		return ProductCategory{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_CATEGORY_CREATED", "product.category.created", id, map[string]any{"code": input.Code}, meta); err != nil {
		return ProductCategory{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProductCategory{}, err
	}
	return ProductCategory{ID: id, Code: input.Code, Name: input.Name, IsActive: true, Version: 1}, nil
}

func (s *Service) ListBrands(ctx context.Context, session identity.Session) ([]ProductBrand, error) {
	if !authorized(session, "product.read") {
		return nil, identity.ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id,code,name,is_active,version FROM product_brands WHERE company_id=$1 ORDER BY lower(name),id`, session.CurrentCompanyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ProductBrand{}
	for rows.Next() {
		var item ProductBrand
		if err = rows.Scan(&item.ID, &item.Code, &item.Name, &item.IsActive, &item.Version); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) CreateBrand(ctx context.Context, session identity.Session, input ReferenceInput, meta identity.RequestMeta) (ProductBrand, error) {
	if !authorized(session, "product.reference.manage") {
		return ProductBrand{}, identity.ErrForbidden
	}
	input.Code = strings.ToUpper(strings.TrimSpace(input.Code))
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return ProductBrand{}, fmt.Errorf("%w: marka adı gereklidir", identity.ErrValidation)
	}
	id := uuid.NewString()
	if input.Code == "" {
		input.Code = "MRK-" + strings.ToUpper(id[:8])
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProductBrand{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO product_brands(id,company_id,code,name) VALUES($1,$2,$3,$4)`, id, session.CurrentCompanyID, input.Code, input.Name); err != nil {
		return ProductBrand{}, mapConstraint(err)
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_BRAND_CREATED", "product.brand.created", id, map[string]any{"code": input.Code}, meta); err != nil {
		return ProductBrand{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProductBrand{}, err
	}
	return ProductBrand{ID: id, Code: input.Code, Name: input.Name, IsActive: true, Version: 1}, nil
}

func (s *Service) SetCategoryActive(ctx context.Context, session identity.Session, id string, expectedVersion int64, active bool, meta identity.RequestMeta) (ProductCategory, error) {
	return s.setReferenceActive(ctx, session, id, expectedVersion, active, "product_categories", "PRODUCT_CATEGORY", meta)
}

func (s *Service) SetBrandActive(ctx context.Context, session identity.Session, id string, expectedVersion int64, active bool, meta identity.RequestMeta) (ProductBrand, error) {
	return s.setReferenceActiveBrand(ctx, session, id, expectedVersion, active, meta)
}

func (s *Service) setReferenceActive(ctx context.Context, session identity.Session, id string, expectedVersion int64, active bool, table, eventType string, meta identity.RequestMeta) (ProductCategory, error) {
	if !authorized(session, "product.reference.manage") {
		return ProductCategory{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProductCategory{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var item ProductCategory
	err = tx.QueryRow(ctx, fmt.Sprintf(`UPDATE %s SET is_active=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4 RETURNING id,code,name,is_active,version`, table), active, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.Code, &item.Name, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductCategory{}, identity.ErrConflict
	}
	if err != nil {
		return ProductCategory{}, err
	}
	if err = writeAuditAndEvent(ctx, tx, session, eventType+map[bool]string{true: "_ACTIVATED", false: "_DEACTIVATED"}[active], "product.reference.active", id, map[string]any{"active": active}, meta); err != nil {
		return ProductCategory{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProductCategory{}, err
	}
	return item, nil
}

func (s *Service) setReferenceActiveBrand(ctx context.Context, session identity.Session, id string, expectedVersion int64, active bool, meta identity.RequestMeta) (ProductBrand, error) {
	if !authorized(session, "product.reference.manage") {
		return ProductBrand{}, identity.ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProductBrand{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var item ProductBrand
	err = tx.QueryRow(ctx, `UPDATE product_brands SET is_active=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3 AND version=$4 RETURNING id,code,name,is_active,version`, active, session.CurrentCompanyID, id, expectedVersion).Scan(&item.ID, &item.Code, &item.Name, &item.IsActive, &item.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProductBrand{}, identity.ErrConflict
	}
	if err != nil {
		return ProductBrand{}, err
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_BRAND"+map[bool]string{true: "_ACTIVATED", false: "_DEACTIVATED"}[active], "product.reference.active", id, map[string]any{"active": active}, meta); err != nil {
		return ProductBrand{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ProductBrand{}, err
	}
	return item, nil
}

func (s *Service) GetCodeSequence(ctx context.Context, session identity.Session) (CodeSequence, error) {
	if !authorized(session, "product.read") {
		return CodeSequence{}, identity.ErrForbidden
	}
	var item CodeSequence
	err := s.pool.QueryRow(ctx, `SELECT prefix,digits FROM company_product_sequences WHERE company_id=$1`, session.CurrentCompanyID).Scan(&item.Prefix, &item.Digits)
	if errors.Is(err, pgx.ErrNoRows) {
		return CodeSequence{Prefix: "STK", Digits: 6}, nil
	}
	return item, err
}

func (s *Service) SaveCodeSequence(ctx context.Context, session identity.Session, input CodeSequence, meta identity.RequestMeta) (CodeSequence, error) {
	if !authorized(session, "product.reference.manage") {
		return CodeSequence{}, identity.ErrForbidden
	}
	input.Prefix = strings.ToUpper(strings.TrimSpace(input.Prefix))
	if input.Digits < 3 || input.Digits > 12 || !regexp.MustCompile(`^[A-Z0-9_-]{1,8}$`).MatchString(input.Prefix) {
		return CodeSequence{}, fmt.Errorf("%w: otomatik stok kodu öneki ve basamak sayısı geçersiz", identity.ErrValidation)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CodeSequence{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, `INSERT INTO company_product_sequences(company_id,prefix,digits) VALUES($1,$2,$3) ON CONFLICT(company_id) DO UPDATE SET prefix=excluded.prefix,digits=excluded.digits,updated_at=now()`, session.CurrentCompanyID, input.Prefix, input.Digits); err != nil {
		return CodeSequence{}, err
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_CODE_SEQUENCE_UPDATED", "product.code_sequence.updated", session.CurrentCompanyID, map[string]any{"prefix": input.Prefix, "digits": input.Digits}, meta); err != nil {
		return CodeSequence{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CodeSequence{}, err
	}
	return input, nil
}

func normalizeInput(input Input) (Input, []UnitInput, []BarcodeInput, error) {
	var err error
	rawCode := strings.TrimSpace(input.Code)
	rawSKU := strings.TrimSpace(input.SKU)
	input.Code = normalizeVariantCode(rawCode)
	input.SKU = normalizeVariantCode(rawSKU)
	if rawCode != "" && input.Code == "" {
		return Input{}, nil, nil, fmt.Errorf("%w: stok kodu yalnızca harf, rakam, tire veya alt çizgi içerebilir", identity.ErrValidation)
	}
	if rawSKU != "" && input.SKU == "" {
		return Input{}, nil, nil, fmt.Errorf("%w: SKU yalnızca harf, rakam, tire veya alt çizgi içerebilir", identity.ErrValidation)
	}
	if input.Code != "" && input.SKU != "" && input.Code != input.SKU {
		return Input{}, nil, nil, fmt.Errorf("%w: stok kodu ve SKU aynı olmalıdır", identity.ErrValidation)
	}
	if input.Code == "" {
		input.Code = input.SKU
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.PurchasePrice = normalizePrice(input.PurchasePrice)
	input.SalesPrice = normalizePrice(input.SalesPrice)
	input.CustomDesc1 = strings.TrimSpace(input.CustomDesc1)
	input.CustomDesc2 = strings.TrimSpace(input.CustomDesc2)
	input.CustomDesc3 = strings.TrimSpace(input.CustomDesc3)
	input.PurchaseTaxType = normalizeTaxType(input.PurchaseTaxType)
	input.SalesTaxType = normalizeTaxType(input.SalesTaxType)
	input.PurchaseTaxRate = normalizeTaxRate(input.PurchaseTaxRate)
	input.SalesTaxRate = normalizeTaxRate(input.SalesTaxRate)
	input.ExciseTaxRate = normalizeTaxRate(input.ExciseTaxRate)
	input.WithholdingRate = normalizeTaxRate(input.WithholdingRate)
	input.WithholdingCode = strings.TrimSpace(input.WithholdingCode)
	input.ExemptionCode = strings.TrimSpace(input.ExemptionCode)
	input.TaxNote = strings.TrimSpace(input.TaxNote)
	input.PurchaseTaxProfile, err = normalizeTaxProfile(input.PurchaseTaxProfile, "PURCHASE", input.PurchaseTaxType, input.PurchaseTaxRate, input.PurchaseTaxIncluded, input.WithholdingCode, input.WithholdingRate, input.ExemptionCode, input.TaxNote)
	if err != nil {
		return Input{}, nil, nil, err
	}
	input.SalesTaxProfile, err = normalizeTaxProfile(input.SalesTaxProfile, "SALES", input.SalesTaxType, input.SalesTaxRate, input.SalesTaxIncluded, input.WithholdingCode, input.WithholdingRate, input.ExemptionCode, input.TaxNote)
	if err != nil {
		return Input{}, nil, nil, err
	}
	input.Kind = strings.ToUpper(strings.TrimSpace(input.Kind))
	if input.Name == "" || len([]rune(input.Name)) > 240 {
		return Input{}, nil, nil, fmt.Errorf("%w: ürün/hizmet adı gereklidir ve 240 karakteri aşamaz", identity.ErrValidation)
	}
	if input.PurchasePrice == "" || input.SalesPrice == "" {
		return Input{}, nil, nil, fmt.Errorf("%w: alış ve satış fiyatı sıfır veya pozitif olmalıdır", identity.ErrValidation)
	}
	if input.Kind != "PHYSICAL" && input.Kind != "SERVICE" {
		return Input{}, nil, nil, fmt.Errorf("%w: kart türü fiziksel ürün veya hizmet olmalıdır", identity.ErrValidation)
	}
	input.CategoryID = strings.TrimSpace(input.CategoryID)
	input.BrandID = strings.TrimSpace(input.BrandID)
	input.BaseUnit = strings.ToUpper(strings.TrimSpace(input.BaseUnit))
	units, err := normalizeUnits(input.Units, input.BaseUnit)
	if err != nil {
		return Input{}, nil, nil, err
	}
	barcodes, err := normalizeBarcodes(input.Barcodes)
	if err != nil {
		return Input{}, nil, nil, err
	}
	return input, units, barcodes, nil
}

func normalizeUnits(input []UnitInput, baseUnit string) ([]UnitInput, error) {
	if len(input) == 0 {
		if baseUnit == "" {
			return nil, fmt.Errorf("%w: temel birim gereklidir", identity.ErrValidation)
		}
		input = []UnitInput{{Code: baseUnit, IsBase: true, ConversionFactor: "1"}}
	}
	if len(input) > 1 {
		return nil, fmt.Errorf("%w: bir stok kartında yalnızca bir stok birimi kullanılabilir", identity.ErrValidation)
	}
	seen := map[string]struct{}{}
	baseCount := 0
	for index := range input {
		input[index].Code = strings.ToUpper(strings.TrimSpace(input[index].Code))
		input[index].ConversionFactor = normalizeDecimal(input[index].ConversionFactor)
		if input[index].Code == "" || input[index].ConversionFactor == "" {
			return nil, fmt.Errorf("%w: birim kodu ve pozitif dönüşüm katsayısı gereklidir", identity.ErrValidation)
		}
		if _, ok := seen[input[index].Code]; ok {
			return nil, fmt.Errorf("%w: aynı birim kartta iki kez kullanılamaz", identity.ErrValidation)
		}
		seen[input[index].Code] = struct{}{}
		if input[index].IsBase {
			baseCount++
		}
		if input[index].DecimalScale == nil {
			defaultScale := 3
			input[index].DecimalScale = &defaultScale
		}
		if *input[index].DecimalScale < 0 || *input[index].DecimalScale > 8 {
			return nil, fmt.Errorf("%w: birim ondalık basamağı 0 ile 8 arasında olmalıdır", identity.ErrValidation)
		}
	}
	if baseUnit != "" {
		found := false
		for index := range input {
			if input[index].Code == baseUnit {
				found = true
				if baseCount == 0 {
					input[index].IsBase = true
					baseCount = 1
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: temel birim, birim listesinde bulunmalıdır", identity.ErrValidation)
		}
	}
	if baseCount != 1 {
		return nil, fmt.Errorf("%w: tam olarak bir temel birim seçilmelidir", identity.ErrValidation)
	}
	for _, item := range input {
		if item.IsBase && item.ConversionFactor != "1" {
			return nil, fmt.Errorf("%w: temel birimin dönüşüm katsayısı 1 olmalıdır", identity.ErrValidation)
		}
	}
	return input, nil
}

func validateBaseUnitImmutable(currentBaseUnit string, units []UnitInput) error {
	currentBaseUnit = strings.ToUpper(strings.TrimSpace(currentBaseUnit))
	if currentBaseUnit == "" {
		return nil
	}
	for _, unit := range units {
		if !unit.IsBase {
			continue
		}
		nextBaseUnit := strings.ToUpper(strings.TrimSpace(unit.Code))
		if currentBaseUnit != nextBaseUnit {
			return fmt.Errorf("%w: stok kartının stok birimi değiştirilemez; mevcut birim: %s, yeni birim: %s", identity.ErrValidation, currentBaseUnit, nextBaseUnit)
		}
		return nil
	}
	return fmt.Errorf("%w: stok kartının stok birimi korunamadı", identity.ErrValidation)
}

func normalizeBarcodes(input []BarcodeInput) ([]BarcodeInput, error) {
	seen := map[string]struct{}{}
	primary := 0
	items := make([]BarcodeInput, 0, len(input))
	for _, item := range input {
		item.Barcode = strings.TrimSpace(item.Barcode)
		item.BarcodeType = strings.ToUpper(strings.TrimSpace(item.BarcodeType))
		if item.Barcode == "" {
			continue
		}
		if item.BarcodeType == "" {
			item.BarcodeType = "EAN"
		}
		if _, ok := seen[item.Barcode]; ok {
			return nil, fmt.Errorf("%w: aynı barkod birden fazla eklenemez", identity.ErrValidation)
		}
		seen[item.Barcode] = struct{}{}
		if item.IsPrimary {
			primary++
		}
		items = append(items, item)
	}
	if primary > 1 {
		return nil, fmt.Errorf("%w: yalnızca bir barkod ana barkod olabilir", identity.ErrValidation)
	}
	if len(items) > 0 && primary == 0 {
		items[0].IsPrimary = true
	}
	return items, nil
}

func validateReferences(ctx context.Context, tx pgx.Tx, companyID, categoryID, brandID string, units []UnitInput) error {
	if categoryID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_categories WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, categoryID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: kategori bu firmada bulunamadı veya pasif", identity.ErrValidation)
		}
	}
	if brandID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_brands WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, brandID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: marka bu firmada bulunamadı veya pasif", identity.ErrValidation)
		}
	}
	for _, unit := range units {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM units WHERE code=$1 AND is_active)`, unit.Code).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: geçersiz veya pasif birim: %s", identity.ErrValidation, unit.Code)
		}
	}
	return nil
}

func replaceDetails(ctx context.Context, tx pgx.Tx, companyID, productID string, units []UnitInput, barcodes []BarcodeInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM product_units WHERE company_id=$1 AND product_id=$2`, companyID, productID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id IS NULL`, companyID, productID); err != nil {
		return err
	}
	for _, unit := range units {
		if _, err := tx.Exec(ctx, `INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor,decimal_scale) VALUES($1,$2,$3,$4,$5,$6)`, companyID, productID, unit.Code, unit.IsBase, unit.ConversionFactor, *unit.DecimalScale); err != nil {
			return err
		}
	}
	for _, barcode := range barcodes {
		if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,$6)`, uuid.NewString(), companyID, productID, barcode.Barcode, barcode.BarcodeType, barcode.IsPrimary); err != nil {
			return err
		}
	}
	return nil
}

func refreshSearch(ctx context.Context, tx pgx.Tx, companyID, productID string) error {
	_, err := tx.Exec(ctx, `UPDATE products p SET search_vector =
		setweight(to_tsvector('simple', coalesce(p.code,'')), 'A') ||
		setweight(to_tsvector('simple', regexp_replace(coalesce(p.code,''),'[^[:alnum:]]','','g')), 'A') ||
        setweight(to_tsvector('simple', coalesce(p.name,'')), 'A') ||
        setweight(to_tsvector('simple', coalesce(p.description,'')), 'C') ||
        setweight(to_tsvector('simple', coalesce((SELECT pc.name FROM product_categories pc WHERE pc.company_id=p.company_id AND pc.id=p.category_id),'')), 'B') ||
        setweight(to_tsvector('simple', coalesce((SELECT pb.name FROM product_brands pb WHERE pb.company_id=p.company_id AND pb.id=p.brand_id),'')), 'B') ||
        setweight(to_tsvector('simple', coalesce((SELECT string_agg(barcode,' ') FROM product_barcodes WHERE company_id=p.company_id AND product_id=p.id),'')), 'A')
        WHERE p.company_id=$1 AND p.id=$2`, companyID, productID)
	return err
}

func loadProduct(ctx context.Context, q rowQuerier, session identity.Session, id string, scope Scope) (Product, error) {
	selection, args, err := productSelect(session, scope)
	if err != nil {
		return Product{}, err
	}
	productArgs := append(append([]any{}, args...), id)
	item, err := scanProduct(q.QueryRow(ctx, selection+` WHERE p.company_id=$1 AND p.id=$5`, productArgs...))
	if err != nil {
		return Product{}, err
	}
	detailArgs := []any{session.CurrentCompanyID, id}
	// Product cards expose one unit. The LIMIT also keeps legacy cards that
	// still contain conversion rows from reintroducing multiple choices in the
	// detail API; the next successful save replaces those legacy rows.
	rows, err := q.Query(ctx, `SELECT pu.unit_code,u.name,pu.is_base,pu.conversion_factor::text,pu.decimal_scale FROM product_units pu JOIN units u ON u.code=pu.unit_code WHERE pu.company_id=$1 AND pu.product_id=$2 ORDER BY pu.is_base DESC,pu.unit_code LIMIT 1`, detailArgs...)
	if err != nil {
		return Product{}, err
	}
	for rows.Next() {
		var unit ProductUnit
		if err = rows.Scan(&unit.Code, &unit.Name, &unit.IsBase, &unit.ConversionFactor, &unit.DecimalScale); err != nil {
			rows.Close()
			return Product{}, err
		}
		item.Units = append(item.Units, unit)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Product{}, err
	}
	rows.Close()
	rows, err = q.Query(ctx, `SELECT id,barcode,barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id IS NULL ORDER BY is_primary DESC,barcode`, detailArgs...)
	if err != nil {
		return Product{}, err
	}
	for rows.Next() {
		var barcode ProductBarcode
		if err = rows.Scan(&barcode.ID, &barcode.Barcode, &barcode.BarcodeType, &barcode.IsPrimary); err != nil {
			rows.Close()
			return Product{}, err
		}
		item.Barcodes = append(item.Barcodes, barcode)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Product{}, err
	}
	rows, err = q.Query(ctx, `SELECT ptp.direction,ptp.treatment,ptp.tax_definition_id,ptp.tax_rate_id,ptp.tax_code,ptp.rate::text,ptp.tax_included,ptp.withholding_rule_id,COALESCE(NULLIF(ptp.withholding_code,''),wr.code,''),ptp.withholding_rate::text,COALESCE(ptp.withholding_numerator,wr.ratio_numerator),COALESCE(ptp.withholding_denominator,wr.ratio_denominator),ptp.exemption_id,ptp.exemption_code,ptp.tax_note,ptp.version FROM product_tax_profiles ptp LEFT JOIN tax_withholding_rules wr ON wr.company_id=ptp.company_id AND wr.id=ptp.withholding_rule_id WHERE ptp.company_id=$1 AND ptp.product_id=$2 ORDER BY ptp.direction`, detailArgs...)
	if err != nil {
		return Product{}, err
	}
	// Collect the profile headers before issuing the per-direction component
	// queries below: on a request-pinned connection the nested q.Query would hit
	// "conn busy" while these rows are still open.
	var profiles []*TaxProfile
	for rows.Next() {
		var direction, treatment, taxCode, rate, withholdingCode, withholdingRate, exemptionCode, taxNote string
		var taxDefinitionID, taxRateID, withholdingRuleID, exemptionID *string
		var withholdingNumerator, withholdingDenominator *int
		var taxIncluded bool
		var version int64
		if err = rows.Scan(&direction, &treatment, &taxDefinitionID, &taxRateID, &taxCode, &rate, &taxIncluded, &withholdingRuleID, &withholdingCode, &withholdingRate, &withholdingNumerator, &withholdingDenominator, &exemptionID, &exemptionCode, &taxNote, &version); err != nil {
			rows.Close()
			return Product{}, err
		}
		profiles = append(profiles, &TaxProfile{Direction: direction, Treatment: treatment, TaxDefinitionID: taxDefinitionID, TaxRateID: taxRateID, TaxCode: taxCode, Rate: rate, TaxIncluded: taxIncluded, WithholdingRuleID: withholdingRuleID, WithholdingCode: withholdingCode, WithholdingRate: withholdingRate, WithholdingNumerator: withholdingNumerator, WithholdingDenominator: withholdingDenominator, ExemptionID: exemptionID, ExemptionCode: exemptionCode, TaxNote: taxNote, Version: version})
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Product{}, err
	}
	rows.Close()
	for _, profile := range profiles {
		direction := profile.Direction
		componentRows, componentErr := q.Query(ctx, `SELECT c.tax_definition_id,COALESCE(td.code,''),COALESCE(td.name,''),c.tax_rate_id,c.calculation_type,c.included_in_tax_base,c.metadata FROM product_tax_profile_components c JOIN tax_definitions td ON td.company_id=c.company_id AND td.id=c.tax_definition_id WHERE c.company_id=$1 AND c.product_id=$2 AND c.direction=$3 AND UPPER(td.code) NOT LIKE 'KDV%' ORDER BY c.sequence`, session.CurrentCompanyID, id, direction)
		if componentErr != nil {
			return Product{}, componentErr
		}
		for componentRows.Next() {
			var component TaxProfileComponent
			var metadata []byte
			if componentErr = componentRows.Scan(&component.TaxDefinitionID, &component.TaxDefinitionCode, &component.TaxDefinitionName, &component.TaxRateID, &component.CalculationType, &component.IncludedInTaxBase, &metadata); componentErr != nil {
				componentRows.Close()
				return Product{}, componentErr
			}
			_ = json.Unmarshal(metadata, &component.Metadata)
			if component.Metadata == nil {
				component.Metadata = map[string]any{}
			}
			profile.Components = append(profile.Components, component)
		}
		componentErr = componentRows.Err()
		componentRows.Close()
		if componentErr != nil {
			return Product{}, componentErr
		}
		if direction == "PURCHASE" {
			item.PurchaseTaxProfile = profile
		} else if direction == "SALES" {
			item.SalesTaxProfile = profile
		}
	}
	return item, nil
}

// productComponentRateSQL resolves what one additional tax component is worth:
// the rate row it points at, the value typed on the card, or the definition's
// currently valid rate - the same order the document resolver uses.
const productComponentRateSQL = `COALESCE(
        (SELECT tr.rate FROM tax_rates tr WHERE tr.company_id=c.company_id AND tr.id=c.tax_rate_id),
        CASE WHEN replace(COALESCE(c.metadata->>'rate',''),',','.') ~ '^[0-9]+(\.[0-9]+)?$'
             THEN replace(c.metadata->>'rate',',','.')::numeric END,
        (SELECT dr.rate FROM tax_rates dr
          WHERE dr.company_id=c.company_id AND dr.tax_definition_id=c.tax_definition_id
            AND dr.valid_from <= CURRENT_DATE AND (dr.valid_to IS NULL OR dr.valid_to >= CURRENT_DATE)
          ORDER BY dr.valid_from DESC, dr.id LIMIT 1),
        0) AS value`

// productComponentsSQL lists a card's additional taxes for one direction, in
// the shape a document line needs them.
func productComponentsSQL(direction string) string {
	return `COALESCE((SELECT jsonb_agg(jsonb_build_object(
            'code', td.code,
            'name', td.name,
            'calculation_type', c.calculation_type,
            'rate', trim_scale(component_rate.value)::text,
            'included_in_base', c.included_in_tax_base) ORDER BY c.sequence)
          FROM product_tax_profile_components c
          JOIN tax_definitions td ON td.company_id=c.company_id AND td.id=c.tax_definition_id
          LEFT JOIN LATERAL (SELECT ` + productComponentRateSQL + `) component_rate ON true
         WHERE c.company_id=p.company_id AND c.product_id=p.id AND c.direction='` + direction + `'
           AND UPPER(td.code) NOT LIKE 'KDV%'),'[]'::jsonb)::text`
}

func decodeTaxLineComponents(raw []byte) []TaxLineComponent {
	components := []TaxLineComponent{}
	if len(raw) == 0 {
		return components
	}
	_ = json.Unmarshal(raw, &components)
	if components == nil {
		components = []TaxLineComponent{}
	}
	return components
}

func scanProduct(row interface{ Scan(...any) error }) (Product, error) {
	item := Product{
		Units:    make([]ProductUnit, 0),
		Barcodes: make([]ProductBarcode, 0),
	}
	var kind string
	var purchaseComponents, salesComponents []byte
	if err := row.Scan(&item.ID, &item.Code, &item.Name, &kind, &item.Description, &item.PurchasePrice, &item.SalesPrice, &item.CustomDesc1, &item.CustomDesc2, &item.CustomDesc3, &item.PurchaseTaxType, &item.SalesTaxType, &item.PurchaseTaxRate, &item.SalesTaxRate, &item.PurchaseTaxIncluded, &item.SalesTaxIncluded, &item.ExciseTaxRate, &item.WithholdingCode, &item.WithholdingRate, &item.ExemptionCode, &item.TaxNote, &item.CategoryID, &item.CategoryName, &item.BrandID, &item.BrandName, &item.IsActive, &item.VariantsEnabled, &item.VariantSummary.Count, &item.VariantSummary.ActiveCount, &item.BarcodeSummary, &item.UnitSummary, &item.CreatedAt, &item.UpdatedAt, &item.Version, &item.PhysicalQuantity, &item.ReservedQuantity, &item.AvailableQuantity, &item.StockUnit, &item.NetPrice, &purchaseComponents, &salesComponents); err != nil {
		return Product{}, err
	}
	item.PurchaseTaxComponents = decodeTaxLineComponents(purchaseComponents)
	item.SalesTaxComponents = decodeTaxLineComponents(salesComponents)
	item.Kind = kind
	item.SKU = item.Code
	return item, nil
}

func (s *Service) authorizeScope(ctx context.Context, session identity.Session, scope Scope) error {
	if scope.BranchID == "" && scope.WarehouseID == "" {
		return nil
	}
	if scope.BranchID != "" {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM branches WHERE company_id=$1 AND id=$2 AND is_active)`, session.CurrentCompanyID, scope.BranchID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return identity.ErrForbidden
		}
		var hasScopes, allowed bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM membership_branch_scopes WHERE company_id=$1 AND user_id=$2), EXISTS(SELECT 1 FROM membership_branch_scopes WHERE company_id=$1 AND user_id=$2 AND branch_id=$3)`, session.CurrentCompanyID, session.User.ID, scope.BranchID).Scan(&hasScopes, &allowed); err != nil {
			return err
		}
		if hasScopes && !allowed {
			return identity.ErrForbidden
		}
	}
	if scope.WarehouseID != "" {
		var exists bool
		if scope.BranchID != "" {
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE company_id=$1 AND id=$2 AND is_active AND branch_id=$3)`, session.CurrentCompanyID, scope.WarehouseID, scope.BranchID).Scan(&exists); err != nil {
				return err
			}
		} else if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM warehouses WHERE company_id=$1 AND id=$2 AND is_active)`, session.CurrentCompanyID, scope.WarehouseID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return identity.ErrForbidden
		}
		var hasScopes, allowed bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM membership_warehouse_scopes WHERE company_id=$1 AND user_id=$2), EXISTS(SELECT 1 FROM membership_warehouse_scopes WHERE company_id=$1 AND user_id=$2 AND warehouse_id=$3)`, session.CurrentCompanyID, session.User.ID, scope.WarehouseID).Scan(&hasScopes, &allowed); err != nil {
			return err
		}
		if hasScopes && !allowed {
			return identity.ErrForbidden
		}
	}
	return nil
}

func nextCode(ctx context.Context, tx pgx.Tx, companyID string) (string, error) {
	for {
		var prefix string
		var digits int
		var number int64
		err := tx.QueryRow(ctx, `INSERT INTO company_product_sequences(company_id,prefix,digits,next_number) VALUES($1,'STK',6,2) ON CONFLICT(company_id) DO UPDATE SET next_number=company_product_sequences.next_number+1,updated_at=now() RETURNING prefix,digits,next_number-1`, companyID).Scan(&prefix, &digits, &number)
		if err != nil {
			return "", err
		}
		code := fmt.Sprintf("%s%0*d", prefix, digits, number)
		var exists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM products WHERE company_id=$1 AND code=$2)`, companyID, code).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
}

func writeAuditAndEvent(ctx context.Context, tx pgx.Tx, session identity.Session, eventType, eventName, entityID string, details map[string]any, meta identity.RequestMeta) error {
	auditID := uuid.NewString()
	eventID := uuid.NewString()
	if _, err := tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,$3,$4,'product',$5,$6,$7,$8,$9)`, auditID, session.CurrentCompanyID, session.User.ID, eventType, entityID, jsonBytes(details), meta.TraceID, meta.IP, truncate(meta.UserAgent, 512)); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO outbox_events(event_id,type,schema_version,company_id,trace_id,payload) VALUES($1,$2,1,$3,$4,$5)`, eventID, eventName, session.CurrentCompanyID, meta.TraceID, jsonBytes(map[string]any{"product_id": entityID, "event": eventName}))
	return err
}

func authorized(session identity.Session, permission string) bool {
	return identity.ValidateExternalActor(session) == nil && session.HasPermission(permission)
}

// lockProductCodeSequence makes manual product-code writes use the same
// company sequence lock as automatic code generation. This closes the race
// between an automatic candidate check and a concurrent manual insert.
func lockProductCodeSequence(ctx context.Context, tx pgx.Tx, companyID string) error {
	var nextNumber int64
	return tx.QueryRow(ctx, `INSERT INTO company_product_sequences(company_id,prefix,digits,next_number) VALUES($1,'STK',6,1) ON CONFLICT(company_id) DO UPDATE SET next_number=company_product_sequences.next_number,updated_at=now() RETURNING next_number`, companyID).Scan(&nextNumber)
}

func nullableID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func scopeUUIDValue(name, value string) (any, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return nil, fmt.Errorf("%w: %s kimliği geçersiz", identity.ErrValidation, name)
	}
	return value, nil
}

var decimalPattern = regexp.MustCompile(`^(\d+)(?:\.(\d{1,8}))?$`)

func normalizeDecimal(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", "."))
	match := decimalPattern.FindStringSubmatch(value)
	if match == nil || value == "0" {
		return ""
	}
	normalized := trimDecimalFractionZeros(value)
	if normalized == "0" {
		return ""
	}
	return normalized
}

func normalizePrice(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, " ", ""))
	comma, dot := strings.LastIndex(value, ","), strings.LastIndex(value, ".")
	switch {
	case comma >= 0 && dot >= 0 && comma > dot:
		value = strings.ReplaceAll(value, ".", "")
		value = strings.Replace(value, ",", ".", 1)
	case comma >= 0 && dot >= 0:
		value = strings.ReplaceAll(value, ",", "")
	case comma >= 0:
		value = strings.Replace(value, ",", ".", 1)
	}
	if value == "" {
		return "0"
	}
	match := decimalPattern.FindStringSubmatch(value)
	if match == nil {
		return ""
	}
	normalized := trimDecimalFractionZeros(value)
	if normalized == "" {
		return "0"
	}
	return normalized
}

func trimDecimalFractionZeros(value string) string {
	dot := strings.IndexByte(value, '.')
	if dot < 0 {
		return value
	}
	fraction := strings.TrimRight(value[dot+1:], "0")
	if fraction == "" {
		return value[:dot]
	}
	return value[:dot] + "." + fraction
}

func normalizeTaxType(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return "KDV"
	}
	switch value {
	case "KDV", "ÖTV", "OTV", "ÖİV", "OIV", "BSMV", "DAMGA", "KDV_TEVKIFAT", "MUAF", "İSTİSNA", "ISTISNA", "NOT_APPLICABLE", "YOK", "NONE":
		return value
	}
	return "KDV"
}

func normalizeTaxRate(value string) string {
	value = normalizePrice(value)
	if value == "" {
		return "0"
	}
	return value
}

func normalizeTaxProfile(input *TaxProfileInput, direction, legacyType, legacyRate string, legacyIncluded bool, legacyWithholdingCode, legacyWithholdingRate, legacyExemptionCode, legacyNote string) (*TaxProfileInput, error) {
	profile := TaxProfileInput{}
	if input != nil {
		profile = *input
	} else {
		profile = TaxProfileInput{
			TaxCode:         legacyType,
			Rate:            legacyRate,
			TaxIncluded:     legacyIncluded,
			WithholdingCode: legacyWithholdingCode,
			WithholdingRate: legacyWithholdingRate,
			ExemptionCode:   legacyExemptionCode,
			TaxNote:         legacyNote,
		}
		legacyTypeUpper := strings.ToUpper(strings.TrimSpace(legacyType))
		if legacyTypeUpper != "KDV_TEVKIFAT" {
			profile.WithholdingCode = ""
			profile.WithholdingRate = "0"
		}
		if legacyTypeUpper != "MUAF" && legacyTypeUpper != "İSTİSNA" && legacyTypeUpper != "ISTISNA" {
			profile.ExemptionCode = ""
		}
	}
	profile.Treatment = normalizeTaxTreatment(profile.Treatment, profile.TaxCode, profile.WithholdingCode, profile.ExemptionCode)
	if profile.Treatment == "" {
		return nil, fmt.Errorf("%w: %s vergi uygulaması geçersiz", identity.ErrValidation, direction)
	}
	profile.TaxCode = strings.ToUpper(strings.TrimSpace(profile.TaxCode))
	profile.TaxDefinitionID = strings.TrimSpace(profile.TaxDefinitionID)
	profile.TaxRateID = strings.TrimSpace(profile.TaxRateID)
	profile.WithholdingRuleID = strings.TrimSpace(profile.WithholdingRuleID)
	profile.ExemptionID = strings.TrimSpace(profile.ExemptionID)
	profile.WithholdingCode = strings.TrimSpace(profile.WithholdingCode)
	profile.ExemptionCode = strings.TrimSpace(profile.ExemptionCode)
	profile.TaxNote = strings.TrimSpace(profile.TaxNote)
	if profile.Rate == "" && input != nil {
		profile.Rate = "0"
	}
	var err error
	profile.Rate, err = normalizeProfileRate(profile.Rate)
	if err != nil {
		return nil, fmt.Errorf("%w: %s vergi oranı geçersiz", identity.ErrValidation, direction)
	}
	profile.WithholdingRate, err = normalizeProfileRate(profile.WithholdingRate)
	if err != nil {
		return nil, fmt.Errorf("%w: %s tevkifat oranı geçersiz", identity.ErrValidation, direction)
	}
	if input == nil && profile.WithholdingCode != "" && profile.WithholdingRate == "0" {
		profile.WithholdingRate = normalizeTaxRate(legacyWithholdingRate)
	}
	if input == nil && profile.Rate == "0" {
		profile.Rate = normalizeTaxRate(legacyRate)
	}
	for _, ref := range []struct {
		name  string
		value string
	}{
		{"tax_definition_id", profile.TaxDefinitionID},
		{"tax_rate_id", profile.TaxRateID},
		{"withholding_rule_id", profile.WithholdingRuleID},
		{"exemption_id", profile.ExemptionID},
	} {
		if ref.value != "" {
			if _, err := uuid.Parse(ref.value); err != nil {
				return nil, fmt.Errorf("%w: %s geçersiz", identity.ErrValidation, ref.name)
			}
		}
	}
	if profile.Treatment == TaxTreatmentWithholding && profile.WithholdingCode == "" && profile.WithholdingRuleID == "" {
		return nil, taxValidation("WITHHOLDING_DEFINITION_REQUIRED", direction+" vergi profili tevkifat kuralı gerektirir")
	}
	if profile.Treatment == TaxTreatmentExempt && profile.ExemptionCode == "" && profile.ExemptionID == "" {
		return nil, taxValidation("EXEMPTION_DEFINITION_REQUIRED", direction+" vergi profili istisna tanımı gerektirir")
	}
	if profile.Treatment == TaxTreatmentNotApplicable && (profile.Rate != "0" || profile.WithholdingRate != "0") {
		return nil, fmt.Errorf("%w: %s vergi profili uygulanmıyorsa oranlar sıfır olmalıdır", identity.ErrValidation, direction)
	}
	if input != nil {
		switch profile.Treatment {
		case TaxTreatmentStandard:
			// A product card may enter a company-specific/manual KDV rate.
			// Catalog-backed rates still carry TaxRateID; manual rates are
			// authoritative in the product profile and are identified by the
			// explicit tax code.
			if profile.TaxRateID == "" && strings.TrimSpace(profile.TaxCode) == "" {
				return nil, taxValidation("TAX_RATE_REQUIRED", direction+" KDV oranı gereklidir")
			}
		case TaxTreatmentWithholding:
			if profile.TaxRateID == "" || profile.WithholdingRuleID == "" {
				return nil, taxValidation("WITHHOLDING_DEFINITION_REQUIRED", direction+" KDV oranı ve tevkifat tanımı gereklidir")
			}
		case TaxTreatmentExempt:
			if profile.ExemptionID == "" {
				return nil, taxValidation("EXEMPTION_DEFINITION_REQUIRED", direction+" istisna/muafiyet tanımı gereklidir")
			}
		case TaxTreatmentNotApplicable:
			if profile.TaxRateID != "" || profile.WithholdingRuleID != "" || profile.ExemptionID != "" || profile.Rate != "0" || profile.WithholdingRate != "0" {
				return nil, taxValidation("INVALID_TAX_PROFILE", direction+" vergi uygulanmaz profilinde vergi referansı bulunamaz")
			}
		}
	}
	return &profile, nil
}

func normalizeTaxTreatment(value, taxCode, withholdingCode, exemptionCode string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		taxCode = strings.ToUpper(strings.TrimSpace(taxCode))
		if strings.TrimSpace(withholdingCode) != "" || taxCode == "KDV_TEVKIFAT" {
			return TaxTreatmentWithholding
		}
		if strings.TrimSpace(exemptionCode) != "" || taxCode == "MUAF" || taxCode == "İSTİSNA" || taxCode == "ISTISNA" {
			return TaxTreatmentExempt
		}
		if taxCode == "YOK" || taxCode == "NONE" || taxCode == TaxTreatmentNotApplicable {
			return TaxTreatmentNotApplicable
		}
		return TaxTreatmentStandard
	}
	switch value {
	case TaxTreatmentStandard, TaxTreatmentWithholding, TaxTreatmentExempt, TaxTreatmentNotApplicable:
		return value
	default:
		return ""
	}
}

func normalizeProfileRate(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", "."))
	if value == "" {
		return "0", nil
	}
	if !decimalPattern.MatchString(value) {
		return "", errors.New("invalid tax rate")
	}
	ratio, ok := new(big.Rat).SetString(value)
	if !ok || ratio.Sign() < 0 || ratio.Cmp(big.NewRat(100, 1)) > 0 {
		return "", errors.New("invalid tax rate")
	}
	normalized := strings.TrimRight(strings.TrimRight(ratio.FloatString(8), "0"), ".")
	if normalized == "" {
		return "0", nil
	}
	return normalized, nil
}

func upsertTaxProfiles(ctx context.Context, tx pgx.Tx, companyID, productID string, purchase, sales *TaxProfileInput) error {
	for direction, profile := range map[string]*TaxProfileInput{"PURCHASE": purchase, "SALES": sales} {
		if profile == nil {
			return fmt.Errorf("%w: %s vergi profili eksik", identity.ErrValidation, direction)
		}
		if profile.Treatment == "" {
			return fmt.Errorf("%w: %s vergi profili geçersiz", identity.ErrValidation, direction)
		}
		if err := validateTaxProfileReferences(ctx, tx, companyID, profile); err != nil {
			return err
		}
		if profile.TaxRateID != "" && (profile.Rate == "" || profile.Rate == "0") {
			if err := tx.QueryRow(ctx, `SELECT rate::text FROM tax_rates WHERE company_id=$1 AND id=$2`, companyID, profile.TaxRateID).Scan(&profile.Rate); err != nil {
				return err
			}
			profile.Rate = normalizeTaxRate(profile.Rate)
		}
		_, err := tx.Exec(ctx, `INSERT INTO product_tax_profiles(company_id,product_id,direction,treatment,tax_definition_id,tax_rate_id,tax_code,rate,tax_included,withholding_rule_id,withholding_code,withholding_rate,withholding_numerator,withholding_denominator,exemption_id,exemption_code,tax_note)
VALUES($1,$2,$3,$4,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8,$9,NULLIF($10,'')::uuid,$11,$12,$13,$14,NULLIF($15,'')::uuid,$16,$17)
ON CONFLICT(company_id,product_id,direction) DO UPDATE SET treatment=excluded.treatment,tax_definition_id=excluded.tax_definition_id,tax_rate_id=excluded.tax_rate_id,tax_code=excluded.tax_code,rate=excluded.rate,tax_included=excluded.tax_included,withholding_rule_id=excluded.withholding_rule_id,withholding_code=excluded.withholding_code,withholding_rate=excluded.withholding_rate,withholding_numerator=excluded.withholding_numerator,withholding_denominator=excluded.withholding_denominator,exemption_id=excluded.exemption_id,exemption_code=excluded.exemption_code,tax_note=excluded.tax_note,updated_at=now(),version=product_tax_profiles.version+1`, companyID, productID, direction, profile.Treatment, profile.TaxDefinitionID, profile.TaxRateID, profile.TaxCode, profile.Rate, profile.TaxIncluded, profile.WithholdingRuleID, profile.WithholdingCode, profile.WithholdingRate, profile.WithholdingNumerator, profile.WithholdingDenominator, profile.ExemptionID, profile.ExemptionCode, profile.TaxNote)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `DELETE FROM product_tax_profile_components WHERE company_id=$1 AND product_id=$2 AND direction=$3`, companyID, productID, direction); err != nil {
			return err
		}
		for index, component := range profile.Components {
			rateID := component.TaxRateID
			if rateID == "" {
				rateID = component.RateID
			}
			if component.TaxDefinitionID == "" {
				return fmt.Errorf("%w: ek vergi tanımı gereklidir", identity.ErrValidation)
			}
			if component.CalculationType == "" {
				component.CalculationType = "PERCENTAGE"
			}
			component.CalculationType = strings.ToUpper(strings.TrimSpace(component.CalculationType))
			if !containsTaxCalculationType(component.CalculationType) {
				return fmt.Errorf("%w: ek vergi hesaplama yöntemi geçersiz", identity.ErrValidation)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO product_tax_profile_components(company_id,product_id,direction,sequence,tax_definition_id,tax_rate_id,calculation_type,included_in_tax_base,metadata) VALUES($1,$2,$3,$4,$5::uuid,NULLIF($6,'')::uuid,$7,$8,$9)`, companyID, productID, direction, index+1, component.TaxDefinitionID, rateID, component.CalculationType, component.IncludedInTaxBase, jsonBytes(component.Metadata)); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsTaxCalculationType(value string) bool {
	return value == "PERCENTAGE" || value == "FIXED_AMOUNT" || value == "QUANTITY_BASED"
}

func validateTaxProfileReferences(ctx context.Context, tx pgx.Tx, companyID string, profile *TaxProfileInput) error {
	if profile.TaxDefinitionID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_definitions WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, profile.TaxDefinitionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return taxValidation("TAX_DEFINITION_INACTIVE", "Vergi tanımı bulunamadı veya pasif.")
		}
	}
	if profile.TaxRateID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_rates r JOIN tax_definitions d ON d.company_id=r.company_id AND d.id=r.tax_definition_id WHERE r.company_id=$1 AND r.id=$2 AND ($3='' OR r.tax_definition_id=$3::uuid) AND d.is_active AND r.valid_from <= CURRENT_DATE AND (r.valid_to IS NULL OR r.valid_to >= CURRENT_DATE))`, companyID, profile.TaxRateID, profile.TaxDefinitionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return taxValidation("TAX_RATE_NOT_EFFECTIVE", "Vergi oranı bulunamadı, pasif veya geçerlilik tarihi dışında.")
		}
	}
	if profile.WithholdingRuleID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_withholding_rules WHERE company_id=$1 AND id=$2 AND is_active AND valid_from <= CURRENT_DATE AND (valid_to IS NULL OR valid_to >= CURRENT_DATE))`, companyID, profile.WithholdingRuleID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return taxValidation("TAX_DEFINITION_INACTIVE", "Tevkifat tanımı bulunamadı, pasif veya geçerlilik tarihi dışında.")
		}
	}
	if profile.ExemptionID != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_exemptions WHERE company_id=$1 AND id=$2 AND is_active AND valid_from <= CURRENT_DATE AND (valid_to IS NULL OR valid_to >= CURRENT_DATE))`, companyID, profile.ExemptionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return taxValidation("TAX_DEFINITION_INACTIVE", "İstisna/muafiyet tanımı bulunamadı, pasif veya geçerlilik tarihi dışında.")
		}
	}
	if profile.Treatment == TaxTreatmentWithholding && profile.TaxRateID == "" {
		return fmt.Errorf("%w: KDV tevkifatı için KDV oranı gereklidir", identity.ErrValidation)
	}
	if profile.Treatment == TaxTreatmentExempt && profile.ExemptionID == "" && profile.ExemptionCode == "" {
		return fmt.Errorf("%w: istisna veya muafiyet tanımı gereklidir", identity.ErrValidation)
	}
	for _, component := range profile.Components {
		if component.TaxDefinitionID == "" {
			return fmt.Errorf("%w: ek vergi tanımı gereklidir", identity.ErrValidation)
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_definitions WHERE company_id=$1 AND id=$2 AND is_active)`, companyID, component.TaxDefinitionID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("%w: ek vergi tanımı bulunamadı veya pasif", identity.ErrValidation)
		}
		rateID := component.TaxRateID
		if rateID == "" {
			rateID = component.RateID
		}
		if rateID != "" {
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tax_rates WHERE company_id=$1 AND id=$2 AND tax_definition_id=$3 AND valid_from <= CURRENT_DATE AND (valid_to IS NULL OR valid_to >= CURRENT_DATE))`, companyID, rateID, component.TaxDefinitionID).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: ek vergi oranı tanımla eşleşmiyor", identity.ErrValidation)
			}
		}
	}
	return nil
}

func taxProfilesAudit(purchase, sales *TaxProfileInput) map[string]any {
	return map[string]any{
		"purchase": taxProfileAudit(purchase),
		"sales":    taxProfileAudit(sales),
	}
}

func taxProfileAudit(profile *TaxProfileInput) map[string]any {
	if profile == nil {
		return map[string]any{}
	}
	return map[string]any{
		"treatment":        profile.Treatment,
		"tax_code":         profile.TaxCode,
		"rate":             profile.Rate,
		"tax_included":     profile.TaxIncluded,
		"withholding_code": profile.WithholdingCode,
		"withholding_rate": profile.WithholdingRate,
		"exemption_code":   profile.ExemptionCode,
	}
}

func normalizeSearchQuery(value string) string {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		var builder strings.Builder
		for _, r := range token {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				builder.WriteRune(r)
			}
		}
		if builder.Len() > 0 {
			result = append(result, builder.String()+":*")
		}
	}
	return strings.Join(result, " & ")
}

func encodeCursor(name, id string) string {
	value, _ := json.Marshal([]string{name, id})
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeCursor(value string) (string, string, error) {
	if strings.TrimSpace(value) == "" {
		return "", "00000000-0000-0000-0000-000000000000", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", err
	}
	var parts []string
	if err = json.Unmarshal(raw, &parts); err != nil || len(parts) != 2 || uuid.Validate(parts[1]) != nil {
		return "", "", errors.New("invalid cursor")
	}
	return parts[0], parts[1], nil
}

func mapConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "products_company_code_unique":
			return fmt.Errorf("%w: stok kodu bu firmada zaten kullanılıyor", identity.ErrValidation)
		case "product_barcodes_company_id_barcode_key", "product_primary_barcode_unique", "product_variant_primary_barcode_unique":
			return variantValidation("VARIANT_BARCODE_DUPLICATE", "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor.")
		case "product_variants_company_id_variant_code_key":
			return variantValidation("VARIANT_CODE_DUPLICATE", "Varyant SKU bu firmada zaten kullanılıyor.")
		case "product_base_unit_unique":
			return fmt.Errorf("%w: yalnızca bir temel birim olabilir", identity.ErrValidation)
		case "product_variants_product_signature_unique":
			return variantValidation("VARIANT_DUPLICATE", "Aynı varyant kombinasyonu zaten mevcut")
		case "product_variants_signature_check", "product_variants_code_format_check":
			return variantValidation("VARIANT_IDENTITY_INVALID", "Varyant kimliği geçersiz")
		}
		message := strings.ToLower(pgErr.Message)
		if pgErr.Code == "55000" && (strings.Contains(message, "variant") || strings.Contains(message, "varyant")) {
			return variantValidation("VARIANT_STATE_CONFLICT", "Varyant kimliği veya modu mevcut stok geçmişi nedeniyle değiştirilemez")
		}
		if pgErr.Code == "23514" && (strings.Contains(message, "variant") || strings.Contains(message, "varyant")) {
			return variantValidation(variantConstraintCode(message), "Varyant bilgileri ürün kurallarıyla uyuşmuyor")
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("%w: ürün kartındaki kod veya referans zaten kullanılıyor", identity.ErrValidation)
	}
	return err
}

func variantConstraintCode(message string) string {
	switch {
	case strings.Contains(message, "variant_required"):
		return "VARIANT_REQUIRED"
	case strings.Contains(message, "variant_inactive"):
		return "VARIANT_INACTIVE"
	case strings.Contains(message, "must be physical"), strings.Contains(message, "variant_product_must_be_physical"):
		return "VARIANT_PRODUCT_MUST_BE_PHYSICAL"
	case strings.Contains(message, "barcode"):
		return "VARIANT_BARCODE_INVALID"
	case strings.Contains(message, "variant values"):
		return "VARIANT_VALUES_INVALID"
	default:
		return "VARIANT_RULE_VIOLATION"
	}
}

func jsonBytes(value any) []byte {
	if value == nil {
		return []byte("{}")
	}
	data, _ := json.Marshal(value)
	return data
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
