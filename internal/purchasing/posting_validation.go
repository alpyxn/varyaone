package purchasing

import (
	"context"
	"errors"
	"strings"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type purchasePostingLine struct {
	LineNo      int
	LineType    string
	ProductID   string
	VariantID   string
	WarehouseID string
}

// validatePurchasePostingMastersTx is deliberately called after the draft
// has been locked and before any allocation, ledger or stock effect. Draft
// reads may still show a now-inactive master, but a posting command must use
// current company/branch/warehouse eligibility and current active flags.
func validatePurchasePostingMastersTx(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID, supplierID, headerWarehouseID string, lines []purchasePostingLine) error {
	var supplier, active bool
	if err := q.QueryRow(ctx, `SELECT is_supplier,is_active FROM parties WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, supplierID).Scan(&supplier, &active); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	} else if err != nil {
		return err
	}
	if !supplier {
		return identity.ErrForbidden
	}
	if !active {
		return ErrSupplierInactive
	}

	if strings.TrimSpace(headerWarehouseID) != "" {
		if err := validatePurchaseWarehouseTx(ctx, q, session, branchID, headerWarehouseID); err != nil {
			return err
		}
	}
	for _, line := range lines {
		lineType := normalizePurchaseLineType(line.LineType)
		if strings.TrimSpace(line.ProductID) == "" {
			return validation("satır için ürün veya hizmet kartı gereklidir")
		}
		if lineType == "SERVICE" {
			if strings.TrimSpace(line.WarehouseID) != "" {
				return validation("hizmet satırında depo bulunamaz")
			}
		} else if lineType == "PRODUCT" {
			if err := validatePurchaseWarehouseTx(ctx, q, session, branchID, line.WarehouseID); err != nil {
				return err
			}
		} else {
			return validation("satır türü geçersiz")
		}

		var productKind string
		var productActive bool
		if err := q.QueryRow(ctx, `SELECT kind::text,is_active FROM products WHERE company_id=$1 AND id=$2 FOR SHARE`, session.CurrentCompanyID, line.ProductID).Scan(&productKind, &productActive); errors.Is(err, pgx.ErrNoRows) {
			return identity.ErrForbidden
		} else if err != nil {
			return err
		}
		if !productActive {
			return ErrProductInactive
		}
		expectedKind := "PHYSICAL"
		if lineType == "SERVICE" {
			expectedKind = "SERVICE"
		}
		if productKind != expectedKind {
			return validation("ürün kartı satır türüyle eşleşmiyor")
		}

		variantID := strings.TrimSpace(line.VariantID)
		if variantID == "" {
			var variantsEnabled bool
			if err := q.QueryRow(ctx, `SELECT variants_enabled OR EXISTS(SELECT 1 FROM product_variants pv WHERE pv.company_id=products.company_id AND pv.product_id=products.id) FROM products WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, line.ProductID).Scan(&variantsEnabled); err != nil {
				return err
			}
			if lineType == "PRODUCT" && variantsEnabled {
				return ErrVariantRequired
			}
			continue
		}
		if uuid.Validate(variantID) != nil {
			return validation("varyant kimliği geçersiz")
		}
		var variantExists, variantActive bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),COALESCE((SELECT is_active FROM product_variants WHERE company_id=$1 AND id=$2 AND product_id=$3),false)`, session.CurrentCompanyID, variantID, line.ProductID).Scan(&variantExists, &variantActive); err != nil {
			return err
		}
		if !variantExists {
			return validation("varyant ürünle eşleşmiyor")
		}
		if !variantActive {
			return ErrVariantInactive
		}
		if lineType == "SERVICE" {
			return validation("hizmet satırında varyant bulunamaz")
		}
	}
	return nil
}

func validatePurchaseWarehouseTx(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, session identity.Session, branchID, warehouseID string) error {
	warehouseID = strings.TrimSpace(warehouseID)
	if warehouseID == "" {
		return ErrWarehouseRequired
	}
	if uuid.Validate(warehouseID) != nil {
		return identity.ErrForbidden
	}
	var active bool
	if err := q.QueryRow(ctx, `SELECT is_active FROM warehouses WHERE company_id=$1 AND id=$2 AND branch_id=$3 FOR SHARE`, session.CurrentCompanyID, warehouseID, branchID).Scan(&active); errors.Is(err, pgx.ErrNoRows) {
		return identity.ErrForbidden
	} else if err != nil {
		return err
	}
	if !active {
		return ErrWarehouseInactive
	}
	if err := ensurePurchaseScope(ctx, q, session, branchID, warehouseID); err != nil {
		return err
	}
	return nil
}
