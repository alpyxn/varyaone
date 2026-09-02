package party

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPartyIsolationAutoNumberAndImmutableLedger(t *testing.T) {
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
	identityService, err := identity.NewService(pool, bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Cari Yönetici", AdminEmail: "cari@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Cari Test AŞ", TradeName: "Cari Test", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "party-test"})
	if err != nil {
		t.Fatal(err)
	}
	if !session.HasPermission("party.create") {
		t.Fatal("system administrator did not receive Phase 0.2 permissions")
	}
	service := NewService(pool)
	var (
		istanbulProvinceID     int64 = 34
		kadikoyDistrictID      int64
		caferagaNeighborhoodID int64
	)
	if err = pool.QueryRow(ctx, `SELECT id FROM turkish_districts WHERE province_id=$1 AND name='Kadıköy'`, istanbulProvinceID).Scan(&kadikoyDistrictID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT id FROM turkish_neighborhoods WHERE district_id=$1 AND name='Caferağa Mah.' ORDER BY id LIMIT 1`, kadikoyDistrictID).Scan(&caferagaNeighborhoodID); err != nil {
		t.Fatal(err)
	}
	provinces, err := service.ListTurkishProvinces(ctx, session)
	if err != nil || len(provinces) != 81 {
		t.Fatalf("turkish province reference is incomplete: count=%d err=%v", len(provinces), err)
	}
	if provinces[33].Name != "İstanbul" || provinces[33].PlateCode != "34" {
		t.Fatalf("Istanbul province reference is incorrect: item=%+v", provinces[33])
	}
	districts, err := service.ListTurkishDistricts(ctx, session, istanbulProvinceID, "Kadık", 25)
	if err != nil || len(districts) != 1 || districts[0].ID != kadikoyDistrictID {
		t.Fatalf("district reference lookup failed: items=%+v err=%v", districts, err)
	}
	neighborhoods, err := service.ListTurkishNeighborhoods(ctx, session, kadikoyDistrictID, "Cafer", 25)
	if err != nil || len(neighborhoods) != 1 || neighborhoods[0].ID != caferagaNeighborhoodID {
		t.Fatalf("neighborhood reference lookup failed: items=%+v err=%v", neighborhoods, err)
	}
	preference, err := service.GetAddressPreference(ctx, session)
	if err != nil || preference.ProvinceID != nil {
		t.Fatalf("empty address preference returned %+v err=%v", preference, err)
	}
	savedPreference, err := service.SaveAddressPreference(ctx, session, AddressPreference{ProvinceID: &istanbulProvinceID, DistrictID: &kadikoyDistrictID, NeighborhoodID: &caferagaNeighborhoodID})
	if err != nil || savedPreference.ProvinceName != "İstanbul" || savedPreference.DistrictName != "Kadıköy" || savedPreference.NeighborhoodName != "Caferağa Mah." {
		t.Fatalf("address preference was not saved canonically: %+v err=%v", savedPreference, err)
	}
	reloadedPreference, err := service.GetAddressPreference(ctx, session)
	if err != nil || reloadedPreference.ProvinceID == nil || *reloadedPreference.ProvinceID != istanbulProvinceID || reloadedPreference.DistrictID == nil || *reloadedPreference.DistrictID != kadikoyDistrictID {
		t.Fatalf("saved address preference was not isolated/read back: %+v err=%v", reloadedPreference, err)
	}
	var badDistrictID int64
	if err = pool.QueryRow(ctx, `SELECT id FROM turkish_districts WHERE province_id=62 ORDER BY id LIMIT 1`).Scan(&badDistrictID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveAddressPreference(ctx, session, AddressPreference{ProvinceID: &istanbulProvinceID, DistrictID: &badDistrictID}); !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("cross-province address preference returned %v", err)
	}
	clearedPreference, err := service.SaveAddressPreference(ctx, session, AddressPreference{})
	if err != nil || clearedPreference.ProvinceID != nil || clearedPreference.DistrictID != nil || clearedPreference.NeighborhoodID != nil {
		t.Fatalf("address preference could not be cleared: %+v err=%v", clearedPreference, err)
	}

	inputs := []Input{
		{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Alfa Müşteri", LegalName: "Alfa Müşteri AŞ", TaxNumber: "1234567890", DefaultCurrency: "TRY", CreditLimit: "1000.2500", RiskLimit: "800", RiskPolicy: "BLOCK"},
		{Kind: "PERSON", IsSupplier: true, DisplayName: "Zeynep Tedarik", FirstName: "Zeynep", LastName: "Tedarik", IdentityNumber: "12345678901", DefaultCurrency: "TRY", RiskPolicy: "WARN"},
	}
	created := make([]Party, len(inputs))
	errs := make([]error, len(inputs))
	var group sync.WaitGroup
	for index := range inputs {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			created[i], errs[i] = service.Create(ctx, session, inputs[i], identity.RequestMeta{TraceID: "concurrent-create"})
		}(index)
	}
	group.Wait()
	for _, err = range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if created[0].Code == created[1].Code || created[0].Code == "" || created[1].Code == "" {
		t.Fatalf("automatic codes were not unique: %q %q", created[0].Code, created[1].Code)
	}
	reserved, err := service.Create(ctx, session, Input{
		Code:            "CR000003",
		Kind:            "ORGANIZATION",
		IsCustomer:      true,
		DisplayName:     "Elle Kodlanan Cari",
		LegalName:       "Elle Kodlanan Cari Ltd",
		DefaultCurrency: "TRY",
		RiskPolicy:      "WARN",
	}, identity.RequestMeta{})
	if err != nil || reserved.Code != "CR000003" {
		t.Fatalf("reserved party sequence code could not be created: %+v err=%v", reserved, err)
	}
	afterReserved, err := service.Create(ctx, session, Input{
		Kind:            "ORGANIZATION",
		IsCustomer:      true,
		DisplayName:     "Seri Kodu Atlayan Cari",
		LegalName:       "Seri Kodu Atlayan Cari Ltd",
		DefaultCurrency: "TRY",
		RiskPolicy:      "WARN",
	}, identity.RequestMeta{})
	if err != nil || afterReserved.Code != "CR000004" {
		t.Fatalf("automatic party code did not skip an occupied sequence code: %+v err=%v", afterReserved, err)
	}
	updatedInput := inputs[0]
	updatedInput.Code = created[0].Code
	updatedInput.DisplayName = "Alfa Müşteri Revize"
	updatedInput.LegalName = "Alfa Müşteri Revize AŞ"
	updated, err := service.Update(ctx, session, created[0].ID, created[0].Version, updatedInput, identity.RequestMeta{TraceID: "party-update"})
	if err != nil {
		t.Fatal(err)
	}
	codeMutation := updatedInput
	codeMutation.Code = "MANUAL-CHANGE"
	if _, err = service.Update(ctx, session, updated.ID, updated.Version, codeMutation, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "cari kodu oluşturulduktan sonra değiştirilemez") {
		t.Fatalf("code mutation returned %v", err)
	}
	kindMutation := updatedInput
	kindMutation.Kind = "PERSON"
	kindMutation.FirstName = "Alfa"
	kindMutation.LastName = "Müşteri"
	if _, err = service.Update(ctx, session, updated.ID, updated.Version, kindMutation, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "cari türü oluşturulduktan sonra değiştirilemez") {
		t.Fatalf("kind mutation returned %v", err)
	}
	duplicate, err := service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Alfa Şube", LegalName: "Alfa Şube Ltd", TaxNumber: "1234567890", DefaultCurrency: "TRY", RiskPolicy: "WARN"}, identity.RequestMeta{})
	if err != nil || len(duplicate.Warnings) != 1 {
		t.Fatalf("WARN duplicate policy result=%+v err=%v", duplicate.Warnings, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE companies SET duplicate_party_tax_number_policy='BLOCK' WHERE id=$1`, session.CurrentCompanyID); err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Engellenen", LegalName: "Engellenen Ltd", TaxNumber: "1234567890", DefaultCurrency: "TRY", RiskPolicy: "WARN"}, identity.RequestMeta{})
	if !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("BLOCK duplicate policy returned %v", err)
	}
	term, err := service.CreatePaymentTerm(ctx, session, PaymentTerm{Code: "NET30", Name: "30 Gün", DueDays: 30}, identity.RequestMeta{TraceID: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	partyGroup, err := service.CreateGroup(ctx, session, Group{Code: "OZEL-BAYI", Name: "Özel Bayiler"}, identity.RequestMeta{TraceID: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	deactivatedGroup, err := service.DeactivateGroup(ctx, session, partyGroup.ID, partyGroup.Version, identity.RequestMeta{TraceID: "settings-deactivate"})
	if err != nil || deactivatedGroup.IsActive || deactivatedGroup.Version != partyGroup.Version+1 {
		t.Fatalf("group deactivation result=%+v err=%v", deactivatedGroup, err)
	}
	if _, err = service.ActivateGroup(ctx, session, partyGroup.ID, partyGroup.Version, identity.RequestMeta{TraceID: "settings-stale-activate"}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("stale group activation returned %v", err)
	}
	activatedGroup, err := service.ActivateGroup(ctx, session, deactivatedGroup.ID, deactivatedGroup.Version, identity.RequestMeta{TraceID: "settings-activate"})
	if err != nil || !activatedGroup.IsActive || activatedGroup.Version != deactivatedGroup.Version+1 {
		t.Fatalf("group activation result=%+v err=%v", activatedGroup, err)
	}
	deactivatedParty, err := service.Deactivate(ctx, session, afterReserved.ID, afterReserved.Version, identity.RequestMeta{TraceID: "party-deactivate"})
	if err != nil || deactivatedParty.IsActive {
		t.Fatalf("party deactivation result=%+v err=%v", deactivatedParty, err)
	}
	if _, err = service.Activate(ctx, session, afterReserved.ID, afterReserved.Version, identity.RequestMeta{TraceID: "party-stale-activate"}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("stale party activation returned %v", err)
	}
	activatedParty, err := service.Activate(ctx, session, deactivatedParty.ID, deactivatedParty.Version, identity.RequestMeta{TraceID: "party-activate"})
	if err != nil || !activatedParty.IsActive || activatedParty.Version != deactivatedParty.Version+1 {
		t.Fatalf("party activation result=%+v err=%v", activatedParty, err)
	}
	_, err = service.CreateCustomField(ctx, session, CustomFieldDefinition{Code: "bolge", Name: "Bölge", FieldType: "SELECT", SelectOptions: []string{"Marmara", "Ege"}, IsRequired: true}, identity.RequestMeta{TraceID: "settings"})
	if err != nil {
		t.Fatal(err)
	}
	customValue, _ := json.Marshal("Marmara")
	withDetails, err := service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Detaylı Cari", LegalName: "Detaylı Cari Ltd", TradeName: "Detaylı Ticari Ad", DefaultCurrency: "TRY", RiskPolicy: "WARN", PaymentTermID: term.ID, SalesRepUserID: session.User.ID, GroupIDs: []string{partyGroup.ID}, Tags: []string{"Öncelikli"}, Addresses: []Address{{AddressLine: "Örnek Sokak 1", ProvinceID: &istanbulProvinceID, DistrictID: &kadikoyDistrictID, NeighborhoodID: &caferagaNeighborhoodID, IsDefault: true}}, Contacts: []Contact{{FullName: "Ayşe İlgili", Email: "ayse@example.test", Phone: "0212 555 00 00", IsPrimary: true}}, CustomFields: map[string]json.RawMessage{"bolge": customValue}}, identity.RequestMeta{TraceID: "details"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "İki Adres", LegalName: "İki Adres Ltd", DefaultCurrency: "TRY", RiskPolicy: "WARN", Addresses: []Address{{AddressLine: "Birinci adres", City: "İstanbul", IsDefault: true}, {AddressLine: "İkinci adres", City: "Ankara"}}}, identity.RequestMeta{})
	if !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "yalnızca bir adres") {
		t.Fatalf("multiple addresses were accepted: %v", err)
	}
	if len(withDetails.Addresses) != 1 || withDetails.Addresses[0].ProvinceName != "İstanbul" || withDetails.Addresses[0].DistrictName != "Kadıköy" || withDetails.Addresses[0].NeighborhoodName != "Caferağa Mah." || len(withDetails.Contacts) != 1 || len(withDetails.Groups) != 1 || len(withDetails.Tags) != 1 || withDetails.CustomFields["bolge"] != "Marmara" {
		t.Fatalf("party details were not preserved: %+v", withDetails)
	}
	listed, err := service.List(ctx, session, "Detaylı", "", 25, false)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].TradeName == nil || *listed.Items[0].TradeName != "Detaylı Ticari Ad" || listed.Items[0].LegalName == nil || *listed.Items[0].LegalName != "Detaylı Cari Ltd" || listed.Items[0].DisplayName != "Detaylı Ticari Ad" || listed.Items[0].City != "İstanbul" || listed.Items[0].Phone != "0212 555 00 00" || listed.Items[0].Email != "ayse@example.test" || listed.Items[0].AddressSummary != "Örnek Sokak 1 · İstanbul · Kadıköy · Caferağa Mah." || listed.Items[0].GroupSummary != "Özel Bayiler" || listed.Items[0].TagSummary != "Öncelikli" || listed.Items[0].CustomFieldSummary != "Bölge: Marmara" || listed.Items[0].Balance != "0" {
		t.Fatalf("party list presentation fields are incomplete: items=%+v err=%v", listed.Items, err)
	}
	for _, search := range []string{"ayse@example.test", "Örnek Sokak", "Özel Bayiler", "Öncelikli", "Marmara", "Detaylı İstanbul", "Kadıköy", "Caferağa", "TİCARİ AD", "NET30", "30 Gün", "Cari Yönetici"} {
		searched, searchErr := service.List(ctx, session, search, "", 25, false)
		if searchErr != nil || len(searched.Items) != 1 || searched.Items[0].ID != withDetails.ID {
			t.Fatalf("all-field party search %q returned items=%+v err=%v", search, searched.Items, searchErr)
		}
	}
	formattedCode := created[0].Code
	if len(formattedCode) > 2 {
		formattedCode = formattedCode[:2] + "-" + formattedCode[2:]
	}
	formattedCodeResult, err := service.List(ctx, session, formattedCode, "", 25, false)
	if err != nil || len(formattedCodeResult.Items) != 1 || formattedCodeResult.Items[0].ID != created[0].ID {
		t.Fatalf("formatted party code search %q returned items=%+v err=%v", formattedCode, formattedCodeResult.Items, err)
	}
	punctuationOnly, err := service.List(ctx, session, "---", "", 25, false)
	if err != nil || len(punctuationOnly.Items) != 0 {
		t.Fatalf("punctuation-only search returned items=%+v err=%v", punctuationOnly.Items, err)
	}
	_, err = service.Create(ctx, session, Input{Kind: "ORGANIZATION", IsCustomer: true, DisplayName: "Eksik Özel Alan", LegalName: "Eksik Ltd", DefaultCurrency: "TRY", RiskPolicy: "WARN"}, identity.RequestMeta{})
	if !errors.Is(err, identity.ErrValidation) {
		t.Fatalf("required custom field returned %v", err)
	}

	const companyB = "20000000-0000-4000-8000-000000000001"
	if _, err = pool.Exec(ctx, `INSERT INTO companies(id,legal_name,trade_name,entity_type) VALUES($1,'Başka Firma','Başka','LEGAL_ENTITY')`, companyB); err != nil {
		t.Fatal(err)
	}
	foreign := session
	foreign.CurrentCompanyID = companyB
	if _, err = service.Get(ctx, foreign, created[0].ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-company party access returned %v", err)
	}
	if _, err = service.Balances(ctx, foreign, created[0].ID); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-company party balance access returned %v", err)
	}
	if _, err = service.Statement(ctx, foreign, created[0].ID, time.Now().AddDate(-1, 0, 0), time.Now().AddDate(1, 0, 0), 100); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("cross-company party statement access returned %v", err)
	}

	entry := LedgerEntry{PartyID: created[0].ID, Currency: "TRY", EntryType: "TEST_INVOICE", SourceType: "test", SourceID: "30000000-0000-4000-8000-000000000001", IdempotencyKey: "invoice:1", Description: "Test satış faturası", Debit: "100.1250", Credit: "0", ExchangeRate: "1", DocumentDate: time.Now()}
	posted, err := service.PostLedgerEntry(ctx, session, entry, identity.RequestMeta{TraceID: "posting"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := service.PostLedgerEntry(ctx, session, entry, identity.RequestMeta{TraceID: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != posted.ID {
		t.Fatalf("idempotent retry created another entry: %s != %s", replayed.ID, posted.ID)
	}
	if _, err = pool.Exec(ctx, `UPDATE party_ledger_entries SET description='changed' WHERE company_id=$1 AND id=$2`, session.CurrentCompanyID, posted.ID); err == nil {
		t.Fatal("database allowed posted party ledger mutation")
	}
	balanceSearch, err := service.List(ctx, session, "100.1250", "", 25, false)
	if err != nil || len(balanceSearch.Items) != 1 || balanceSearch.Items[0].ID != created[0].ID {
		t.Fatalf("decimal balance search returned items=%+v err=%v", balanceSearch.Items, err)
	}
	reversalID := posted.ID
	_, err = service.PostLedgerEntry(ctx, session, LedgerEntry{PartyID: created[0].ID, Currency: "TRY", EntryType: "REVERSAL", SourceType: "test", SourceID: "30000000-0000-4000-8000-000000000002", IdempotencyKey: "invoice:1:reverse", Description: "Test ters kayıt", Debit: "0", Credit: "100.1250", ExchangeRate: "1", DocumentDate: time.Now(), ReversalOfID: &reversalID}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	balances, err := service.Balances(ctx, session, created[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(balances) != 1 || balances[0].Balance != "0.0000" {
		t.Fatalf("reversal did not close balance: %+v", balances)
	}
	statement, err := service.Statement(ctx, session, created[0].ID, time.Now().AddDate(-1, 0, 0), time.Now().AddDate(1, 0, 0), 100)
	if err != nil || len(statement) != 2 || statement[0].ReversalOfID == nil {
		t.Fatalf("immutable statement did not include original and reversal: items=%+v err=%v", statement, err)
	}
}

func partyTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_party_test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return pool
}
