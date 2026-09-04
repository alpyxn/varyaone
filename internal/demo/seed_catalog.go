package demo

import (
	"context"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/products"
)

// seedWarehouses adds the second stock location. Company provisioning creates
// one warehouse; a single-warehouse demo cannot show a transfer, a per-warehouse
// stock report or a document that ships from somewhere other than the main
// depot. Its code sorts after the provisioned "ANA", so readScope still reports
// the main warehouse as the default one.
func (r *Runner) seedWarehouses(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	store, err := svc.inventory.CreateWarehouse(ctx, inventory.WarehouseInput{
		CompanyID: CompanyID, BranchID: built.scope.branchID,
		Code: "KDK", Name: "Kadıköy Mağaza Deposu", Type: inventory.WarehouseStandard,
		Address: "Kadıköy / İstanbul", ActorUserID: session.User.ID,
	})
	if err != nil {
		return err
	}
	built.scope.storeID = store.ID
	return nil
}

type productSpec struct {
	code     string
	name     string
	category string
	brand    string
	unit     string
	purchase string
	sales    string
	// variantDefinition names the attribute this product is tracked by. An
	// empty value keeps the product single-SKU; a set one means every document
	// line for it must carry one of the generated variants.
	variantDefinition string
}

var physicalProducts = []productSpec{
	{"URN-001", "Kablosuz Klavye", "Elektronik", "Aurora", "ADET", "480", "899", "RENK"},
	{"URN-002", "Kablosuz Mouse", "Elektronik", "Aurora", "ADET", "260", "499", ""},
	{"URN-003", "27\" IPS Monitör", "Elektronik", "Nordis", "ADET", "4750", "7990", ""},
	{"URN-004", "USB-C Çoklayıcı", "Elektronik", "Aurora", "ADET", "620", "1190", ""},
	{"URN-005", "Dizüstü Standı", "Ofis", "Nordis", "ADET", "310", "649", ""},
	{"URN-006", "Ofis Sandalyesi", "Ofis", "Nordis", "ADET", "3200", "5750", "RENK"},
	{"URN-007", "Yükseklik Ayarlı Masa", "Ofis", "Nordis", "ADET", "8400", "13900", ""},
	{"URN-008", "A4 Fotokopi Kağıdı", "Ofis", "", "PAKET", "145", "235", ""},
	{"URN-009", "Toner Kartuş", "Ofis", "", "ADET", "1250", "1990", ""},
	{"URN-010", "Ağ Anahtarı 8 Port", "Elektronik", "Nordis", "ADET", "1450", "2450", ""},
	{"URN-011", "Harici SSD", "Elektronik", "Aurora", "ADET", "1980", "3190", "KAPASITE"},
	{"URN-012", "Web Kamerası", "Elektronik", "Aurora", "ADET", "890", "1590", ""},
}

var serviceProducts = []productSpec{
	{"HZM-001", "Kurulum ve Devreye Alma", "Hizmet", "", "SAAT", "0", "1250", ""},
	{"HZM-002", "Yıllık Bakım Sözleşmesi", "Hizmet", "", "ADET", "0", "18500", ""},
}

// variantDefinitionSpecs are the company-wide variant attributes. A demo
// without them cannot show the variant catalogue, per-variant stock or a
// variant column on any document.
var variantDefinitionSpecs = []products.VariantDefinitionInput{
	{Code: "RENK", Name: "Renk", Options: []products.VariantOptionInput{
		{Code: "SIYAH", Name: "Siyah", ShortCode: "SYH", SortOrder: 1},
		{Code: "BEYAZ", Name: "Beyaz", ShortCode: "BYZ", SortOrder: 2},
		{Code: "GRI", Name: "Gri", ShortCode: "GRI", SortOrder: 3},
		{Code: "LACIVERT", Name: "Lacivert", ShortCode: "LCV", SortOrder: 4},
	}},
	{Code: "KAPASITE", Name: "Kapasite", Options: []products.VariantOptionInput{
		{Code: "512GB", Name: "512 GB", ShortCode: "512", SortOrder: 1},
		{Code: "1TB", Name: "1 TB", ShortCode: "1TB", SortOrder: 2},
		{Code: "2TB", Name: "2 TB", ShortCode: "2TB", SortOrder: 3},
	}},
}

