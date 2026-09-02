package inventory

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestVariantEnabledProductWithoutVariantRejectsDirectStockPosition(t *testing.T) {
	fixture := newTransferStockFixture(t, "0", "0")
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE products SET variants_enabled=true WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestProduct); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.pool.Exec(fixture.ctx, `
		INSERT INTO stock_positions(id,company_id,warehouse_id,product_id,variant_id,physical_quantity,reserved_quantity)
		VALUES('10000000-0000-4000-8000-000000000016',$1,$2,$3,NULL,'1','0')`,
		transferTestCompany, transferTestSource, transferTestProduct)
	if err == nil {
		t.Fatal("variant-enabled product accepted a parent stock position without a variant")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("stock position rejection error=%T %v, want PostgreSQL error", err, err)
	}
	if pgErr.Code != "23514" || !strings.Contains(pgErr.Message, "VARIANT_REQUIRED") {
		t.Fatalf("stock position rejection code=%s message=%q, want 23514/VARIANT_REQUIRED", pgErr.Code, pgErr.Message)
	}
}

func TestVariantEnabledProductWithoutVariantRejectsMovementSafely(t *testing.T) {
	fixture := newTransferStockFixture(t, "0", "0")
	if _, err := fixture.pool.Exec(fixture.ctx, `UPDATE products SET variants_enabled=true WHERE company_id=$1 AND id=$2`, transferTestCompany, transferTestProduct); err != nil {
		t.Fatal(err)
	}

	_, err := fixture.service.PostMovement(fixture.ctx, variantMovementInput(""))
	if !errors.Is(err, ErrVariantRequired) {
		t.Fatalf("variant-enabled product without a variant returned %v, want %v", err, ErrVariantRequired)
	}

	var movementCount int
	if err := fixture.pool.QueryRow(fixture.ctx, `SELECT count(*) FROM stock_movements WHERE company_id=$1 AND product_id=$2`, transferTestCompany, transferTestProduct).Scan(&movementCount); err != nil {
		t.Fatal(err)
	}
	if movementCount != 0 {
		t.Fatalf("rejected variantless movement created %d stock movement rows", movementCount)
	}
}
