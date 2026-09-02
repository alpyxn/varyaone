package identity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUserHasIndependentPermissionsAcrossCompanies(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := identityTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Setup(ctx, SetupInput{
		AdminName: "Test Yönetici", AdminEmail: "admin@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Birinci Firma AŞ", TradeName: "Birinci", EntityType: "LEGAL_ENTITY",
	}, RequestMeta{TraceID: "integration-test", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !session.HasPermission("security.token.manage") {
		t.Fatal("setup administrator did not receive administrator permissions")
	}
	for _, code := range []string{"preaccounting", "hr", "fixed_asset"} {
		if !session.HasModule(code) {
			t.Fatalf("setup without an explicit module list did not enable %s", code)
		}
	}
	disabled, err := service.SetModule(ctx, session, "hr", false, 1, RequestMeta{TraceID: "integration-test"})
	if err != nil {
		t.Fatalf("SetModule disable failed: %v", err)
	}
	if disabled.Enabled || disabled.Version != 2 {
		t.Fatalf("SetModule returned %+v, want disabled at version 2", disabled)
	}
	if _, err := service.SetModule(ctx, session, "hr", true, 1, RequestMeta{}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale If-Match returned %v, want ErrConflict", err)
	}
	reloaded, err := service.Authenticate(ctx, session.Token)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.HasModule("hr") || !reloaded.HasModule("preaccounting") {
		t.Fatalf("session did not reflect the disabled module: %v", reloaded.Modules)
	}
	states, err := service.ListModules(ctx, session)
	if err != nil || len(states) != 3 {
		t.Fatalf("ListModules returned %d states, err=%v", len(states), err)
	}
	rows, err := pool.Query(ctx, `SELECT code, name FROM party_groups WHERE company_id=$1 ORDER BY code`, session.CurrentCompanyID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	groups := make(map[string]string, len(defaultPartyGroups))
	for rows.Next() {
		var code, name string
		if err := rows.Scan(&code, &name); err != nil {
			t.Fatal(err)
		}
		groups[code] = name
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, expected := range defaultPartyGroups {
		if groups[expected.code] != expected.name {
			t.Fatalf("setup did not seed default cari group %s: got %q", expected.code, groups[expected.code])
		}
	}
	updatedCompany, err := service.UpdateCompany(ctx, session, CompanyInput{LegalName: "Birinci Firma AŞ", TradeName: "Birinci", EntityType: "LEGAL_ENTITY", BaseCurrency: "TRY", Timezone: "Europe/Istanbul"}, 1, RequestMeta{TraceID: "phase01-client"})
	if err != nil {
		t.Fatal(err)
	}
	if updatedCompany.DuplicatePartyTaxNumberPolicy != "WARN" || updatedCompany.PartyCodePrefix != "CR" || updatedCompany.PartyCodeDigits != 6 {
		t.Fatalf("legacy company update reset cari policy: %+v", updatedCompany)
	}
	createdToken, err := service.CreateAPIToken(ctx, session, "Entegrasyon", []string{"organization:read", "security:users:read"}, nil, RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	tokenSession, err := service.AuthenticateAPIToken(ctx, createdToken.PlainToken)
	if err != nil {
		t.Fatal(err)
	}
	if !tokenSession.HasPermission("organization.company.read") || tokenSession.HasPermission("security.token.manage") || len(tokenSession.Companies) != 1 {
		t.Fatalf("API token escaped its effective scope: permissions=%v companies=%d", tokenSession.Permissions, len(tokenSession.Companies))
	}
	if err := service.RevokeAPIToken(ctx, session, createdToken.ID, RequestMeta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthenticateAPIToken(ctx, createdToken.PlainToken); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked API token returned %v, want ErrUnauthenticated", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM membership_roles WHERE company_id=$1 AND user_id=$2`, session.CurrentCompanyID, session.User.ID); err == nil {
		t.Fatal("database allowed removal of the final system administrator")
	}
	const companyB = "10000000-0000-4000-8000-000000000001"
	const roleB = "10000000-0000-4000-8000-000000000002"
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'İkinci Firma Ltd','İkinci','LEGAL_ENTITY')`, []any{companyB}},
		{`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, []any{companyB, session.User.ID}},
		{`INSERT INTO roles(id,company_id,name) VALUES($1,$2,'Görüntüleyici')`, []any{roleB, companyB}},
		{`INSERT INTO role_permissions(company_id,role_id,permission_code) VALUES($1,$2,'organization.company.read')`, []any{companyB, roleB}},
		{`INSERT INTO membership_roles(company_id,user_id,role_id) VALUES($1,$2,$3)`, []any{companyB, session.User.ID, roleB}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	selected, err := service.SelectCompany(ctx, session, companyB, RequestMeta{TraceID: "integration-test", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !selected.HasPermission("organization.company.read") || selected.HasPermission("security.token.manage") {
		t.Fatalf("permissions leaked between companies: %v", selected.Permissions)
	}
	_, err = service.SelectCompany(ctx, session, "10000000-0000-4000-8000-000000000099", RequestMeta{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("tampered company ID returned %v, want ErrForbidden", err)
	}
}

func TestCreateCompanyProvisionsAndSwitchesSession(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := identityTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.Setup(ctx, SetupInput{
		AdminName: "Kurulum Sahibi", AdminEmail: "owner@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Birinci Firma AŞ", TradeName: "Birinci", EntityType: "LEGAL_ENTITY",
		Modules: []string{"preaccounting"},
	}, RequestMeta{TraceID: "create-company-test", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if !owner.IsInstanceOwner {
		t.Fatal("setup administrator is not marked as the instance owner")
	}
	firstCompanyID := owner.CurrentCompanyID

	created, err := service.CreateCompany(ctx, owner, CreateCompanyInput{
		LegalName: "İkinci Firma Ltd", TradeName: "İkinci", EntityType: "SOLE_PROPRIETOR",
		Modules: []string{"preaccounting"},
	}, RequestMeta{TraceID: "create-company-test", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.CurrentCompanyID == firstCompanyID || created.CurrentCompanyID == "" {
		t.Fatalf("session did not switch to the new company: %q", created.CurrentCompanyID)
	}
	if len(created.Companies) != 2 {
		t.Fatalf("expected membership in 2 companies, got %d", len(created.Companies))
	}
	if !created.HasPermission("organization.company.edit") || !created.HasPermission("security.token.manage") {
		t.Fatalf("creator did not receive administrator permissions in the new company: %v", created.Permissions)
	}
	var branches, warehouses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM branches WHERE company_id=$1`, created.CurrentCompanyID).Scan(&branches); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM warehouses WHERE company_id=$1`, created.CurrentCompanyID).Scan(&warehouses); err != nil {
		t.Fatal(err)
	}
	if branches != 1 || warehouses != 2 {
		t.Fatalf("new company baseline wrong: branches=%d warehouses=%d", branches, warehouses)
	}

	// A member who did not complete setup cannot create companies.
	const memberID = "20000000-0000-4000-8000-000000000001"
	const roleID = "20000000-0000-4000-8000-000000000002"
	stmts := [][]any{
		{`INSERT INTO users(id,email,display_name,password_hash) VALUES($1,'member@example.test','Üye','x')`, memberID},
		{`INSERT INTO company_memberships(company_id,user_id) VALUES($1,$2)`, firstCompanyID, memberID},
		{`INSERT INTO roles(id,company_id,name) VALUES($1,$2,'Görüntüleyici')`, roleID, firstCompanyID},
		{`INSERT INTO role_permissions(company_id,role_id,permission_code) VALUES($1,$2,'organization.company.read')`, firstCompanyID, roleID},
		{`INSERT INTO membership_roles(company_id,user_id,role_id) VALUES($1,$2,$3)`, firstCompanyID, memberID, roleID},
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s[0].(string), s[1:]...); err != nil {
			t.Fatal(err)
		}
	}
	memberSession := Session{ID: owner.ID, User: User{ID: memberID}, CurrentCompanyID: firstCompanyID}
	if _, err := service.CreateCompany(ctx, memberSession, CreateCompanyInput{
		LegalName: "Üçüncü", TradeName: "Üçüncü", EntityType: "LEGAL_ENTITY",
	}, RequestMeta{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner CreateCompany returned %v, want ErrForbidden", err)
	}
}

func identityTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_identity_test_%d", time.Now().UnixNano())
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
