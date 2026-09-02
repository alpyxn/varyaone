package products

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/platform/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestProductCatalogIsolationSearchAndLifecycle(t *testing.T) {
	databaseURL := os.Getenv("VARYAONE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VARYAONE_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p := productTestPool(t, ctx, databaseURL)
	if err := migrations.New(p).Up(ctx); err != nil {
		t.Fatal(err)
	}
	identityService, err := identity.NewService(p, bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	session, err := identityService.Setup(ctx, identity.SetupInput{AdminName: "Ürün Yönetici", AdminEmail: "product@example.test", Password: "uzun-ve-guvenli-parola", LegalName: "Ürün Test AŞ", TradeName: "Ürün Test", EntityType: "LEGAL_ENTITY"}, identity.RequestMeta{TraceID: "product-test"})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(p)
	definition, err := service.CreateVariantDefinition(ctx, session, VariantDefinitionInput{
		Code: "RENK",
		Name: "Renk",
	}, identity.RequestMeta{TraceID: "variant-definition-create"})
	if err != nil || definition.ID == "" || definition.Code != "RENK" || definition.Name != "Renk" {
		t.Fatalf("variant definition create did not return the committed record: %+v err=%v", definition, err)
	}
	category, err := service.CreateCategory(ctx, session, ReferenceInput{Code: "KAHVE", Name: "Kahve"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	brand, err := service.CreateBrand(ctx, session, ReferenceInput{Code: "VARYA", Name: "Varya"}, identity.RequestMeta{})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, session, Input{
		Name:        "Varya Filtre Kahve",
		Kind:        "PHYSICAL",
		Description: "Kavrulmuş çekirdek",
		CategoryID:  category.ID,
		BrandID:     brand.ID,
		BaseUnit:    "ADET",
		Units: []UnitInput{
			{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)},
		},
		Barcodes: []BarcodeInput{{Barcode: "869000000001", BarcodeType: "EAN"}, {Barcode: "VARYA-001", BarcodeType: "OTHER"}},
	}, Scope{}, identity.RequestMeta{TraceID: "product-create"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Code != "STK000001" || created.SKU != created.Code || len(created.Units) != 1 || len(created.Barcodes) != 2 || created.CategoryName != "Kahve" || created.BrandName != "Varya" {
		t.Fatalf("product details were not preserved: %+v", created)
	}
	if _, err = service.Create(ctx, session, Input{
		Code:     "MULTI-UNIT",
		Name:     "Çoklu Birim Reddedilir",
		Kind:     "PHYSICAL",
		BaseUnit: "ADET",
		Units: []UnitInput{
			{Code: "ADET", IsBase: true, ConversionFactor: "1"},
			{Code: "KOLI", ConversionFactor: "12"},
		},
	}, Scope{}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "yalnızca bir stok birimi") {
		t.Fatalf("multiple stock units were accepted: %v", err)
	}
	manual, err := service.Create(ctx, session, Input{
		Code:     "MANUAL-EXPLICIT",
		AutoCode: true, // Eski istemcilerden gelebilir; manuel kodu ezmemeli.
		Name:     "Elle Kodlanan Ürün",
		Kind:     "PHYSICAL",
		BaseUnit: "ADET",
		Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}},
	}, Scope{}, identity.RequestMeta{})
	if err != nil || manual.Code != "MANUAL-EXPLICIT" {
		t.Fatalf("explicit product code was not preserved: %+v err=%v", manual, err)
	}
	reserved, err := service.Create(ctx, session, Input{
		Code:     "STK000002",
		Name:     "Seri Kodunu Kullanan Ürün",
		Kind:     "PHYSICAL",
		BaseUnit: "ADET",
		Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}},
	}, Scope{}, identity.RequestMeta{})
	if err != nil || reserved.Code != "STK000002" {
		t.Fatalf("reserved sequence code could not be created: %+v err=%v", reserved, err)
	}
	afterReserved, err := service.Create(ctx, session, Input{
		Name:     "Seri Kodu Atlayan Ürün",
		Kind:     "PHYSICAL",
		BaseUnit: "ADET",
		Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}},
	}, Scope{}, identity.RequestMeta{})
	if err != nil || afterReserved.Code != "STK000003" {
		t.Fatalf("automatic code did not skip an occupied sequence code: %+v err=%v", afterReserved, err)
	}
	var concurrentManual Product
	var concurrentAutomatic Product
	var manualErr, automaticErr error
	var codeRace sync.WaitGroup
	codeRace.Add(2)
	go func() {
		defer codeRace.Done()
		concurrentManual, manualErr = service.Create(ctx, session, Input{
			Code:     "STK000004",
			Name:     "Eşzamanlı Elle Kodlanan Ürün",
			Kind:     "PHYSICAL",
			BaseUnit: "ADET",
			Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}},
		}, Scope{}, identity.RequestMeta{})
	}()
	go func() {
		defer codeRace.Done()
		concurrentAutomatic, automaticErr = service.Create(ctx, session, Input{
			Name:     "Eşzamanlı Otomatik Kodlanan Ürün",
			Kind:     "PHYSICAL",
			BaseUnit: "ADET",
			Units:    []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}},
		}, Scope{}, identity.RequestMeta{})
	}()
	codeRace.Wait()
	if automaticErr != nil {
		t.Fatalf("automatic product code lost a manual-code race: %v", automaticErr)
	}
	if manualErr != nil && !errors.Is(manualErr, identity.ErrValidation) {
		t.Fatalf("manual product code race returned unexpected error: %v", manualErr)
	}
	if manualErr == nil && concurrentManual.Code == concurrentAutomatic.Code {
		t.Fatalf("manual and automatic product codes collided: %q", concurrentManual.Code)
	}
	byBarcode, err := service.List(ctx, session, ListOptions{Query: "869000000001", Limit: 20})
	if err != nil || len(byBarcode.Items) != 1 || byBarcode.Items[0].ID != created.ID {
		t.Fatalf("barcode search failed: %+v err=%v", byBarcode.Items, err)
	}
	byCategoryAndBrand, err := service.List(ctx, session, ListOptions{CategoryID: category.ID, BrandID: brand.ID, Limit: 20})
	if err != nil || len(byCategoryAndBrand.Items) != 1 || byCategoryAndBrand.Items[0].ID != created.ID {
		t.Fatalf("category and brand filters failed: %+v err=%v", byCategoryAndBrand.Items, err)
	}
	byAllFields, err := service.List(ctx, session, ListOptions{Query: "Varya Kavrulmuş Kahve", Limit: 20})
	if err != nil || len(byAllFields.Items) != 1 {
		t.Fatalf("all-field product search failed: %+v err=%v", byAllFields.Items, err)
	}
	if punctuation, err := service.List(ctx, session, ListOptions{Query: "---", Limit: 20}); err != nil || len(punctuation.Items) != 0 {
		t.Fatalf("punctuation search became broad: %+v err=%v", punctuation.Items, err)
	}
	if _, err = service.Create(ctx, session, Input{Code: "MANUAL-1", Name: "İkinci", Kind: "SERVICE", BaseUnit: "SAAT", Units: []UnitInput{{Code: "SAAT", IsBase: true, ConversionFactor: "1"}}, Barcodes: []BarcodeInput{{Barcode: "869000000001"}}}, Scope{}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "barkod") {
		t.Fatalf("duplicate barcode returned %v", err)
	}
	updatedInput := Input{Code: created.Code, Name: "Varya Filtre Kahve Güncel", Kind: "PHYSICAL", Description: created.Description, CategoryID: category.ID, BrandID: brand.ID, BaseUnit: "ADET", Units: []UnitInput{{Code: "ADET", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(0)}}, Barcodes: []BarcodeInput{{Barcode: "869000000001", IsPrimary: true}}}
	updated, err := service.Update(ctx, session, created.ID, created.Version, updatedInput, Scope{}, identity.RequestMeta{TraceID: "product-update"})
	if err != nil || updated.Version != 2 || updated.Name != updatedInput.Name {
		t.Fatalf("product update failed: %+v err=%v", updated, err)
	}
	changedUnitInput := updatedInput
	changedUnitInput.BaseUnit = "KOLI"
	changedUnitInput.Units = []UnitInput{{Code: "KOLI", IsBase: true, ConversionFactor: "1", DecimalScale: ptr(3)}}
	if _, err = service.Update(ctx, session, created.ID, updated.Version, changedUnitInput, Scope{}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "stok birimi değiştirilemez") {
		t.Fatalf("base unit change was accepted: %v", err)
	}
	unchanged, getErr := service.Get(ctx, session, created.ID, Scope{})
	if getErr != nil || unchanged.Version != updated.Version || len(unchanged.Units) != 1 || unchanged.Units[0].Code != "ADET" {
		t.Fatalf("rejected base unit change mutated the product: %+v err=%v", unchanged, getErr)
	}
	emptyCodeUpdate := updatedInput
	emptyCodeUpdate.Code = ""
	emptyCodeUpdate.SKU = ""
	if _, err = service.Update(ctx, session, created.ID, updated.Version, emptyCodeUpdate, Scope{}, identity.RequestMeta{}); !errors.Is(err, identity.ErrValidation) || !strings.Contains(err.Error(), "stok kodu") {
		t.Fatalf("blank product code was accepted during update: %v", err)
	}
	if _, err = service.Update(ctx, session, created.ID, created.Version, updatedInput, Scope{}, identity.RequestMeta{}); !errors.Is(err, identity.ErrConflict) {
		t.Fatalf("stale product update returned %v", err)
	}
	deactivated, err := service.Deactivate(ctx, session, created.ID, updated.Version, Scope{}, identity.RequestMeta{TraceID: "product-deactivate"})
	if err != nil || deactivated.IsActive {
		t.Fatalf("product deactivation failed: %+v err=%v", deactivated, err)
	}
	activeOnly, err := service.List(ctx, session, ListOptions{Query: "Varya", Limit: 20})
	if err != nil || len(activeOnly.Items) != 0 {
		t.Fatalf("inactive product leaked into active list: %+v err=%v", activeOnly.Items, err)
	}
	allProducts, err := service.List(ctx, session, ListOptions{Query: "Varya", IncludeInactive: true, Limit: 20})
	if err != nil || len(allProducts.Items) != 1 || allProducts.Items[0].ID != created.ID {
		t.Fatalf("inactive product was not returned by explicit filter: %+v err=%v", allProducts.Items, err)
	}
	foreignSession := session
	foreignSession.CurrentCompanyID = uuid.NewString()
	foreignList, listErr := service.List(ctx, foreignSession, ListOptions{})
	if listErr != nil || len(foreignList.Items) != 0 {
		t.Fatalf("foreign company product leaked: %+v err=%v", foreignList.Items, listErr)
	}
	if _, err = service.Get(ctx, foreignSession, created.ID, Scope{}); !errors.Is(err, identity.ErrForbidden) {
		t.Fatalf("foreign company product access returned %v", err)
	}
}

func ptr(value int) *int { return &value }

func productTestPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	base, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("varya_product_test_%d", time.Now().UnixNano())
	if _, err = base.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	p, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.Close()
		_, _ = base.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		base.Close()
	})
	return p
}
