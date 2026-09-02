package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// inventoryVariant is the small, read-only projection inventory needs from
// the catalog. Inventory deliberately does not own product-variant writes;
// it only validates the stock identity and decorates read models.
type inventoryVariant struct {
	ID      string
	Product string
	Code    string
	Display map[string]any
}

// validateInventoryVariantTx is called inside the command transaction. A
// product with at least one variant is variant-enabled for inventory purposes:
// a parent/product-level ledger row is never a valid stock identity. The
// FOR SHARE lock makes an active variant check stable while the movement or
// transfer command is posted.
func validateInventoryVariantTx(ctx context.Context, q txDB, companyID, productID, variantID string) (inventoryVariant, error) {
	companyID = strings.TrimSpace(companyID)
	productID = strings.TrimSpace(productID)
	variantID = strings.TrimSpace(variantID)

	var kind string
	var hasVariants bool
	if err := q.QueryRow(ctx, `SELECT kind::text,variants_enabled OR EXISTS(
		SELECT 1 FROM product_variants pv
		WHERE pv.company_id=p.company_id AND pv.product_id=p.id
	) FROM products p WHERE p.company_id=$1 AND p.id=$2`, companyID, productID).Scan(&kind, &hasVariants); errors.Is(err, pgx.ErrNoRows) {
		return inventoryVariant{}, ErrNotFound
	} else if err != nil {
		return inventoryVariant{}, err
	}
	if kind != "PHYSICAL" {
		return inventoryVariant{}, fmt.Errorf("%w: yalnız fiziksel ürünlerde stok varyantı kullanılabilir", ErrVariantProductMismatch)
	}
	if variantID == "" {
		if hasVariants {
			return inventoryVariant{}, codeError(ErrVariantRequired.Error(), ErrVariantRequired, "varyantlı üründe aktif varyant seçilmelidir")
		}
		return inventoryVariant{}, nil
	}

	var item inventoryVariant
	var active bool
	var attributes []byte
	if err := q.QueryRow(ctx, `SELECT pv.id,pv.product_id,pv.variant_code,COALESCE((SELECT jsonb_object_agg(d.code,o.name) FROM product_variant_values vv JOIN variant_definitions d ON d.company_id=vv.company_id AND d.id=vv.definition_id JOIN variant_definition_options o ON o.company_id=vv.company_id AND o.definition_id=vv.definition_id AND o.id=vv.option_id WHERE vv.company_id=pv.company_id AND vv.variant_id=pv.id),'{}'::jsonb),pv.is_active
		FROM product_variants pv
		WHERE pv.company_id=$1 AND pv.id=$2
		FOR SHARE`, companyID, variantID).Scan(&item.ID, &item.Product, &item.Code, &attributes, &active); errors.Is(err, pgx.ErrNoRows) {
		return inventoryVariant{}, codeError(ErrVariantProductMismatch.Error(), ErrVariantProductMismatch, "varyant firma veya ürün ile eşleşmiyor")
	} else if err != nil {
		return inventoryVariant{}, err
	}
	if item.Product != productID {
		return inventoryVariant{}, codeError(ErrVariantProductMismatch.Error(), ErrVariantProductMismatch, "varyant firma veya ürün ile eşleşmiyor")
	}
	if !active {
		return inventoryVariant{}, codeError(ErrVariantInactive.Error(), ErrVariantInactive, "pasif varyant stok işleminde seçilemez")
	}
	item.Display = decodeVariantDisplay(attributes)
	return item, nil
}

func decodeVariantDisplay(attributes []byte) map[string]any {
	if len(attributes) == 0 {
		return nil
	}
	var display map[string]any
	if err := json.Unmarshal(attributes, &display); err != nil {
		return nil
	}
	return display
}

func (s *Service) variantPresentations(ctx context.Context, companyID string, ids []string) (map[string]inventoryVariant, error) {
	result := make(map[string]inventoryVariant, len(ids))
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT pv.id,pv.product_id,pv.variant_code,COALESCE((SELECT jsonb_object_agg(d.code,o.name) FROM product_variant_values vv JOIN variant_definitions d ON d.company_id=vv.company_id AND d.id=vv.definition_id JOIN variant_definition_options o ON o.company_id=vv.company_id AND o.definition_id=vv.definition_id AND o.id=vv.option_id WHERE vv.company_id=pv.company_id AND vv.variant_id=pv.id),'{}'::jsonb)
		FROM product_variants pv WHERE pv.company_id=$1 AND pv.id=ANY($2::uuid[])`, companyID, unique)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item inventoryVariant
		var attributes []byte
		if err = rows.Scan(&item.ID, &item.Product, &item.Code, &attributes); err != nil {
			return nil, err
		}
		item.Display = decodeVariantDisplay(attributes)
		result[item.ID] = item
	}
	return result, rows.Err()
}

func (s *Service) enrichStockCountLines(ctx context.Context, companyID string, lines []StockCountLine) error {
	ids := make([]string, 0, len(lines))
	for _, line := range lines {
		if line.VariantID != nil {
			ids = append(ids, *line.VariantID)
		}
	}
	presentations, err := s.variantPresentations(ctx, companyID, ids)
	if err != nil {
		return err
	}
	for index := range lines {
		if lines[index].VariantID == nil {
			continue
		}
		if item, ok := presentations[*lines[index].VariantID]; ok {
			lines[index].VariantCode = item.Code
			lines[index].VariantDisplay = item.Display
		}
	}
	return nil
}
