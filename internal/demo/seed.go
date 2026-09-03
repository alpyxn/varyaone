package demo

import (
	"context"
	"fmt"
	"time"

	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/products"
	"github.com/alpyxn/varyaone/internal/purchasing"
	"github.com/alpyxn/varyaone/internal/sales"
)

// seedMeta stamps a seeding command with its own idempotency key. Commands in
// the commercial chain reserve one before they run, so every call needs a key
// that is unique within the run and stable across runs.
func seedMeta(key string) identity.RequestMeta {
	return identity.RequestMeta{TraceID: "demo-seed", IdempotencyKey: "demo-seed:" + key}
}

// demoTaxRate is the single VAT rate the demo catalogue uses. Keeping one rate
// makes the seeded line amounts easy to verify by eye.
const demoTaxRate = 20

// scope holds the identifiers provisionCompany created for the demo company.
type scope struct {
	branchID    string
	warehouseID string
}

// seededProduct pairs a created product with the unit it was created under;
// products.Product reports its units only as a display summary.
type seededProduct struct {
	products.Product
	spec productSpec
}

// unit is the base unit the product was created with; products.Product reports
// its units only as a display summary.
func (p seededProduct) unit() string { return p.spec.unit }

// minor parses one of the seed's price literals into minor units (kuruş), so
// line amounts can be computed exactly. The literals are the seeder's own, so a
// malformed one is a programming error rather than input to validate.
func minor(value string) int64 {
	whole, fraction, hasFraction := 0, 0, false
	digits := 0
	for _, ch := range value {
		if ch == '.' {
			hasFraction = true
			continue
		}
		if hasFraction {
			if digits < 2 {
				fraction = fraction*10 + int(ch-'0')
				digits++
			}
			continue
		}
		whole = whole*10 + int(ch-'0')
	}
	for digits < 2 {
		fraction *= 10
		digits++
	}
	return int64(whole)*100 + int64(fraction)
}

// money renders minor units back as a decimal literal for the service layer.
func money(value int64) string { return fmt.Sprintf("%d.%02d", value/100, value%100) }

// catalogue is what a seeding run builds up as it goes, so later steps can
// refer to what earlier ones created.
type catalogue struct {
	scope     scope
	cash      finance.Account
	bank      finance.Account
	products  []seededProduct
	services  []seededProduct
	customers []party.Party
	suppliers []party.Party
}

func (r *Runner) seed(ctx context.Context, session identity.Session) error {
	svc, err := r.services()
	if err != nil {
		return err
	}
	built := &catalogue{}
	if built.scope, err = r.readScope(ctx); err != nil {
		return err
	}
	steps := []struct {
		name string
		run  func(context.Context, identity.Session, *services, *catalogue) error
	}{
		{"finance accounts", r.seedFinanceAccounts},
		{"products", r.seedProducts},
		{"parties", r.seedParties},
		{"purchases", r.seedPurchases},
		{"sales", r.seedSales},
		{"settlements", r.seedSettlements},
		{"hr", r.seedHR},
		{"fixed assets", r.seedFixedAssets},
	}
	for _, step := range steps {
		if err = step.run(ctx, session, svc, built); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		r.opts.Logger.Debug("demo seed step complete", "step", step.name)
	}
	return nil
}

