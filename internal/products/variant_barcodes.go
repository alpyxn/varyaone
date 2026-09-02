package products

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// VariantBarcodeReplacementInput is a full replacement command. An empty
// barcode list intentionally removes every barcode from the variant.
type VariantBarcodeReplacementInput struct {
	Barcodes []BarcodeInput `json:"barcodes"`
}

// ReplaceVariantBarcodes atomically replaces all barcodes owned by a variant.
// Barcode identity is independent from stock history, so this command remains
// available after the variant has posted stock movements. The variant row is
// the optimistic-concurrency resource and is locked before any child rows are
// changed.
func (s *Service) ReplaceVariantBarcodes(ctx context.Context, session identity.Session, productID, variantID string, version int64, input VariantBarcodeReplacementInput, meta identity.RequestMeta) (Variant, error) {
	if !authorized(session, "product.variant.manage") {
		return Variant{}, identity.ErrForbidden
	}
	if version < 1 {
		return Variant{}, fmt.Errorf("%w: varyant barkodları için geçerli sürüm gereklidir", identity.ErrValidation)
	}
	normalized, err := normalizeVariantBarcodeReplacement(input.Barcodes)
	if err != nil {
		return Variant{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Variant{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Keep the same product-first lock order as ordinary variant updates, then
	// lock the exact company/product/variant identity to prevent cross-company
	// or cross-product access.
	if _, err = lockVariantProduct(ctx, tx, session.CurrentCompanyID, productID); err != nil {
		return Variant{}, err
	}
	var currentVersion int64
	if err = tx.QueryRow(ctx, `SELECT version FROM product_variants WHERE company_id=$1 AND product_id=$2 AND id=$3 FOR UPDATE`, session.CurrentCompanyID, productID, variantID).Scan(&currentVersion); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Variant{}, identity.ErrForbidden
		}
		return Variant{}, err
	}
	if currentVersion != version {
		return Variant{}, identity.ErrConflict
	}

	if barcode, err := findVariantBarcodeConflict(ctx, tx, session.CurrentCompanyID, productID, variantID, normalized); err != nil {
		return Variant{}, err
	} else if barcode != "" {
		return Variant{}, variantBarcodeConflict(barcode)
	}

	if err = replaceVariantBarcodesStrict(ctx, tx, session.CurrentCompanyID, productID, variantID, normalized); err != nil {
		return Variant{}, mapVariantBarcodeConstraint(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE product_variants SET updated_at=now(),version=version+1 WHERE company_id=$1 AND product_id=$2 AND id=$3 AND version=$4`, session.CurrentCompanyID, productID, variantID, version)
	if err != nil {
		return Variant{}, mapVariantBarcodeConstraint(err)
	}
	if tag.RowsAffected() == 0 {
		return Variant{}, identity.ErrConflict
	}
	if err = writeAuditAndEvent(ctx, tx, session, "PRODUCT_VARIANT_BARCODES_REPLACED", "product.variant_barcodes.replaced", variantID, map[string]any{"product_id": productID, "barcode_count": len(normalized)}, meta); err != nil {
		return Variant{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Variant{}, err
	}
	return s.getVariant(ctx, session.CurrentCompanyID, variantID)
}

func normalizeVariantBarcodeReplacement(input []BarcodeInput) ([]BarcodeInput, error) {
	items := make([]BarcodeInput, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	primary := 0
	for _, item := range input {
		item.Barcode = strings.TrimSpace(item.Barcode)
		if item.Barcode == "" {
			return nil, variantValidation("VARIANT_BARCODE_REQUIRED", "Varyant barkodu boş bırakılamaz")
		}
		item.BarcodeType = strings.ToUpper(strings.TrimSpace(item.BarcodeType))
		if item.BarcodeType == "" {
			item.BarcodeType = "EAN"
		}
		if _, ok := seen[item.Barcode]; ok {
			return nil, variantValidation("VARIANT_BARCODE_LIST_DUPLICATE", "Aynı varyantta aynı barkod birden fazla kullanılamaz.")
		}
		seen[item.Barcode] = struct{}{}
		if item.IsPrimary {
			primary++
		}
		items = append(items, item)
	}
	if primary > 1 {
		return nil, variantValidation("VARIANT_PRIMARY_BARCODE_DUPLICATE", "Yalnızca bir varyant barkodu ana barkod olabilir")
	}
	if len(items) > 0 && primary == 0 {
		items[0].IsPrimary = true
	}
	return items, nil
}

func findVariantBarcodeConflict(ctx context.Context, tx pgx.Tx, companyID, productID, variantID string, inputs []BarcodeInput) (string, error) {
	if len(inputs) == 0 {
		return "", nil
	}
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		values = append(values, input.Barcode)
	}
	var barcode string
	err := tx.QueryRow(ctx, `SELECT barcode FROM product_barcodes WHERE company_id=$1 AND barcode=ANY($2::text[]) AND NOT (product_id=$3 AND variant_id IS NOT DISTINCT FROM $4) ORDER BY barcode LIMIT 1`, companyID, values, productID, variantID).Scan(&barcode)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return barcode, err
}

func variantBarcodeConflict(barcode string) error {
	return variantValidation("VARIANT_BARCODE_DUPLICATE", fmt.Sprintf("Barkod %q başka bir ürün veya varyantta kullanılıyor. Şirket içinde kullanılmamış bir barkod girin.", barcode))
}

func replaceVariantBarcodesStrict(ctx context.Context, tx pgx.Tx, companyID, productID, variantID string, inputs []BarcodeInput) error {
	if _, err := tx.Exec(ctx, `DELETE FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id=$3`, companyID, productID, variantID); err != nil {
		return err
	}
	for _, barcode := range inputs {
		if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), companyID, productID, variantID, barcode.Barcode, barcode.BarcodeType, barcode.IsPrimary); err != nil {
			return err
		}
	}
	return nil
}

func mapVariantBarcodeConstraint(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "product_barcodes_company_id_barcode_key", "product_primary_barcode_unique", "product_variant_primary_barcode_unique":
			return variantValidation("VARIANT_BARCODE_DUPLICATE", "Bu barkod firmada başka bir ürün veya varyantta kullanılıyor.")
		}
	}
	return mapConstraint(err)
}
