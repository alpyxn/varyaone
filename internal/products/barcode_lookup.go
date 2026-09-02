package products

import (
	"context"
	"errors"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/jackc/pgx/v5"
)

// BarcodeMatch is the catalog line-entry a scanned or typed barcode resolves
// to. UnitCode is the product's base unit, so a caller building a commercial
// line has everything it needs without a second lookup.
type BarcodeMatch struct {
	ProductID     string  `json:"product_id"`
	ProductCode   string  `json:"product_code"`
	ProductName   string  `json:"product_name"`
	VariantID     *string `json:"variant_id,omitempty"`
	VariantCode   *string `json:"variant_code,omitempty"`
	UnitCode      string  `json:"unit_code"`
	ProductActive bool    `json:"product_active"`
	VariantActive *bool   `json:"variant_active,omitempty"`
}

// ErrBarcodeNotFound means the barcode is not registered on any product or
// variant in this company.
var ErrBarcodeNotFound = errors.New("barcode not found")

// ResolveBarcode looks up a scanned or typed barcode the same way the stock
// count engine does (product_barcodes, company-scoped, unique per company),
// so a commercial document's line entry and a stock count use one barcode
// table and one resolution rule rather than two.
func (s *Service) ResolveBarcode(ctx context.Context, session identity.Session, barcode string) (BarcodeMatch, error) {
	if identity.ValidateExternalActor(session) != nil {
		return BarcodeMatch{}, identity.ErrForbidden
	}
	barcode = strings.TrimSpace(barcode)
	if barcode == "" {
		return BarcodeMatch{}, ErrBarcodeNotFound
	}
	var match BarcodeMatch
	var variantID, variantCode *string
	var variantActive *bool
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.code, p.name, p.is_active,
		       pb.variant_id::text, v.variant_code, v.is_active,
		       COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=p.company_id AND pu.product_id=p.id AND pu.is_base LIMIT 1), 'ADET')
		  FROM product_barcodes pb
		  JOIN products p ON p.company_id=pb.company_id AND p.id=pb.product_id
		  LEFT JOIN product_variants v ON v.company_id=pb.company_id AND v.id=pb.variant_id
		 WHERE pb.company_id=$1 AND pb.barcode=$2`,
		session.CurrentCompanyID, barcode).Scan(
		&match.ProductID, &match.ProductCode, &match.ProductName, &match.ProductActive,
		&variantID, &variantCode, &variantActive, &match.UnitCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return BarcodeMatch{}, ErrBarcodeNotFound
	}
	if err != nil {
		return BarcodeMatch{}, err
	}
	if variantID != nil && *variantID != "" {
		match.VariantID, match.VariantCode, match.VariantActive = variantID, variantCode, variantActive
	}
	return match, nil
}
