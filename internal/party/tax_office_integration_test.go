package party

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

func TestTaxOfficeReferencesSearchAndPartyCanonicalization(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool := partyTestPool(t, ctx, databaseURL)
	if err := migrations.New(pool).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{6}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{
		AdminName: "Vergi Dairesi Yönetici", AdminEmail: "tax-office@example.test",
		Password: "uzun-ve-guvenli-parola", LegalName: "Vergi Test AŞ", TradeName: "Vergi Test", EntityType: "LEGAL_ENTITY",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(pool)

	items, err := service.ListTaxOfficeReferences(ctx, session, 34, "Kadıköy", "kadık", 2000)
	if err != nil || len(items) == 0 {
		t.Fatalf("Kadıköy tax-office search failed: items=%+v err=%v", items, err)
	}
	for _, item := range items {
		if item.ProvinceID != 34 || item.DistrictName == "" || !strings.Contains(strings.ToLower(item.Name), "kad") {
			t.Fatalf("filtered result escaped its province/district/search: %+v", item)
		}
	}
	codeItems, err := service.ListTaxOfficeReferences(ctx, session, 0, "", "01250", 10)
	if err != nil || len(codeItems) != 1 || codeItems[0].Code == nil || *codeItems[0].Code != "01250" {
		t.Fatalf("official code search failed: items=%+v err=%v", codeItems, err)
	}
	if _, err = service.ListTaxOfficeReferences(ctx, session, 6, "Kadıköy", "", 10); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("invalid province/district combination returned %v", err)
	}

	selected := items[0]
	created, err := service.Create(ctx, session, Input{
		Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Kanonik Cari", LegalName: "Kanonik Cari AŞ",
		TaxOfficeID: selected.ID, TaxOffice: "Kullanıcının uydurduğu ad", DefaultCurrency: "TRY", RiskPolicy: "WARN",
	}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if created.TaxOfficeID == nil || *created.TaxOfficeID != selected.ID || created.TaxOffice == nil || *created.TaxOffice != selected.Name {
		t.Fatalf("selected tax office was not canonicalized: %+v", created)
	}
	if _, err = service.Create(ctx, session, Input{
		Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Geçersiz Cari", LegalName: "Geçersiz Cari AŞ",
		TaxOfficeID: "00000000-0000-4000-8000-000000000099", DefaultCurrency: "TRY", RiskPolicy: "WARN",
	}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("unknown tax-office identity returned %v", err)
	}

	if _, err = pool.Exec(ctx, `UPDATE turkish_tax_offices SET is_active=false WHERE id=$1`, selected.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(ctx, session, Input{
		Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Pasif Cari", LegalName: "Pasif Cari AŞ",
		TaxOfficeID: selected.ID, DefaultCurrency: "TRY", RiskPolicy: "WARN",
	}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("inactive tax-office identity returned %v", err)
	}
	updatedInput := Input{
		Code: created.Code, Kind: created.Kind, IsCustomer: true, DisplayName: "Kanonik Cari Güncel", LegalName: "Kanonik Cari Güncel AŞ",
		TaxOfficeID: selected.ID, TaxOffice: "değiştirilmiş ad", DefaultCurrency: "TRY", RiskPolicy: "WARN",
	}
	updated, err := service.Update(ctx, session, created.ID, created.Version, updatedInput, identity.RequestMeta{})
	if err != nil || updated.TaxOffice == nil || *updated.TaxOffice != selected.Name {
		t.Fatalf("existing inactive reference could not round-trip canonically: %+v err=%v", updated, err)
	}
}
