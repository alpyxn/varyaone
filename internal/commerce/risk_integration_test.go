package commerce

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestEvaluateSalesRiskAppliesPartyOwnPolicy proves the risk evaluation reads
// exactly the same balance the cari ekstre itself would show (SUM(debit) -
// SUM(credit) on the immutable party ledger, in the company's base currency),
// projects it with the document's own amount, and resolves ALLOW/WARN/BLOCK
// from the customer's own risk_policy -- not a single hardcoded behavior.
func TestEvaluateSalesRiskAppliesPartyOwnPolicy(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)

	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_risk_%d", time.Now().UnixNano())
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

	companyID := uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Risk Test AŞ','Risk Test','LEGAL_ENTITY','TRY')`, companyID); err != nil {
		t.Fatal(err)
	}

	newParty := func(policy string, creditLimit string) string {
		partyID := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,legal_name,display_name,default_currency,credit_limit,risk_policy)
			VALUES($1,$2,$3,'ORGANIZATION',true,'Test','Test',$4,$5,$6)`,
			partyID, companyID, "CARI-"+partyID[:8], "TRY", creditLimit, policy); err != nil {
			t.Fatal(err)
		}
		return partyID
	}
	postLedger := func(partyID, entryType, currency, debit, credit, rate string, withSnapshot bool) {
		baseColumns, baseValues := "", ""
		if withSnapshot {
			baseColumns, baseValues = ",base_currency,base_amount", ",'TRY',GREATEST($8::numeric,$9::numeric)*$10::numeric"
		}
		query := `INSERT INTO party_ledger_entries(id,company_id,party_id,currency,entry_type,source_type,source_id,idempotency_key,description,debit,credit,exchange_rate,document_date` + baseColumns + `)
			VALUES($1,$2,$3,$4,$5,'TEST',$6,$7,'test',$8::numeric,$9::numeric,$10::numeric,now()::date` + baseValues + `)`
		ledgerID := uuid.NewString()
		args := []any{ledgerID, companyID, partyID, currency, entryType, uuid.NewString(), uuid.NewString(), debit, credit, rate}
		if withSnapshot {
			if _, err := pool.Exec(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
			return
		}

		// Snapshot triggers populate every new row. To model a legacy row without
		// weakening the immutable UPDATE/DELETE trigger, create the row in a
		// short transaction with only the INSERT snapshot trigger temporarily
		// removed, then restore it before committing.
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err = tx.Exec(ctx, `DROP TRIGGER IF EXISTS party_ledger_currency_snapshot_trigger ON party_ledger_entries`); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `CREATE TRIGGER party_ledger_currency_snapshot_trigger
			BEFORE INSERT ON party_ledger_entries
			FOR EACH ROW EXECUTE FUNCTION populate_party_ledger_currency_snapshot()`); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	postReceivable := func(partyID, debit, credit string) {
		postLedger(partyID, "RECEIVABLE", "TRY", debit, credit, "1", true)
	}

	t.Run("under limit allows", func(t *testing.T) {
		party := newParty("BLOCK", "1000")
		postReceivable(party, "500", "0")
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "200")
		if err != nil {
			t.Fatal(err)
		}
		if eval.Decision != RiskAllow {
			t.Fatalf("decision = %s, want ALLOW; current=%s projected=%s", eval.Decision, eval.CurrentBalance, eval.ProjectedBalance)
		}
		if eval.CurrentBalance != "500.0000" {
			t.Fatalf("current balance = %s, want 500.0000", eval.CurrentBalance)
		}
	})

	t.Run("over limit with BLOCK policy blocks", func(t *testing.T) {
		party := newParty("BLOCK", "1000")
		postReceivable(party, "900", "0")
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "200")
		if err != nil {
			t.Fatal(err)
		}
		if eval.Decision != RiskBlock {
			t.Fatalf("decision = %s, want BLOCK; projected=%s limit=%s", eval.Decision, eval.ProjectedBalance, eval.CreditLimit)
		}
	})

	t.Run("over limit with WARN policy warns but does not block", func(t *testing.T) {
		party := newParty("WARN", "1000")
		postReceivable(party, "900", "0")
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "200")
		if err != nil {
			t.Fatal(err)
		}
		if eval.Decision != RiskWarn {
			t.Fatalf("decision = %s, want WARN", eval.Decision)
		}
	})

	t.Run("a payment reduces exposure", func(t *testing.T) {
		party := newParty("BLOCK", "1000")
		postReceivable(party, "900", "0")
		postLedger(party, "COLLECTION", "TRY", "0", "700", "1", true) // a collection credits the receivable
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "200")
		if err != nil {
			t.Fatal(err)
		}
		if eval.CurrentBalance != "200.0000" {
			t.Fatalf("current balance = %s, want 200.0000 after the payment", eval.CurrentBalance)
		}
		if eval.Decision != RiskAllow {
			t.Fatalf("decision = %s, want ALLOW once the balance is paid down", eval.Decision)
		}
	})

	t.Run("legacy rows without a base snapshot use their immutable rate", func(t *testing.T) {
		party := newParty("BLOCK", "1000")
		postLedger(party, "COLLECTION", "EUR", "0", "100", "2", false)
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "0")
		if err != nil {
			t.Fatal(err)
		}
		if eval.CurrentBalance != "-200.0000" {
			t.Fatalf("current balance = %s, want -200.0000 from legacy rate fallback", eval.CurrentBalance)
		}
	})

	// A confirmed sales order is committed exposure even before it is invoiced.
	// This subtest exercises the committedOrder subquery in EvaluateSalesRisk --
	// specifically the SERVICE (ELSE) branch of its CASE, whose GREATEST(...)
	// parenthesis was once left unclosed and made every order-confirm and
	// invoice-post fail with "syntax error at or near END".
	t.Run("a confirmed order adds committed exposure", func(t *testing.T) {
		party := newParty("BLOCK", "1000")
		postReceivable(party, "500", "0")

		userID := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO users(id,email,display_name,password_hash) VALUES($1,$2,'Test','x')`, userID, userID[:8]+"@t.test"); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, companyID, userID); err != nil {
			t.Fatal(err)
		}
		branchID := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,$3,'Merkez')`, branchID, companyID, "BR-"+branchID[:8]); err != nil {
			t.Fatal(err)
		}
		docID, orderID, lineID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO documents(id,company_id,document_type_code,document_no,branch_id,party_id,document_date,currency_code,created_by,updated_by)
			VALUES($1,$2,'SALES_ORDER',$3,$4,$5,now()::date,'TRY',$6,$6)`, docID, companyID, "SO-"+docID[:8], branchID, party, userID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO sales_orders(id,company_id,document_id,document_no,branch_id,party_id,document_date,currency_code,exchange_rate,status,created_by,updated_by,payable_total)
			VALUES($1,$2,$3,$4,$5,$6,now()::date,'TRY',1,'DRAFT',$7,$7,300)`, orderID, companyID, docID, "SO-"+docID[:8], branchID, party, userID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO sales_order_lines(id,company_id,document_id,line_no,line_type,unit_code,quantity,base_quantity,unit_price,payable_amount)
			VALUES($1,$2,$3,1,'SERVICE','ADET',1,1,300,300)`, lineID, companyID, orderID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE sales_orders SET status='CONFIRMED' WHERE company_id=$1 AND id=$2`, companyID, orderID); err != nil {
			t.Fatal(err)
		}

		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "201")
		if err != nil {
			t.Fatal(err)
		}
		// 500 receivable + 300 committed order + 201 additional.
		if eval.ProjectedBalance != "1001.0000" {
			t.Fatalf("projected balance = %s, want 1001.0000 (500 + 300 committed + 201)", eval.ProjectedBalance)
		}
		if eval.Decision != RiskBlock {
			t.Fatalf("decision = %s, want BLOCK once the committed order pushes exposure over the limit", eval.Decision)
		}
	})

	t.Run("zero credit_limit means unlimited, not zero", func(t *testing.T) {
		party := newParty("BLOCK", "0")
		postReceivable(party, "50000", "0")
		eval, err := EvaluateSalesRisk(ctx, pool, companyID, party, "1")
		if err != nil {
			t.Fatal(err)
		}
		if eval.Decision != RiskAllow {
			t.Fatalf("decision = %s, want ALLOW: an unset credit_limit must not block every sale", eval.Decision)
		}
	})
}
