package party

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

func TestPartyGridSortAcrossPages(t *testing.T) {
	url := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := partyTestPool(t, ctx, url)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	ids, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := ids.Setup(ctx, identity.SetupInput{AdminName: "Sıralama", AdminEmail: "sort@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Sıralama AŞ", TradeName: "Sıralama", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)
	var created []Party
	for i, amount := range []string{"10.50", "2", "100", "2", "0"} {
		p, err := service.Create(ctx, session, Input{Code: []string{"C03", "C01", "C05", "C02", "C04"}[i], Kind: "ORGANIZATION", IsCustomer: true, IsSupplier: i == 1, LegalName: []string{"Zeta", "Alfa", "Orta", "Alfa", "Beta"}[i], DefaultCurrency: "TRY", CreditLimit: amount, RiskLimit: amount, RiskPolicy: "WARN"}, identity.RequestMeta{})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, p)
	}
	// Empty contacts, nullable references and computed fields must all sort and
	// paginate, not merely draw a sorting icon on the first 50 records.
	fields := []string{"code", "trade_name", "legal_name", "first_name", "last_name", "kind", "roles", "tax_number", "identity_number", "tax_office", "phone", "email", "address_summary", "city", "contact_summary", "group_summary", "tag_summary", "custom_field_summary", "default_currency", "payment_term", "price_list", "sales_rep", "default_discount_rate", "credit_limit", "risk_limit", "balance", "risk_policy", "status", "created_at", "updated_at"}
	for _, field := range fields {
		for _, direction := range []string{"asc", "desc"} {
			t.Run(field+":"+direction, func(t *testing.T) {
				sortValue := field + ":" + direction
				all, err := service.ListSorted(ctx, session, "", "", 100, true, "", sortValue)
				if err != nil {
					t.Fatal(err)
				}
				var paged []Party
				cursor := ""
				for n := 0; n < 5; n++ {
					page, err := service.ListSorted(ctx, session, "", cursor, 2, true, "", sortValue)
					if err != nil {
						t.Fatal(err)
					}
					paged = append(paged, page.Items...)
					cursor = page.NextCursor
					if cursor == "" {
						break
					}
				}
				if len(paged) != 5 || !reflect.DeepEqual(paged, all.Items) || cursor != "" {
					t.Fatalf("pagination differs: paged=%d all=%d remaining=%q", len(paged), len(all.Items), cursor)
				}
			})
		}
	}
	ordered, err := service.ListSorted(ctx, session, "", "", 100, true, "", "credit_limit:asc,code:desc")
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, p := range ordered.Items {
		codes = append(codes, p.Code)
	}
	if !reflect.DeepEqual(codes, []string{"C04", "C02", "C01", "C03", "C05"}) {
		t.Fatalf("numeric/multiple sort: %v", codes)
	}
	// A sorted search still enforces the role filter and company scope.
	filtered, err := service.ListSorted(ctx, session, "Alfa", "", 2, false, "supplier", "code:asc")
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Code != "C01" {
		t.Fatalf("filter lost: %+v %v", filtered, err)
	}
	const groupID = "dbdbdbdb-0000-4000-8000-000000000001"
	if _, err = pool.Exec(ctx, `INSERT INTO party_groups(id,company_id,code,name) VALUES($1,$2,'FILTER-GROUP','Filtre grubu')`, groupID, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO party_group_memberships(company_id,party_id,group_id) VALUES($1,$2,$3)`, session.CurrentCompanyID, created[1].ID, groupID); err != nil {
		t.Fatal(err)
	}
	grouped, err := service.ListSorted(ctx, session, "Alfa", "", 2, false, "supplier", "code:asc", groupID)
	if err != nil || len(grouped.Items) != 1 || grouped.Items[0].ID != created[1].ID {
		t.Fatalf("group/role/search intersection: %+v %v", grouped, err)
	}
	// Existing consumers retain the default cursor shape and name ordering.
	defaults, err := service.List(ctx, session, "", "", 2, false)
	if err != nil || defaults.NextCursor == "" {
		t.Fatalf("default: %+v %v", defaults, err)
	}
	_, _, err = decodeCursor(defaults.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	expected := append([]Party{}, created...)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].DisplayName == expected[j].DisplayName {
			return expected[i].ID < expected[j].ID
		}
		return expected[i].DisplayName < expected[j].DisplayName
	})
	if defaults.Items[0].ID != expected[0].ID || defaults.Items[1].ID != expected[1].ID {
		t.Fatal("default ordering changed")
	}
}