func (r *Runner) seedProducts(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	categories := map[string]string{}
	for _, name := range []string{"Elektronik", "Ofis", "Hizmet"} {
		category, err := svc.products.CreateCategory(ctx, session, products.ReferenceInput{Name: name}, seedMeta("category-"+name))
		if err != nil {
			return err
		}
		categories[name] = category.ID
	}
	brands := map[string]string{}
	for _, name := range []string{"Aurora", "Nordis"} {
		brand, err := svc.products.CreateBrand(ctx, session, products.ReferenceInput{Name: name}, seedMeta("brand-"+name))
		if err != nil {
			return err
		}
		brands[name] = brand.ID
	}
	definitions := map[string]products.VariantDefinition{}
	for _, spec := range variantDefinitionSpecs {
		definition, err := svc.products.CreateVariantDefinition(ctx, session, spec, seedMeta("variant-definition-"+spec.Code))
		if err != nil {
			return err
		}
		definitions[spec.Code] = definition
	}
	create := func(spec productSpec, kind string) (seededProduct, error) {
		created, err := svc.products.Create(ctx, session, products.Input{
			Code: spec.code, Name: spec.name, Kind: kind, BaseUnit: spec.unit,
			PurchasePrice: spec.purchase, SalesPrice: spec.sales,
			PurchaseTaxRate: "20", SalesTaxRate: "20",
			CategoryID: categories[spec.category], BrandID: brands[spec.brand],
		}, products.Scope{BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID}, seedMeta("product-"+spec.code))
		if err != nil {
			return seededProduct{}, err
		}
		product := seededProduct{Product: created, spec: spec}
		if spec.variantDefinition == "" {
			return product, nil
		}
		// Variant mode is switched on before the product has any movement:
		// afterwards the identity of a stocked product is locked, exactly as it
		// is for a user.
		product.variants, err = r.enableVariants(ctx, session, svc, created, definitions[spec.variantDefinition])
		return product, err
	}
	for _, spec := range physicalProducts {
		product, err := create(spec, "PHYSICAL")
		if err != nil {
			return err
		}
		built.products = append(built.products, product)
	}
	for _, spec := range serviceProducts {
		product, err := create(spec, "SERVICE")
		if err != nil {
			return err
		}
		built.services = append(built.services, product)
	}
	return nil
}

// enableVariants puts one product under a variant definition and generates the
// full combination set, which for a single definition is one variant per
// option.
func (r *Runner) enableVariants(ctx context.Context, session identity.Session, svc *services, product products.Product, definition products.VariantDefinition) ([]products.Variant, error) {
	optionIDs := make([]string, 0, len(definition.Options))
	for _, option := range definition.Options {
		optionIDs = append(optionIDs, option.ID)
	}
	enabled := true
	if _, err := svc.products.UpdateVariantConfig(ctx, session, product.ID, products.ProductVariantConfigInput{
		VariantsEnabled: &enabled,
		Definitions: []products.ProductVariantDefinitionInput{
			{DefinitionID: definition.ID, Position: 1, OptionIDs: optionIDs},
		},
	}, product.Version, seedMeta("variant-config-"+product.Code)); err != nil {
		return nil, err
	}
	return svc.products.GenerateVariants(ctx, session, product.ID, seedMeta("variant-generate-"+product.Code))
}

type partySpec struct {
	name     string
	kind     string
	taxNo    string
	customer bool
	supplier bool
}

var partySpecs = []partySpec{
	{"Deniz Bilişim Ltd. Şti.", "ORGANIZATION", "1234567801", true, false},
	{"Ege Ofis Çözümleri A.Ş.", "ORGANIZATION", "1234567802", true, false},
	{"Kuzey Yapı Market", "ORGANIZATION", "1234567803", true, false},
	{"Marmara Eğitim Kurumları", "ORGANIZATION", "1234567804", true, false},
	{"Anadolu Lojistik A.Ş.", "ORGANIZATION", "1234567805", true, false},
	{"Selin Aydın", "PERSON", "", true, false},
	{"Mert Korkmaz", "PERSON", "", true, false},
	{"Batı Teknoloji Ticaret", "ORGANIZATION", "1234567806", true, true},
	{"Global Elektronik Dağıtım", "ORGANIZATION", "1234567811", false, true},
	{"Ofis Dünyası Toptan", "ORGANIZATION", "1234567812", false, true},
	{"Nordis Türkiye Distribütör", "ORGANIZATION", "1234567813", false, true},
}

func (r *Runner) seedParties(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for _, spec := range partySpecs {
		input := party.Input{
			Kind: spec.kind, IsActive: true, IsCustomer: spec.customer, IsSupplier: spec.supplier,
			DisplayName: spec.name, DefaultCurrency: "TRY", TaxNumber: spec.taxNo,
		}
		if spec.kind == "ORGANIZATION" {
			input.LegalName = spec.name
			input.TradeName = spec.name
		} else {
			input.FirstName, input.LastName = splitPersonName(spec.name)
		}
		created, err := svc.party.Create(ctx, session, input, seedMeta("party-"+spec.name))
		if err != nil {
			return err
		}
		if spec.customer {
			built.customers = append(built.customers, created)
		}
		if spec.supplier {
			built.suppliers = append(built.suppliers, created)
		}
	}
	return nil
}

func splitPersonName(full string) (string, string) {
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == ' ' {
			return full[:i], full[i+1:]
		}
	}
	return full, full
}
