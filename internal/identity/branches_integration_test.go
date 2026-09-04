package identity

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
)

// Branches had no read endpoint, so a screen that needed one asked the user to
// type a branch id. The list has to respect the same branch scoping the
// document services enforce, otherwise the picker would offer branches the
// member is not allowed to post to.
func TestListBranchesRespectsMembershipScope(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool := identityTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(pool, bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Setup(ctx, SetupInput{
		AdminName: "Şube Yönetici", AdminEmail: "branches@example.test", Password: "uzun-ve-guvenli-parola",
		LegalName: "Şube AŞ", TradeName: "Şube", EntityType: "LEGAL_ENTITY",
	}, RequestMeta{TraceID: "branches-test"})
	if err != nil {
		t.Fatal(err)
	}

	// Setup provisions "Merkez"; add a second active and one closed branch.
	istanbul, closed := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(ctx, `INSERT INTO branches(id,company_id,code,name) VALUES($1,$2,'IST','İstanbul')`, istanbul, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO branches(id,company_id,code,name,is_active) VALUES($1,$2,'KPL','Kapanan Şube',false)`, closed, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}

	// An unscoped membership sees every active branch, ordered by code.
	branches, err := service.ListBranches(ctx, session, false)
	if err != nil {
		t.Fatalf("list branches: %v", err)
	}
	if got := codes(branches); len(got) != 2 || got[0] != "IST" || got[1] != "MRK" {
		t.Fatalf("unscoped branches = %v, want [IST MRK]", got)
	}

	// The closed branch only appears when it is asked for, so an account still
	// pointing at it can keep its selection.
	all, err := service.ListBranches(ctx, session, true)
	if err != nil {
		t.Fatalf("list branches including inactive: %v", err)
	}
	if got := codes(all); len(got) != 3 {
		t.Fatalf("branches including inactive = %v, want 3 entries", got)
	}

	// Scoping the membership to İstanbul hides the others.
	if _, err = pool.Exec(ctx, `INSERT INTO membership_branch_scopes(company_id,user_id,branch_id) VALUES($1,$2,$3)`, session.CurrentCompanyID, session.User.ID, istanbul); err != nil {
		t.Fatal(err)
	}
	scoped, err := service.ListBranches(ctx, session, false)
	if err != nil {
		t.Fatalf("list scoped branches: %v", err)
	}
	if got := codes(scoped); len(got) != 1 || got[0] != "IST" {
		t.Fatalf("scoped branches = %v, want [IST]", got)
	}

	// Without a company there is nothing to list.
	if _, err = service.ListBranches(ctx, Session{}, false); err != ErrForbidden {
		t.Fatalf("companyless session returned %v, want ErrForbidden", err)
	}
}

func codes(branches []Branch) []string {
	out := make([]string, 0, len(branches))
	for _, branch := range branches {
		out = append(out, branch.Code)
	}
	return out
}
