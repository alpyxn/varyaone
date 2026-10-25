package demo

import (
	"context"
	_ "embed"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/alpyxn/varyaone/internal/crm"
	"github.com/alpyxn/varyaone/internal/finance"
	"github.com/alpyxn/varyaone/internal/fixedasset"
	"github.com/alpyxn/varyaone/internal/hr/employee"
	"github.com/alpyxn/varyaone/internal/hr/timesheet"
	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/alpyxn/varyaone/internal/party"
	"github.com/alpyxn/varyaone/internal/products"
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

// scope holds the places documents are written to. The main warehouse comes
// from company provisioning; the store warehouse is seeded here so the demo has
// somewhere to transfer stock to and a second column in every stock report.
type scope struct {
	branchID    string
	warehouseID string
	storeID     string
}

// seededProduct pairs a created product with the unit it was created under and
// the variants generated for it; products.Product reports its units only as a
// display summary, and a variant-tracked product may never appear on a document
// line without one of its variants.
type seededProduct struct {
	products.Product
	spec     productSpec
	variants []products.Variant
}

// unit is the base unit the product was created with; products.Product reports
// its units only as a display summary.
func (p seededProduct) unit() string { return p.spec.unit }

// variantID picks one of the product's variants, or returns "" for a product
// that is not variant-tracked, so both kinds of product build a document line
// through the same code.
func (p seededProduct) variantID(index int) string {
	if len(p.variants) == 0 {
		return ""
	}
	return p.variants[index%len(p.variants)].ID
}

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

// quantity renders a whole count as the decimal literal the document services
// expect.
func quantity(value int64) string { return fmt.Sprintf("%d", value) }

// catalogue is what a seeding run builds up as it goes, so later steps can
// refer to what earlier ones created.
type catalogue struct {
	scope      scope
	cash       finance.Account
	bank       finance.Account
	products   []seededProduct
	services   []seededProduct
	customers  []party.Party
	suppliers  []party.Party
	receipts   []seededReceipt
	invoiceIDs []string
}

// plainProducts is the subset of the catalogue a document line may name on its
// own: products that are not variant-tracked, so the line needs no variant.
func (c *catalogue) plainProducts() []seededProduct {
	plain := make([]seededProduct, 0, len(c.products))
	for _, product := range c.products {
		if len(product.variants) == 0 {
			plain = append(plain, product)
		}
	}
	return plain
}

// lineDescription is the name a document line carries for a product, with the
// variant code appended when the product is variant-tracked. Document services
// snapshot the description the caller sends, so a line built from a posted
// document's own rows still needs the catalogue to name it.
func (c *catalogue) lineDescription(productID, variantID string) string {
	for _, product := range c.products {
		if product.ID != productID {
			continue
		}
		for _, variant := range product.variants {
			if variant.ID == variantID {
				return product.Name + " " + variant.VariantCode
			}
		}
		return product.Name
	}
	return "Ürün"
}

// variantProducts is the opposite subset: the products every line must carry a
// variant for.
func (c *catalogue) variantProducts() []seededProduct {
	tracked := make([]seededProduct, 0, 3)
	for _, product := range c.products {
		if len(product.variants) > 0 {
			tracked = append(tracked, product)
		}
	}
	return tracked
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
		{"company logo", r.seedCompanyLogo},
		{"warehouses", r.seedWarehouses},
		{"finance accounts", r.seedFinanceAccounts},
		{"products", r.seedProducts},
		{"parties", r.seedParties},
		{"purchases", r.seedPurchases},
		{"sales", r.seedSales},
		{"stock operations", r.seedStockOperations},
		{"stock count", r.seedStockCount},
		{"settlements", r.seedSettlements},
		{"hr", r.seedHR},
		{"timesheet", r.seedTimesheet},
		{"fixed assets", r.seedFixedAssets},
		{"crm", r.seedCRM},
	}
	for _, step := range steps {
		if err = step.run(ctx, session, svc, built); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
		r.opts.Logger.Debug("demo seed step complete", "step", step.name)
	}
	return nil
}

// placeholderLogo is the demo company's logo. It is a Logoipsum mark — a free
// placeholder logo, deliberately not a real brand — so the demo shows what a
// company logo looks like on the payslip and in the app without implying the
// sample data belongs to anyone.
//
//go:embed assets/placeholder-logo.png
var placeholderLogo []byte

