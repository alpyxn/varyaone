package dashboard

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recentActivityFixture spins up an isolated, fully migrated schema with one
// company (its default branch/warehouse and one party) for exercising
// RecentActivity's permission and branch/warehouse scoping.
type recentActivityFixture struct {
	ctx                                               context.Context
	t                                                 *testing.T
	pool                                              *pgxpool.Pool
	service                                           *Service
	session                                           identity.Session
	companyID, branchID, warehouseID, partyID, userID string
}

func newRecentActivityFixture(t *testing.T) *recentActivityFixture {
	t.Helper()
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
	schema := fmt.Sprintf("varya_recent_%d", time.Now().UnixNano())
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Panel Yönetici", AdminEmail: "panel@example.test",
		Password: "uzun-ve-guvenli-parola", LegalName: "Panel AŞ", TradeName: "Panel", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "recent-activity-test"})
	if err != nil {
		t.Fatal(err)
	}
	f := &recentActivityFixture{ctx: ctx, t: t, pool: pool, service: NewService(pool), session: session, companyID: session.CurrentCompanyID, userID: session.User.ID}
	if err = pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1 ORDER BY created_at LIMIT 1`, f.companyID).Scan(&f.branchID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM warehouses WHERE company_id=$1 AND branch_id=$2 AND NOT is_system ORDER BY created_at LIMIT 1`, f.companyID, f.branchID).Scan(&f.warehouseID); err != nil {
		t.Fatal(err)
	}
	f.partyID = uuid.NewString()
	f.exec(`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'CARI-1','ORGANIZATION',true,false,'Test Müşteri','Test Müşteri AŞ','TRY')`, f.partyID, f.companyID)
	return f
}

func (f *recentActivityFixture) exec(sql string, args ...any) {
	f.t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		f.t.Fatalf("fixture: %v", err)
	}
}

func (f *recentActivityFixture) addBranchAndWarehouse(code string) (branchID, warehouseID string) {
	branchID, warehouseID = uuid.NewString(), uuid.NewString()
	f.exec(`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,$3,$3)`, branchID, f.companyID, code)
	f.exec(`INSERT INTO warehouses(id,company_id,branch_id,code,name) VALUES($1,$2,$3,$4,$4)`, warehouseID, f.companyID, branchID, code+"-WH")
	return branchID, warehouseID
}

func (f *recentActivityFixture) product(code string) string {
	id := uuid.NewString()
	f.exec(`INSERT INTO products(id,company_id,code,name,kind) VALUES($1,$2,$3,$3,'PHYSICAL')`, id, f.companyID, code)
	return id
}

func (f *recentActivityFixture) stockMovement(warehouseID, productID string) {
	id := uuid.NewString()
	key := "test-key-" + id
	f.exec(`INSERT INTO stock_movements(id,company_id,warehouse_id,product_id,movement_type,direction,quantity,reason_code,reason_description,source_type,source_id,idempotency_key,payload_hash,actor_user_id)
		VALUES($1,$2,$3,$4,'MANUAL_ADJUSTMENT','IN',1,'CORRECTION','test düzeltme','MANUAL',$1,$5,sha256($5::text::bytea),$6)`,
		id, f.companyID, warehouseID, productID, key, f.userID)
}

func (f *recentActivityFixture) draftDocument(typeCode, no, branchID string) {
	id := uuid.NewString()
	f.exec(`INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by)
		VALUES($1,$2,$3,$4,$5,$6,now()::date,'TRY',$7,$7)`,
		id, f.companyID, typeCode, no, branchID, f.partyID, f.userID)
}

