package taxes

import (
	"context"
	"fmt"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"testing"
	"time"
)

func TestDefinitionRateEditsPreserveEarlierDates(t *testing.T) {
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
	schema := fmt.Sprintf("varya_tax_%d", time.Now().UnixNano())
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
	if _, err = pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type,base_currency) VALUES($1,'Tax Test','Tax Test','LEGAL_ENTITY','TRY')`, companyID); err != nil {
		t.Fatal(err)
	}
	var definition TaxDefinition
	definition.CompanyID = companyID
	definition.Source = "TEST"
	if err = pool.QueryRow(ctx, `SELECT id FROM tax_definitions WHERE company_id=$1 AND code='KDV_20'`, companyID).Scan(&definition.ID); err != nil {
		t.Fatal(err)
	}
	change := func(value, kind string) {
		t.Helper()
		normalized, err := normalizeTaxValue(value, kind)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if err = updateDefinitionRateTx(ctx, tx, definition, TaxDefinition{Rate: normalized, CalculationType: kind}); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	change("18,5", "PERCENTAGE")
	change("125,50", "QUANTITY_BASED")
	var past, today, kind string
	if err = pool.QueryRow(ctx, `SELECT rate::text FROM tax_rates WHERE company_id=$1 AND tax_definition_id=$2 AND valid_from<=CURRENT_DATE-1 AND valid_to>=CURRENT_DATE-1`, companyID, definition.ID).Scan(&past); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT rate::text,calculation_type FROM tax_rates WHERE company_id=$1 AND tax_definition_id=$2 AND valid_from=CURRENT_DATE`, companyID, definition.ID).Scan(&today, &kind); err != nil {
		t.Fatal(err)
	}
	if normalizeDecimal(past) != "20" || normalizeDecimal(today) != "125.5" || kind != "QUANTITY_BASED" {
		t.Fatalf("past=%s today=%s kind=%s", past, today, kind)
	}
	change("125,50", "QUANTITY_BASED")
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM tax_rates WHERE company_id=$1 AND tax_definition_id=$2`, companyID, definition.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows=%d err=%v", count, err)
	}
}