// seedCompanyLogo puts the placeholder mark on the demo company. The logo is
// stored the way the settings screen stores an uploaded one: a base64 image
// data URI on the company row.
func (r *Runner) seedCompanyLogo(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(placeholderLogo)
	if _, err := r.pool.Exec(ctx, `UPDATE companies SET logo=$2,updated_at=now(),version=version+1 WHERE id=$1`,
		CompanyID, dataURI); err != nil {
		return fmt.Errorf("set demo company logo: %w", err)
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
		{bank, "3500000.00", "opening-bank"},
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
	// One till-to-bank deposit. Every company that takes cash makes this move,
	// and without it the account-transfer screens are the only finance list in
	// the demo with nothing on them — which is how their record page came to be
	// the one no browser test could open.
	if _, err = svc.finance.PostFinanceTransfer(ctx, session, finance.FinanceTransferInput{
		FromAccountID: cash.ID, ToAccountID: bank.ID, Amount: "12500.00",
		TransactionDate: r.day(-20), Description: "Kasa fazlasının bankaya yatırılması",
		IdempotencyKey: "demo-seed:finance-transfer",
	}, seedMeta("finance-transfer")); err != nil {
		return err
	}
	return nil
}

// seedSettlements collects from customers and pays suppliers, leaving some
// invoices fully paid, some partly paid and some untouched - which is what
// makes the aging and open-item screens worth looking at.
//
// Every amount is derived from what the party actually owes: a payment larger
// than the open balance is refused (see enforceOpenAmountTx), and a demo built
// from round made-up figures would be the first thing to hit that rule.
func (r *Runner) seedSettlements(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for index, customer := range built.customers {
		if index%3 == 2 {
			continue // leave a third of the customers entirely open
		}
		amount, err := r.settlementAmount(ctx, session, svc, customer.ID, "RECEIVABLE", index%2 == 0)
		if err != nil || amount == "" {
			if err != nil {
				return err
			}
			continue
		}
		account := built.bank
		method := "BANK"
		if index%2 == 1 {
			account, method = built.cash, "CASH"
		}
		// Collections happen after the newest seeded invoice and return. Keeping
		// the business date behind those documents would make FIFO allocation pay
		// an invoice that, on paper, did not exist yet.
		if _, err = svc.finance.PostCollection(ctx, session, finance.PaymentInput{
			PartyID: customer.ID, AccountID: account.ID, PaymentKind: "COLLECTION", PaymentMethod: method,
			Currency: "TRY", Amount: amount, TransactionDate: r.day(-6 + index%6),
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
		amount, err := r.settlementAmount(ctx, session, svc, supplier.ID, "PAYABLE", false)
		if err != nil || amount == "" {
			if err != nil {
				return err
			}
			continue
		}
		if _, err = svc.finance.PostPayment(ctx, session, finance.PaymentInput{
			PartyID: supplier.ID, AccountID: built.bank.ID, PaymentKind: "PAYMENT", PaymentMethod: "BANK",
			Currency: "TRY", Amount: amount, TransactionDate: r.day(-15 + index*3),
			Description: "Tedarikçi ödemesi", IdempotencyKey: fmt.Sprintf("demo-payment-%d", index),
			AutoAllocate: true,
		}, seedMeta(fmt.Sprintf("payment-%d", index))); err != nil {
			return err
		}
	}
	return nil
}

// settlementAmount is what this party can be settled for: the whole open
// balance when full is set, and half the oldest invoice otherwise, which is
// what leaves the demo with a mix of paid, part-paid and untouched invoices.
// It returns "" when the party owes nothing.
func (r *Runner) settlementAmount(ctx context.Context, session identity.Session, svc *services, partyID, side string, full bool) (string, error) {
	items, err := svc.finance.ListOpenItems(ctx, session, partyID, "TRY", side, 100)
	if err != nil {
		return "", err
	}
	total := int64(0)
	for _, item := range items {
		open := minor(item.OpenAmount)
		if !full {
			total = open / 2
			break
		}
		total += open
	}
	if total == 0 {
		return "", nil
	}
	return money(total), nil
}

type employeeSpec struct {
	code       string
	first      string
	last       string
	title      string
	email      string
	grossWage  string // monthly gross wage in TRY; the demo pays every seeded employee something so payroll can actually run
	hireOffset int    // days before "now" the employment started
}

var employeeSpecs = []employeeSpec{
	{"PER-001", "Ayşe", "Yıldırım", "Genel Müdür", "ayse.yildirim@demo.varyaone.com", "120000.00", -1095},
	{"PER-002", "Burak", "Şahin", "Satış Müdürü", "burak.sahin@demo.varyaone.com", "75000.00", -820},
	{"PER-003", "Ceren", "Demir", "Muhasebe Uzmanı", "ceren.demir@demo.varyaone.com", "55000.00", -640},
	{"PER-004", "Deniz", "Arslan", "Depo Sorumlusu", "deniz.arslan@demo.varyaone.com", "40000.00", -460},
	{"PER-005", "Emre", "Koç", "Saha Teknisyeni", "emre.koc@demo.varyaone.com", "42000.00", -300},
	{"PER-006", "Funda", "Aksoy", "İnsan Kaynakları Uzmanı", "funda.aksoy@demo.varyaone.com", "48000.00", -180},
}

func (r *Runner) seedHR(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	for _, spec := range employeeSpecs {
		hireDate := r.day(spec.hireOffset).Format("2006-01-02")
		if _, err := svc.employee.Create(ctx, session, employee.Input{
			EmployeeCode: spec.code, FirstName: spec.first, LastName: spec.last,
			Status: "ACTIVE", PositionTitle: spec.title, WorkEmail: spec.email,
			Employment: &employee.EmploymentSetup{
				StartDate: hireDate,
				GrossWage: spec.grossWage,
				Currency:  "TRY",
				WorkType:  "FULL_TIME",
				SgkStatus: "4A",
			},
		}, seedMeta("employee-"+spec.code)); err != nil {
			return err
		}
	}
	return nil
}

// seedTimesheet finalizes one full month of puantaj (attendance) for the
// previous calendar month, so a payroll run has a finalized timesheet period
// to point at right after seeding — payroll refuses to run against one that
// isn't finalized (payroll/run.ErrTimesheetNotFinal upstream).
func (r *Runner) seedTimesheet(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	prevMonth := time.Date(r.opts.Now().UTC().Year(), r.opts.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	period, err := svc.timesheet.CreatePeriod(ctx, session, timesheet.PeriodInput{
		PeriodYear: prevMonth.Year(), PeriodMonth: int(prevMonth.Month()),
	}, seedMeta("timesheet-period"))
	if err != nil {
		return err
	}
	period, err = svc.timesheet.Generate(ctx, session, period.ID, seedMeta("timesheet-generate"))
	if err != nil {
		return err
	}
	if _, err := svc.timesheet.Finalize(ctx, session, period.ID, period.Version, seedMeta("timesheet-finalize")); err != nil {
		return err
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

// seedCRM fills the CRM module with a small cold-call campaign: a script, a
// call list of leads in various states, a promised callback and a pipeline
// with open, won and lost opportunities.
func (r *Runner) seedCRM(ctx context.Context, session identity.Session, svc *services, built *catalogue) error {
	script, err := svc.crm.CreateScript(ctx, session, crm.CallScriptInput{
		Name: "Tanışma araması",
		Body: "Merhaba, ben Varya Demo'dan arıyorum. Ofis ve depo ihtiyaçlarınız için kısa bir bilgi vermek istiyorum, uygun musunuz?\n\n" +
			"İtirazlar:\n- \"Tedarikçimiz var\": Fiyat karşılaştırması için bir teklif bırakmamıza izin verir misiniz?\n" +
			"- \"Şu an meşgulüm\": Ne zaman tekrar arayayım?",
	}, seedMeta("crm-script"))
	if err != nil {
		return err
	}
	list, err := svc.crm.CreateCallList(ctx, session, crm.CallListInput{
		Name: "Ekim tanışma aramaları", Description: "Fuar katılımcıları ve referanslar", ScriptID: script.ID,
		RetryMinutes: 120, MaxAttempts: 4,
	}, seedMeta("crm-list"))
	if err != nil {
		return err
	}
	leads := []crm.LeadInput{
		{DisplayName: "Ege Ambalaj Ltd.", ContactName: "Selin Aydın", ContactTitle: "Satın alma müdürü", Phone: "0232 555 10 01", City: "İzmir", Sector: "Ambalaj", Source: "Fuar"},
		{DisplayName: "Kuzey Lojistik A.Ş.", ContactName: "Murat Kaya", ContactTitle: "Operasyon müdürü", Phone: "0216 555 10 02", City: "İstanbul", Sector: "Lojistik", Source: "Fuar"},
		{DisplayName: "Anadolu Gıda", ContactName: "Elif Şahin", Phone: "0312 555 10 03", City: "Ankara", Sector: "Gıda", Source: "Referans"},
		{DisplayName: "Marmara Yapı", ContactName: "Can Öztürk", Phone: "0224 555 10 04", City: "Bursa", Sector: "İnşaat", Source: "Web sitesi"},
		{DisplayName: "Akdeniz Otelcilik", ContactName: "Zeynep Arslan", ContactTitle: "Genel müdür yardımcısı", Phone: "0242 555 10 05", City: "Antalya", Sector: "Turizm", Source: "Fuar"},
		{DisplayName: "Trakya Tekstil", ContactName: "Burak Demir", Phone: "0282 555 10 06", City: "Tekirdağ", Sector: "Tekstil", Source: "Soğuk arama listesi"},
		{DisplayName: "Karadeniz Mobilya", Phone: "0462 555 10 07", City: "Trabzon", Sector: "Mobilya", Source: "Soğuk arama listesi"},
		{DisplayName: "Başkent Eczanesi", ContactName: "Ayşe Yıldız", Phone: "0312 555 10 08", City: "Ankara", Sector: "Sağlık", Source: "Referans"},
	}
	created := make([]crm.Lead, 0, len(leads))
	ids := make([]string, 0, len(leads))
	for index, input := range leads {
		lead, err := svc.crm.CreateLead(ctx, session, input, seedMeta(fmt.Sprintf("crm-lead-%d", index)))
		if err != nil {
			return err
		}
		created = append(created, lead)
		ids = append(ids, lead.ID)
	}
	if _, err = svc.crm.AddLeads(ctx, session, list.ID, crm.AddLeadsInput{LeadIDs: ids}, seedMeta("crm-list-add")); err != nil {
		return err
	}
	// Work through the first few leads the way a rep would from the call screen.
	callback := time.Now().Add(26 * time.Hour).Truncate(time.Hour)
	outcomes := []struct {
		outcome  string
		note     string
		callback *time.Time
	}{
		{crm.OutcomeInterested, "Depo rafları için teklif istedi; 40 m² alan.", nil},
		{crm.OutcomeMeetingSet, "Perşembe 14:00 ofislerinde toplantı.", nil},
		{crm.OutcomeCallback, "Toplantıdaydı, yarın öğleden sonra aranacak.", &callback},
		{crm.OutcomeNoAnswer, "", nil},
		{crm.OutcomeNotInterested, "Yeni tedarikçi çalışmıyorlar.", nil},
	}
	for index, step := range outcomes {
		next, err := svc.crm.ClaimNext(ctx, session, list.ID)
		if err != nil {
			return err
		}
		duration := 60 + index*45
		if _, err = svc.crm.LogCall(ctx, session, next.Lead.ID, crm.CallInput{
			CallListItemID: next.Item.ID, Outcome: step.outcome, Note: step.note,
			DurationSeconds: &duration, CallbackAt: timeText(step.callback), RemindMinutes: 10,
		}, seedMeta(fmt.Sprintf("crm-call-%d", index))); err != nil {
			return err
		}
	}

	stages, err := svc.crm.ListStages(ctx, session)
	if err != nil {
		return err
	}
	stageByName := map[string]crm.Stage{}
	for _, stage := range stages {
		stageByName[stage.Name] = stage
	}
	type plan struct {
		input crm.OpportunityInput
		stage string
		lost  string
	}
	plans := []plan{
		{input: crm.OpportunityInput{Title: "Depo raf sistemi", LeadID: created[0].ID, Amount: "185000", ExpectedCloseDate: r.day(20).Format("2006-01-02")}, stage: "Görüşme"},
		{input: crm.OpportunityInput{Title: "Filo için ofis malzemesi", LeadID: created[1].ID, Amount: "64000", ExpectedCloseDate: r.day(35).Format("2006-01-02")}, stage: "Yeni"},
	}
	if len(built.customers) >= 3 {
		plans = append(plans,
			plan{input: crm.OpportunityInput{Title: "Yıllık sarf malzemesi anlaşması", PartyID: built.customers[0].ID, Amount: "420000", ExpectedCloseDate: r.day(14).Format("2006-01-02")}, stage: "Pazarlık"},
			plan{input: crm.OpportunityInput{Title: "Yeni şube donanımı", PartyID: built.customers[1].ID, Amount: "96500"}, stage: "Kazanıldı"},
			plan{input: crm.OpportunityInput{Title: "Kampanya baskıları", PartyID: built.customers[2].ID, Amount: "28000"}, stage: "Kaybedildi", lost: "Fiyat yüksek bulundu"},
		)
	}
	for index, p := range plans {
		opportunity, err := svc.crm.CreateOpportunity(ctx, session, p.input, seedMeta(fmt.Sprintf("crm-opportunity-%d", index)))
		if err != nil {
			return err
		}
		stage, ok := stageByName[p.stage]
		if !ok || stage.ID == opportunity.StageID {
			continue
		}
		if _, err = svc.crm.MoveStage(ctx, session, opportunity.ID, opportunity.Version, stage.ID, p.lost, seedMeta(fmt.Sprintf("crm-opportunity-move-%d", index))); err != nil {
			return err
		}
	}
	return nil
}

func timeText(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
