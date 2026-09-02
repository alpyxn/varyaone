package sales

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

func TestSalesSourceDocumentReferenceUsesAuthoritativeMetadata(t *testing.T) {
	tests := []struct {
		code, kind, status, lifecycle string
	}{
		{code: "SALES_QUOTE", kind: "QUOTE", status: "SENT", lifecycle: "SENT"},
		{code: "SALES_ORDER", kind: "ORDER", status: "CONFIRMED", lifecycle: "OPEN"},
		{code: "SALES_DELIVERY", kind: "DISPATCH", status: "POSTED", lifecycle: "FINALIZED"},
		{code: "SALES_INVOICE", kind: "INVOICE", status: "POSTED", lifecycle: "FINALIZED"},
		{code: "SALES_RETURN_INVOICE", kind: "RETURN", status: "CANCELLED", lifecycle: "CANCELLED"},
	}
	for _, test := range tests {
		t.Run(test.code, func(t *testing.T) {
			spec, ok := commercialSpecForSourceType(test.code)
			if !ok {
				t.Fatalf("source type %q was not recognized", test.code)
			}
			if got := sourceKindForCommercial(spec.kind); got != test.kind {
				t.Fatalf("source kind = %q, want %q", got, test.kind)
			}
			if got := commercialLifecycleStatus(spec.kind, test.status); got != test.lifecycle {
				t.Fatalf("source lifecycle = %q, want %q", got, test.lifecycle)
			}

			reference := SourceDocumentReference{
				ID:               "source-id",
				DocumentNo:       "IRS-001",
				DocumentTypeCode: test.code,
				Kind:             test.kind,
				RelationType:     "INVOICING",
				Direction:        "SOURCE",
				LifecycleStatus:  test.lifecycle,
				Status:           test.status,
			}
			if reference.DocumentNo != "IRS-001" || reference.RelationType != "INVOICING" || reference.Direction != "SOURCE" {
				t.Fatalf("source reference lost authoritative fields: %+v", reference)
			}
		})
	}
}

func TestSalesSourceDocumentReferencesPreserveMultipleRelations(t *testing.T) {
	sources := []SourceDocumentReference{
		{ID: "dispatch-id", DocumentNo: "IRS-001", DocumentTypeCode: "SALES_DELIVERY", Kind: "DISPATCH", RelationType: "FULFILLMENT", Direction: "SOURCE", LifecycleStatus: "FINALIZED", Status: "POSTED"},
		{ID: "order-id", DocumentNo: "SIP-001", DocumentTypeCode: "SALES_ORDER", Kind: "ORDER", RelationType: "CONVERSION", Direction: "SOURCE", LifecycleStatus: "OPEN", Status: "CONFIRMED"},
	}
	if len(sources) != 2 || sources[0].DocumentNo != "IRS-001" || sources[1].DocumentNo != "SIP-001" {
		t.Fatalf("multiple source references were not retained: %+v", sources)
	}
	if sources[0].RelationType == sources[1].RelationType {
		t.Fatal("distinct source relation types were collapsed")
	}
}

func TestSalesSourceDocumentsAreCompanyScoped(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool := salesSourceMetadataTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Satış Yönetici", AdminEmail: "sales-source@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Satış Kaynak Test AŞ", TradeName: "Satış Kaynak Test", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{TraceID: "sales-source-metadata"})
	if err != nil {
		t.Fatal(err)
	}
	var branchID, partyID string
	if err := pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1 ORDER BY created_at LIMIT 1`, session.CurrentCompanyID).Scan(&branchID); err != nil {
		t.Fatal(err)
	}
	partyID = uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'SRC-001','ORGANIZATION',true,true,'Kaynak Test Cari','Kaynak Test Cari','TRY')`, partyID, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}

	targetID, dispatchID, orderID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, document := range []struct {
		id, typeCode, number string
	}{
		{targetID, "SALES_INVOICE", "FAT-001"},
		{dispatchID, "SALES_DELIVERY", "IRS-001"},
		{orderID, "SALES_ORDER", "SIP-001"},
	} {
		insertSalesSourceTestDocument(t, ctx, pool, session.CurrentCompanyID, document.id, document.typeCode, document.number, branchID, partyID, session.User.ID, "POSTED")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'FULFILLMENT'),($1,$2,$4,'CONVERSION')`, session.CurrentCompanyID, targetID, dispatchID, orderID); err != nil {
		t.Fatal(err)
	}

	// Reuse the same IDs in another company. A source row from that company
	// must not be visible when the selected company is the first one.
	otherCompanyID, otherBranchID, otherPartyID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'İkinci Kaynak Firma','İkinci Kaynak','LEGAL_ENTITY')`, []any{otherCompanyID}},
		{`INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'MRK','Merkez')`, []any{otherBranchID, otherCompanyID}},
		{`INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,default_currency) VALUES($1,$2,'SRC-001','ORGANIZATION',true,true,'Diğer Cari','Diğer Cari','TRY')`, []any{otherPartyID, otherCompanyID}},
		{`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, []any{otherCompanyID, session.User.ID}},
	} {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	insertSalesSourceTestDocument(t, ctx, pool, otherCompanyID, targetID, "SALES_INVOICE", "DIGER-FAT-001", otherBranchID, otherPartyID, session.User.ID, "POSTED")
	insertSalesSourceTestDocument(t, ctx, pool, otherCompanyID, dispatchID, "SALES_DELIVERY", "DIGER-IRS-001", otherBranchID, otherPartyID, session.User.ID, "POSTED")
	if _, err := pool.Exec(ctx, `INSERT INTO commercial_document_sources(company_id,document_id,source_document_id,relation_type) VALUES($1,$2,$3,'FULFILLMENT')`, otherCompanyID, targetID, dispatchID); err != nil {
		t.Fatal(err)
	}

	sources, err := loadCommercialSources(ctx, pool, session, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 2 {
		t.Fatalf("source count = %d, want 2: %+v", len(sources), sources)
	}
	byID := make(map[string]SourceDocumentReference, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	if byID[dispatchID].DocumentNo != "IRS-001" || byID[dispatchID].RelationType != "FULFILLMENT" {
		t.Fatalf("dispatch source metadata = %+v", byID[dispatchID])
	}
	if byID[orderID].DocumentNo != "SIP-001" || byID[orderID].RelationType != "CONVERSION" {
		t.Fatalf("order source metadata = %+v", byID[orderID])
	}
}

func insertSalesSourceTestDocument(t *testing.T, ctx context.Context, pool *pgxpool.Pool, companyID, id, typeCode, documentNo, branchID, partyID, userID, status string) {
	t.Helper()
	var postedAt any
	if status == "POSTED" {
		postedAt = time.Now().UTC()
	}
	if _, err := pool.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,status,posted_at,created_by,updated_by) VALUES($1,$2,$3,$4,$5,$6,CURRENT_DATE,'TRY',$7,$8,$9,$9)`, id, companyID, typeCode, documentNo, branchID, partyID, status, postedAt, userID); err != nil {
		t.Fatal(err)
	}
}

func salesSourceMetadataTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_sales_source_test_%d", time.Now().UnixNano())
	if _, err := base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
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
	return pool
}