// TestRecentActivityHidesStockOutsideWarehouseScope pins the internal/dashboard
// fix for a report finding: RecentActivity's stock block used to filter only
// by company_id, so a user whose membership is scoped to one warehouse (via
// membership_branch_scopes/membership_warehouse_scopes, the same tables
// inventory.ListMovements itself filters by) could see stock movements from
// every other warehouse in the company too.
func TestRecentActivityHidesStockOutsideWarehouseScope(t *testing.T) {
	f := newRecentActivityFixture(t)
	_, outsideWarehouseID := f.addBranchAndWarehouse("OUTSIDE")
	productID := f.product("URUN-1")
	f.stockMovement(f.warehouseID, productID)
	f.stockMovement(outsideWarehouseID, productID)

	// Unscoped membership (no rows in membership_warehouse_scopes) still sees
	// both warehouses — existing behaviour for the common case is unchanged.
	unscoped := f.session
	unscoped.Permissions = []string{"inventory.read"}
	entries, err := f.service.RecentActivity(f.ctx, unscoped, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("unscoped user: got %d stock entries, want 2", len(entries))
	}

	f.exec(`INSERT INTO membership_warehouse_scopes(company_id,user_id,warehouse_id) VALUES($1,$2,$3)`, f.companyID, f.userID, f.warehouseID)
	scoped := f.session
	scoped.Permissions = []string{"inventory.read"}
	entries, err = f.service.RecentActivity(f.ctx, scoped, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("warehouse-scoped user: got %d stock entries, want 1", len(entries))
	}
	if entries[0].Kind != "stock" {
		t.Fatalf("unexpected entry kind %q", entries[0].Kind)
	}
}

// TestRecentActivityFiltersDocumentsByPermittedTypeAndBranch pins the second
// half of the same report finding: the document block used to show every
// DRAFT document of every commercial type and every branch as soon as the
// caller held any single one of a fixed list of read permissions, instead of
// only the types that permission actually grants and only the caller's own
// branch/warehouse scope.
func TestRecentActivityFiltersDocumentsByPermittedTypeAndBranch(t *testing.T) {
	f := newRecentActivityFixture(t)
	outsideBranchID, _ := f.addBranchAndWarehouse("OUTSIDE-DOC")
	f.draftDocument("SALES_QUOTE", "TEKLIF-1", f.branchID)
	f.draftDocument("SALES_ORDER", "SIPARIS-1", f.branchID)
	f.draftDocument("SALES_QUOTE", "TEKLIF-OUTSIDE", outsideBranchID)

	// Holding only sales.quote.read must show SALES_QUOTE and nothing else —
	// not SALES_ORDER, and not the quote sitting in a branch outside scope.
	session := f.session
	session.Permissions = []string{"sales.quote.read"}
	f.exec(`INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, f.companyID, f.userID, f.branchID)
	entries, err := f.service.RecentActivity(f.ctx, session, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scoped quote reader: got %d document entries, want 1", len(entries))
	}
	if entries[0].TitleCode != "SALES_QUOTE" || entries[0].Label != "TEKLIF-1" {
		t.Fatalf("unexpected entry %+v", entries[0])
	}

	// A caller with no matching permission at all sees the whole document
	// block disappear rather than an unfiltered feed.
	none := f.session
	none.Permissions = nil
	entries, err = f.service.RecentActivity(f.ctx, none, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("no-permission caller: got %d entries, want 0", len(entries))
	}
}

// TestRecentActivityDocumentReadDoesNotGrantPurchaseVisibility pins a report
// finding: allowedDocumentTypeCodes used one "manage" flag (that included a
// bare "document.read") for both the sales and the purchasing document
// blocks. internal/sales.hasCommercialReadPermission does accept a bare
// "document.read", but internal/purchasing.authorizeReadPermission does not
// — it accepts "purchase.order.manage" instead, never "document.read". A
// session holding only "document.read" was shown purchase-order summaries
// here that ListPurchaseDocuments/GetPurchaseDocument would themselves
// refuse to return.
func TestRecentActivityDocumentReadDoesNotGrantPurchaseVisibility(t *testing.T) {
	f := newRecentActivityFixture(t)
	f.draftDocument("SALES_QUOTE", "TEKLIF-DR", f.branchID)
	f.draftDocument("PURCHASE_ORDER", "SIP-DR", f.branchID)
	f.exec(`INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, f.companyID, f.userID, f.branchID)

	session := f.session
	session.Permissions = []string{"document.read"}
	entries, err := f.service.RecentActivity(f.ctx, session, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("document.read caller: got %d entries, want 1 (the sales quote only)", len(entries))
	}
	if entries[0].TitleCode != "SALES_QUOTE" {
		t.Fatalf("document.read caller saw %q, want only SALES_QUOTE — purchase.order.manage/commercial.document.read/.manage is what purchasing actually requires", entries[0].TitleCode)
	}
}