// readScope reads the branch and stock warehouse that company provisioning
// created, rather than assuming their identifiers.
func (r *Runner) readScope(ctx context.Context) (scope, error) {
	var result scope
	if err := r.pool.QueryRow(ctx, `SELECT id FROM branches WHERE company_id=$1 ORDER BY code LIMIT 1`, CompanyID).Scan(&result.branchID); err != nil {
		return scope{}, fmt.Errorf("read demo branch: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT id FROM warehouses WHERE company_id=$1 AND NOT is_system AND is_active ORDER BY code LIMIT 1`, CompanyID).Scan(&result.warehouseID); err != nil {
		return scope{}, fmt.Errorf("read demo warehouse: %w", err)
	}
	return result, nil
}

// day returns a date offset from the seeding clock. Every document is dated
// relative to "now", so the demo shows a business that was active last week
// rather than one frozen on the day the seed was written.
func (r *Runner) day(offset int) time.Time {
	now := r.opts.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
}

func (r *Runner) seedFinanceAccounts(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	cash, err := svc.finance.CreateAccount(ctx, session, finance.AccountInput{
		AccountType: "CASH", Code: "KASA-TL", Name: "Merkez Kasa", Currency: "TRY", BranchID: built.scope.branchID,
		Description: "Merkez şube TL kasası",
	}, seedMeta("account-cash"))
	if err != nil {
		return err
	}
	bank, err := svc.finance.CreateAccount(ctx, session, finance.AccountInput{
		AccountType: "BANK", Code: "BANKA-TL", Name: "Ticari Banka TL Hesabı", Currency: "TRY", BranchID: built.scope.branchID,
		BankName: "Demo Bankası", BankBranchName: "Kadıköy", IBAN: "TR330006100519786457841326",
	}, seedMeta("account-bank"))
	if err != nil {
		return err
	}
	built.cash, built.bank = cash, bank
	// Opening balances so the company starts with money in the till. Without
	// them the first supplier payment would drive the bank account negative and
	// the negative-balance policy would (correctly) refuse it.
	openings := []struct {
		account finance.Account
		amount  string
		key     string
	}{
		{cash, "45000.00", "opening-cash"},
		{bank, "750000.00", "opening-bank"},
	}
	for _, opening := range openings {
		if _, err = svc.finance.PostOpeningBalance(ctx, session, finance.AccountMovementInput{
			AccountID: opening.account.ID, Direction: "IN", Amount: opening.amount,
			TransactionDate: r.day(-90), Description: "Açılış bakiyesi",
			IdempotencyKey: "demo-seed:" + opening.key,
		}, seedMeta(opening.key)); err != nil {
			return err
		}
	}
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
}

var physicalProducts = []productSpec{
	{"URN-001", "Kablosuz Klavye", "Elektronik", "Aurora", "ADET", "480", "899"},
	{"URN-002", "Kablosuz Mouse", "Elektronik", "Aurora", "ADET", "260", "499"},
	{"URN-003", "27\" IPS Monitör", "Elektronik", "Nordis", "ADET", "4750", "7990"},
	{"URN-004", "USB-C Çoklayıcı", "Elektronik", "Aurora", "ADET", "620", "1190"},
	{"URN-005", "Dizüstü Standı", "Ofis", "Nordis", "ADET", "310", "649"},
	{"URN-006", "Ofis Sandalyesi", "Ofis", "Nordis", "ADET", "3200", "5750"},
	{"URN-007", "Yükseklik Ayarlı Masa", "Ofis", "Nordis", "ADET", "8400", "13900"},
	{"URN-008", "A4 Fotokopi Kağıdı", "Ofis", "", "PAKET", "145", "235"},
	{"URN-009", "Toner Kartuş", "Ofis", "", "ADET", "1250", "1990"},
	{"URN-010", "Ağ Anahtarı 8 Port", "Elektronik", "Nordis", "ADET", "1450", "2450"},
	{"URN-011", "Harici SSD 1 TB", "Elektronik", "Aurora", "ADET", "1980", "3190"},
	{"URN-012", "Web Kamerası", "Elektronik", "Aurora", "ADET", "890", "1590"},
}

var serviceProducts = []productSpec{
	{"HZM-001", "Kurulum ve Devreye Alma", "Hizmet", "", "SAAT", "0", "1250"},
	{"HZM-002", "Yıllık Bakım Sözleşmesi", "Hizmet", "", "ADET", "0", "18500"},
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
	create := func(spec productSpec, kind string) (seededProduct, error) {
		created, err := svc.products.Create(ctx, session, products.Input{
			Code: spec.code, Name: spec.name, Kind: kind, BaseUnit: spec.unit,
			PurchasePrice: spec.purchase, SalesPrice: spec.sales,
			PurchaseTaxRate: "20", SalesTaxRate: "20",
			CategoryID: categories[spec.category], BrandID: brands[spec.brand],
		}, products.Scope{BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID}, seedMeta("product-"+spec.code))
		return seededProduct{Product: created, spec: spec}, err
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

// seedPurchases brings the opening stock in the way a real business does: as
// posted standalone supplier invoices. That gives every product a cost layer
// and every supplier an open payable, so stock valuation, aging and the
// supplier ledger all have something true to show.
func (r *Runner) seedPurchases(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for index, supplier := range built.suppliers {
		lines := []purchasing.PurchaseInvoiceLine{}
		for productIndex, product := range built.products {
			if productIndex%len(built.suppliers) != index {
				continue
			}
			quantity := int64(40 + productIndex*5)
			unitPrice := minor(product.spec.purchase)
			gross := quantity * unitPrice
			tax := gross * demoTaxRate / 100
			lines = append(lines, purchasing.PurchaseInvoiceLine{
				LineNo: len(lines) + 1, LineType: "PRODUCT", ProductID: product.ID,
				WarehouseID: built.scope.warehouseID, UnitCode: product.unit(), DescriptionSnapshot: product.Name,
				Quantity: fmt.Sprintf("%d", quantity), UnitPrice: money(unitPrice),
				GrossAmount: money(gross), DiscountAmount: "0.00", TaxBase: money(gross),
				TaxAmount: money(tax), WithholdingAmount: "0.00", PayableAmount: money(gross + tax),
			})
		}
		if len(lines) == 0 {
			continue
		}
		invoiceDate := r.day(-60 + index*3)
		dueDate := invoiceDate.AddDate(0, 0, 30)
		invoice, err := svc.purchasing.CreatePurchaseInvoice(ctx, session, purchasing.PurchaseInvoiceInput{
			SupplierID: supplier.ID, BranchID: built.scope.branchID, WarehouseID: built.scope.warehouseID,
			Standalone: true, InvoiceDate: invoiceDate, DueDate: &dueDate, Currency: "TRY", Lines: lines,
		}, seedMeta(fmt.Sprintf("purchase-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.purchasing.FinalizePurchaseInvoice(ctx, session, invoice.ID, invoice.Version, seedMeta(fmt.Sprintf("purchase-finalize-%d", index))); err != nil {
			return err
		}
	}
	return nil
}

// seedSales walks the sales chain end to end so every screen has content: open
// quotes, a confirmed order with reservations, posted dispatches and invoices
// across the last two months, and one return.
func (r *Runner) seedSales(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	line := func(product seededProduct, quantity int) sales.CommercialLineInput {
		return sales.CommercialLineInput{
			LineType: "PRODUCT", ProductID: product.ID, WarehouseID: built.scope.warehouseID,
			UnitCode: product.unit(), Quantity: fmt.Sprintf("%d", quantity), UnitPrice: product.SalesPrice,
		}
	}
	serviceLine := func(product seededProduct, quantity int) sales.CommercialLineInput {
		return sales.CommercialLineInput{
			LineType: "SERVICE", ProductID: product.ID, UnitCode: product.unit(),
			Quantity: fmt.Sprintf("%d", quantity), UnitPrice: product.SalesPrice,
		}
	}
	document := func(customer party.Party, date time.Time, lines []sales.CommercialLineInput) sales.CommercialDocumentInput {
		due := date.AddDate(0, 0, 21)
		return sales.CommercialDocumentInput{
			BranchID: built.scope.branchID, DefaultWarehouseID: built.scope.warehouseID,
			PartyID: customer.ID, DocumentDate: date, DueDate: &due, CurrencyCode: "TRY", Lines: lines,
		}
	}

	// Posted invoices spread over the last two months.
	for index := 0; index < 10; index++ {
		customer := built.customers[index%len(built.customers)]
		lines := []sales.CommercialLineInput{
			line(built.products[index%len(built.products)], 2+index%4),
			line(built.products[(index+3)%len(built.products)], 1+index%3),
		}
		if index%3 == 0 {
			lines = append(lines, serviceLine(built.services[0], 2))
		}
		invoice, err := svc.sales.CreateSalesInvoice(ctx, session, document(customer, r.day(-52+index*5), lines), seedMeta(fmt.Sprintf("invoice-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesInvoice, invoice.ID, "post", invoice.Version, seedMeta(fmt.Sprintf("invoice-post-%d", index)), ""); err != nil {
			return err
		}
	}

	// Two dispatches waiting to be invoiced.
	for index := 0; index < 2; index++ {
		customer := built.customers[(index+2)%len(built.customers)]
		dispatch, err := svc.sales.CreateSalesDispatch(ctx, session,
			document(customer, r.day(-6+index*2), []sales.CommercialLineInput{line(built.products[index+4], 3)}), seedMeta(fmt.Sprintf("dispatch-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesDispatch, dispatch.ID, "post", dispatch.Version, seedMeta(fmt.Sprintf("dispatch-post-%d", index)), ""); err != nil {
			return err
		}
	}

	// Confirmed orders: open, reserved, not yet delivered.
	for index := 0; index < 3; index++ {
		customer := built.customers[(index+1)%len(built.customers)]
		order, err := svc.sales.CreateSalesOrder(ctx, session,
			document(customer, r.day(-9+index*3), []sales.CommercialLineInput{
				line(built.products[(index+6)%len(built.products)], 4),
				line(built.products[(index+8)%len(built.products)], 2),
			}), seedMeta(fmt.Sprintf("order-create-%d", index)))
		if err != nil {
			return err
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesOrder, order.ID, "confirm", order.Version, seedMeta(fmt.Sprintf("order-confirm-%d", index)), ""); err != nil {
			return err
		}
	}

	// Quotes: one still a draft, one sent, one accepted.
	quoteCommands := []string{"", "send", "accept"}
	for index, command := range quoteCommands {
		customer := built.customers[(index+3)%len(built.customers)]
		quote, err := svc.sales.CreateSalesQuote(ctx, session,
			document(customer, r.day(-4+index), []sales.CommercialLineInput{
				line(built.products[(index+2)%len(built.products)], 5),
				serviceLine(built.services[1], 1),
			}), seedMeta(fmt.Sprintf("quote-create-%d", index)))
		if err != nil {
			return err
		}
		version := quote.Version
		if command == "accept" {
			sent, sendErr := svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, "send", version, seedMeta(fmt.Sprintf("quote-send-%d", index)), "")
			if sendErr != nil {
				return sendErr
			}
			version = sent.Version
		}
		if command == "" {
			continue
		}
		if _, err = svc.sales.TransitionCommercial(ctx, session, sales.SalesQuote, quote.ID, command, version, seedMeta(fmt.Sprintf("quote-%s-%d", command, index)), ""); err != nil {
			return err
		}
	}
	return nil
}

// seedSettlements collects from customers and pays suppliers, leaving some
// invoices fully paid, some partly paid and some untouched - which is what
// makes the aging and open-item screens worth looking at.
func (r *Runner) seedSettlements(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for index, customer := range built.customers {
		if index%3 == 2 {
			continue // leave a third of the customers entirely open
		}
		amount := fmt.Sprintf("%d", 4000+index*2500)
		account := built.bank
		method := "BANK"
		if index%2 == 1 {
			account, method = built.cash, "CASH"
		}
		if _, err := svc.finance.PostCollection(ctx, session, finance.PaymentInput{
			PartyID: customer.ID, AccountID: account.ID, PaymentKind: "COLLECTION", PaymentMethod: method,
			Currency: "TRY", Amount: amount, TransactionDate: r.day(-20 + index*2),
			Description: "Müşteri tahsilatı", IdempotencyKey: fmt.Sprintf("demo-collection-%d", index),
			AutoAllocate: true,
		}, seedMeta(fmt.Sprintf("collection-%d", index))); err != nil {
			return err
		}
	}
	for index, supplier := range built.suppliers {
		if index%2 == 1 {
			continue
		}
		if _, err := svc.finance.PostPayment(ctx, session, finance.PaymentInput{
			PartyID: supplier.ID, AccountID: built.bank.ID, PaymentKind: "PAYMENT", PaymentMethod: "BANK",
			Currency: "TRY", Amount: fmt.Sprintf("%d", 15000+index*5000), TransactionDate: r.day(-15 + index*3),
			Description: "Tedarikçi ödemesi", IdempotencyKey: fmt.Sprintf("demo-payment-%d", index),
			AutoAllocate: true,
		}, seedMeta(fmt.Sprintf("payment-%d", index))); err != nil {
			return err
		}
	}
	return nil
}

type employeeSpec struct {
	code     string
	first    string
	last     string
	title    string
	email    string
	position string
}

var employeeSpecs = []employeeSpec{
	{"PER-001", "Ayşe", "Yıldırım", "Genel Müdür", "ayse.yildirim@demo.varyaone.com", ""},
	{"PER-002", "Burak", "Şahin", "Satış Müdürü", "burak.sahin@demo.varyaone.com", ""},
	{"PER-003", "Ceren", "Demir", "Muhasebe Uzmanı", "ceren.demir@demo.varyaone.com", ""},
	{"PER-004", "Deniz", "Arslan", "Depo Sorumlusu", "deniz.arslan@demo.varyaone.com", ""},
	{"PER-005", "Emre", "Koç", "Saha Teknisyeni", "emre.koc@demo.varyaone.com", ""},
	{"PER-006", "Funda", "Aksoy", "İnsan Kaynakları Uzmanı", "funda.aksoy@demo.varyaone.com", ""},
}

func (r *Runner) seedHR(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for _, spec := range employeeSpecs {
		if _, err := svc.employee.Create(ctx, session, employee.Input{
			EmployeeCode: spec.code, FirstName: spec.first, LastName: spec.last,
			Status: "ACTIVE", PositionTitle: spec.title, WorkEmail: spec.email,
		}, seedMeta("employee-"+spec.code)); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) seedFixedAssets(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	if _, err := svc.fixedAsset.CreateCategory(ctx, session, fixedasset.CategoryInput{Code: "BT", Name: "Bilgi Teknolojileri"}, seedMeta("asset-category-bt")); err != nil {
		return err
	}
	assets := []fixedasset.Input{
		{AssetCode: "SK-001", Name: "Dizüstü Bilgisayar - Satış", Category: "BT", SerialNumber: "SN-DEMO-0001", Status: "AVAILABLE"},
		{AssetCode: "SK-002", Name: "Dizüstü Bilgisayar - Muhasebe", Category: "BT", SerialNumber: "SN-DEMO-0002", Status: "AVAILABLE"},
		{AssetCode: "SK-003", Name: "Depo El Terminali", Category: "BT", SerialNumber: "SN-DEMO-0003", Status: "AVAILABLE"},
		{AssetCode: "SK-004", Name: "Toplantı Odası Projeksiyon", Category: "BT", SerialNumber: "SN-DEMO-0004", Status: "AVAILABLE"},
	}
	for _, asset := range assets {
		if _, err := svc.fixedAsset.Create(ctx, session, asset, seedMeta("asset-"+asset.AssetCode)); err != nil {
			return err
		}
	}
	return nil
}
