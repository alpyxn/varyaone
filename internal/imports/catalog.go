package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type catalogImportRow struct {
	Number int
	Values map[string]string
}

// commitCatalogImport applies the catalog batch in one transaction. IDs are
// derived from the immutable import job and row number so a retry after a
// metadata update failure cannot create a second record.
func (s *Service) commitCatalogImport(ctx context.Context, companyID, actorID, jobID, entity string, allowOpeningStock bool) error {
	rows, err := s.validImportRows(ctx, companyID, jobID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var createdBy string
	if err := tx.QueryRow(ctx, `SELECT created_by::text FROM import_jobs WHERE company_id=$1 AND id=$2`, companyID, jobID).Scan(&createdBy); err != nil {
		return err
	}
	for _, row := range rows {
		if err := contextError(ctx); err != nil {
			return err
		}
		var rowErr error
		switch entity {
		case string(dataexchange.EntityProduct):
			rowErr = s.commitImportedProductRow(ctx, tx, companyID, actorID, jobID, row, allowOpeningStock)
		case string(dataexchange.EntityVariant):
			rowErr = commitImportedVariantRow(ctx, tx, companyID, actorID, jobID, row)
		case string(dataexchange.EntityBarcode):
			rowErr = commitBarcodeRow(ctx, tx, companyID, jobID, row)
		case string(dataexchange.EntityWarehouse):
			rowErr = commitImportedWarehouseRow(ctx, tx, companyID, actorID, jobID, row)
		case string(dataexchange.EntityPriceList):
			rowErr = commitPriceRow(ctx, tx, companyID, jobID, row)
		case string(dataexchange.EntityParty):
			rowErr = commitImportedPartyRow(ctx, tx, companyID, actorID, jobID, row)
		default:
			return fmt.Errorf("desteklenmeyen katalog aktarım türü: %s", entity)
		}
		if rowErr != nil {
			return fmt.Errorf("%d numaralı aktarım satırı: %w", row.Number, rowErr)
		}
	}
	return tx.Commit(ctx)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (s *Service) validImportRows(ctx context.Context, companyID, jobID string) ([]catalogImportRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT row_number,values FROM import_row_results WHERE company_id=$1 AND import_job_id=$2 AND status IN ('VALID','WARNING') ORDER BY row_number`, companyID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]catalogImportRow, 0)
	for rows.Next() {
		var item catalogImportRow
		var raw []byte
		if err := rows.Scan(&item.Number, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &item.Values); err != nil {
			return nil, fmt.Errorf("aktarım satır değerleri geçersiz")
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func stableImportID(jobID string, rowNumber int) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("varya-import:%s:%d", jobID, rowNumber))).String()
}

func normalizedValue(values map[string]string, key string) string {
	return strings.TrimSpace(values[key])
}

func refreshImportedProductSearch(ctx context.Context, tx pgx.Tx, companyID, productID string) error {
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

func commitBarcodeRow(ctx context.Context, tx pgx.Tx, companyID, jobID string, row catalogImportRow) error {
	productCode := strings.ToUpper(normalizedValue(row.Values, "product_code"))
	barcode := normalizedValue(row.Values, "barcode")
	variantCode := strings.ToUpper(normalizedValue(row.Values, "variant_code"))
	var productID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM products WHERE company_id=$1 AND code=$2`, companyID, productCode).Scan(&productID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("stok kartı bulunamadı")
		}
		return err
	}
	variantID := ""
	if variantCode != "" {
		if err := tx.QueryRow(ctx, `SELECT id::text FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3`, companyID, productID, variantCode).Scan(&variantID); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("varyant bulunamadı")
			}
			return err
		}
	}
	id := stableImportID(jobID, row.Number)
	var existingID, existingProductID, existingVariantID string
	err := tx.QueryRow(ctx, `SELECT id::text,product_id::text,COALESCE(variant_id::text,'') FROM product_barcodes WHERE company_id=$1 AND barcode=$2`, companyID, barcode).Scan(&existingID, &existingProductID, &existingVariantID)
	if err == nil {
		if existingProductID != productID || existingVariantID != variantID {
			return fmt.Errorf("%w: barkod başka bir stok kartı veya varyant için zaten kayıtlı", ErrIdentityConflict)
		}
		return nil
	}
	if err != nil && err != pgx.ErrNoRows {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type) VALUES($1,$2,$3,$4,$5,'EAN') ON CONFLICT (company_id,id) DO NOTHING`, id, companyID, productID, nullable(variantID), barcode)
	return err
}

func commitPriceRow(ctx context.Context, tx pgx.Tx, companyID, jobID string, row catalogImportRow) error {
	priceListCode := strings.ToUpper(normalizedValue(row.Values, "price_list_code"))
	productCode := strings.ToUpper(normalizedValue(row.Values, "product_code"))
	variantCode := strings.ToUpper(normalizedValue(row.Values, "variant_code"))
	price, err := dataexchange.ParseQuantity(normalizedValue(row.Values, "price"))
	if err != nil || price.IsNegative() || price.Scale() > 8 {
		return fmt.Errorf("fiyat geçersiz")
	}
	var listID string
	if err = tx.QueryRow(ctx, `SELECT id::text FROM price_lists WHERE company_id=$1 AND code=$2`, companyID, priceListCode).Scan(&listID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("fiyat listesi bulunamadı")
		}
		return err
	}
	var itemID, variantID string
	if productCode == "" {
		return fmt.Errorf("stok kartı zorunludur")
	}
	if err = tx.QueryRow(ctx, `SELECT id::text FROM products WHERE company_id=$1 AND code=$2`, companyID, productCode).Scan(&itemID); err != nil {
		if err == pgx.ErrNoRows {
			return fmt.Errorf("stok kartı bulunamadı")
		}
		return err
	}
	if variantCode != "" {
		if err = tx.QueryRow(ctx, `SELECT id::text FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3`, companyID, itemID, variantCode).Scan(&variantID); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("varyant bulunamadı")
			}
			return err
		}
	}
	id := stableImportID(jobID, row.Number)
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_list_entries WHERE company_id=$1 AND id=$2)`, companyID, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM price_list_entries WHERE company_id=$1 AND price_list_id=$2 AND item_id=$3 AND variant_id IS NOT DISTINCT FROM $4::uuid AND valid_from <= CURRENT_DATE AND (valid_to IS NULL OR valid_to >= CURRENT_DATE))`, companyID, listID, itemID, nullable(variantID)).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("fiyat dönemi mevcut bir kayıtla çakışıyor")
	}
	_, err = tx.Exec(ctx, `INSERT INTO price_list_entries(id,company_id,price_list_id,item_id,variant_id,valid_from,unit_price) VALUES($1,$2,$3,$4,$5,CURRENT_DATE,$6)`, id, companyID, listID, itemID, nullable(variantID), price.String())
	return err
}
