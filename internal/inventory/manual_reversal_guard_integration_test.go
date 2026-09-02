package inventory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestManualReverseRejectsDocumentOriginMovement proves invariant 5/20/21: the
// generic stock-movement reverse command may only compensate a genuinely manual
// movement. A movement a document created (here a goods receipt) owns its
// lifecycle through that document and can only be undone by cancelling it.
// The rejection is enforced both in the service and by the 000111 DB trigger.
func TestManualReverseRejectsDocumentOriginMovement(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_manual_reverse_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	if err = migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}

	companyID, userID, productID, warehouseID, branchID, partyID :=
		uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	mustExec(`INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Manual Reverse AŞ','Manual Reverse','LEGAL_ENTITY','TRY')`, companyID)
	mustExec(`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'manual-reverse@example.test','Manual Reverse User','test-hash')`, userID)
	mustExec(`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID)
	mustExec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MERKEZ','Merkez')`, branchID, companyID)
	mustExec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-001','ORGANIZATION',true,true,'Test Cari','Test Cari AŞ','TRY')`, partyID, companyID)
	mustExec(`INSERT INTO warehouses(id,company_id,code,name,warehouse_type,branch_id) VALUES($1,$2,'DEPO','Test Depo','STANDARD',$3)`, warehouseID, companyID, branchID)
	mustExec(`INSERT INTO products(id,company_id,code,name,kind,variants_enabled) VALUES($1,$2,'STK-MR','Manual Reverse Ürünü','PHYSICAL',false)`, productID, companyID)
	mustExec(`INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor) VALUES($1,$2,'ADET',true,1)`, companyID, productID)

	session := identity.Session{
		User:             identity.User{ID: userID},
		CurrentCompanyID: companyID,
		Permissions:      []string{"inventory.movement.post", "inventory.movement.reverse"},
	}
	inv := NewService(pool)

	// A document-origin movement: a posted goods receipt of 10 units.
	receiptID := uuid.NewString()
	mustExec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by) VALUES($1,$2,'PURCHASE_DELIVERY','MK-01',$3,$4,now()::date,'TRY',$5,$5)`, receiptID, companyID, branchID, partyID, userID)
	receiptLineID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = inv.PostPurchaseReceiptMovementsTx(ctx, tx, session, PurchaseReceiptStockPostingInput{
		ReceiptID: receiptID, WarehouseID: warehouseID,
		Lines: []PurchaseStockLine{{LineID: receiptLineID, ProductID: productID, Quantity: "10", BaseQuantity: "10", ConversionFactor: "1", UnitCode: "ADET", UnitCost: "50", Currency: "TRY"}},
	}); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("post receipt: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	var receiptMovementID string
	if err = pool.QueryRow(ctx, `SELECT id FROM stock_movements WHERE company_id=$1 AND source_id=$2`, companyID, receiptID).Scan(&receiptMovementID); err != nil {
		t.Fatal(err)
	}

	// Service layer rejects the manual reverse of a document-origin movement.
	if _, err = inv.ReverseMovement(ctx, companyID, receiptMovementID, "yanlış giriş", "", userID); !errors.Is(err, ErrDocumentOriginMovement) {
		t.Fatalf("document-origin reverse: got %v, want ErrDocumentOriginMovement", err)
	}

	// Database trigger rejects a hand-crafted reversal row too (API bypass).
	_, err = pool.Exec(ctx, `INSERT INTO stock_movements
		(id,company_id,warehouse_id,product_id,movement_type,direction,quantity,reason_code,reason_description,source_type,source_id,idempotency_key,payload_hash,reversal_of_id,actor_user_id)
		VALUES($1,$2,$3,$4,'RECONCILIATION','OUT',10,'REVERSAL','bypass','STOCK_MOVEMENT_REVERSAL',$5,$6,decode(md5('bypass'),'hex'),$5,$7)`,
		uuid.NewString(), companyID, warehouseID, productID, receiptMovementID, "bypass-key-"+receiptMovementID[:8], userID)
	if err == nil {
		t.Fatal("DB trigger allowed a hand-crafted reversal of a document-origin movement")
	}

	// Stock is unchanged: still exactly +10, no reversal row.
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "10.00000000")

	// A manual adjustment, by contrast, can be reversed exactly once.
	manualID := uuid.NewString()
	if _, err = inv.PostMovement(ctx, MovementInput{
		ID: manualID, CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID,
		MovementType: MovementManualAdjustment, Direction: DirectionIn, Quantity: "4", EnteredQuantity: "4",
		UnitCode: "ADET", ConversionFactor: "1", ReasonCode: "CORRECTION", ReasonDescription: "sayım farkı",
		ActorUserID: userID,
	}); err != nil {
		t.Fatalf("post manual movement: %v", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "14.00000000")

	if _, err = inv.ReverseMovement(ctx, companyID, manualID, "hatalı düzeltme", "reverse-manual-1", userID); err != nil {
		t.Fatalf("reverse manual movement: %v", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "10.00000000")

	// A second reverse of the same manual movement is rejected (single reversal),
	// even with a fresh idempotency key.
	if _, err = inv.ReverseMovement(ctx, companyID, manualID, "tekrar", "reverse-manual-2", userID); !errors.Is(err, ErrMovementAlreadyReversed) {
		t.Fatalf("double reverse: got %v, want ErrMovementAlreadyReversed", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "10.00000000")

	// A movement with a source that is NOT a commercial document (here an
	// opening-stock import) can be reversed directly from stock movements.
	nonDocID, nonDocSource := uuid.NewString(), uuid.NewString()
	if _, err = inv.PostMovement(ctx, MovementInput{
		ID: nonDocID, CompanyID: companyID, WarehouseID: warehouseID, ProductID: productID,
		MovementType: MovementDamage, Direction: DirectionOut, Quantity: "3", EnteredQuantity: "3",
		UnitCode: "ADET", ConversionFactor: "1", ReasonCode: "DAMAGE", ReasonDescription: "hasar",
		SourceType: "OPENING_STOCK_IMPORT", SourceID: nonDocSource, ActorUserID: userID,
	}); err != nil {
		t.Fatalf("post non-document movement: %v", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "7.00000000")

	if reversed, gErr := inv.GetMovement(ctx, companyID, nonDocID, userID); gErr != nil {
		t.Fatalf("get non-document movement: %v", gErr)
	} else if reversed.ReversedByID != nil {
		t.Fatalf("movement reported as reversed before it was: %v", *reversed.ReversedByID)
	}

	reversalEntry, err := inv.ReverseMovement(ctx, companyID, nonDocID, "hatalı kayıt", "reverse-nondoc-1", userID)
	if err != nil {
		t.Fatalf("reverse non-document movement: %v", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "10.00000000")

	// The original now advertises its reversal so the UI can hide the action.
	if reversed, gErr := inv.GetMovement(ctx, companyID, nonDocID, userID); gErr != nil {
		t.Fatalf("get reversed movement: %v", gErr)
	} else if reversed.ReversedByID == nil || *reversed.ReversedByID != reversalEntry.ID {
		t.Fatalf("reversed_by_id = %v, want %s", reversed.ReversedByID, reversalEntry.ID)
	}

	// A second reverse of the same movement is rejected cleanly.
	if _, err = inv.ReverseMovement(ctx, companyID, nonDocID, "tekrar", "reverse-nondoc-again", userID); !errors.Is(err, ErrMovementAlreadyReversed) {
		t.Fatalf("double reverse non-document: got %v, want ErrMovementAlreadyReversed", err)
	}

	// The reversal entry itself is not reversible again.
	if _, err = inv.ReverseMovement(ctx, companyID, reversalEntry.ID, "olmaz", "reverse-nondoc-2", userID); !errors.Is(err, ErrDocumentOriginMovement) {
		t.Fatalf("reverse of a reversal entry: got %v, want ErrDocumentOriginMovement", err)
	}
	assertPhysical(t, ctx, pool, companyID, warehouseID, productID, "10.00000000")
}

func assertPhysical(t *testing.T, ctx context.Context, pool *pgxpool.Pool, companyID, warehouseID, productID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(physical_quantity),0)::text FROM stock_positions WHERE company_id=$1 AND warehouse_id=$2 AND product_id=$3`, companyID, warehouseID, productID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("physical quantity=%s, want %s", got, want)
	}
}

var _ = pgx.ErrNoRows
