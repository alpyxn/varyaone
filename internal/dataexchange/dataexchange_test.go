package dataexchange

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInitialEntitySpecsDirections(t *testing.T) {
	specs := InitialEntitySpecs()
	byType := make(map[EntityType]EntitySpec, len(specs))
	for _, spec := range specs {
		byType[spec.Type] = spec
	}

	for _, entity := range []EntityType{EntityProduct, EntityVariant, EntityWarehouse, EntityParty} {
		spec, ok := byType[entity]
		if !ok {
			t.Fatalf("missing %s entity spec", entity)
		}
		if !spec.Importable || !spec.Exportable {
			t.Fatalf("%s directions = import:%v export:%v, want both enabled", entity, spec.Importable, spec.Exportable)
		}
	}
	barcode := byType[EntityBarcode]
	if barcode.Importable || !barcode.Exportable {
		t.Fatalf("barcode directions = import:%v export:%v, want import disabled and export enabled", barcode.Importable, barcode.Exportable)
	}

	for _, entity := range []EntityType{EntityPriceList, EntityOpeningStock, EntityStockCount} {
		if spec, ok := byType[entity]; !ok || !spec.Importable || !spec.Exportable {
			t.Fatalf("separate workflow entity %s is not preserved in the catalog", entity)
		}
	}
}

func TestWriteXLSXSupportsPayrollSheetNameAndNumericCells(t *testing.T) {
	table, err := NewTable([]string{"Çalışan", "Net"}, [][]string{{"Çağrı Öztürk", "28075.50"}})
	if err != nil {
		t.Fatal(err)
	}
	var payload bytes.Buffer
	if err = WriteXLSXWithOptions(&payload, table, XLSXOptions{SheetName: "Bordro Özeti", NumericColumns: map[int]bool{1: true}}); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(payload.Bytes()), int64(payload.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{}
	for _, file := range reader.File {
		stream, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		contents, e := io.ReadAll(stream)
		_ = stream.Close()
		if e != nil {
			t.Fatal(e)
		}
		entries[file.Name] = string(contents)
	}
	if !strings.Contains(entries["xl/workbook.xml"], `name="Bordro Özeti"`) {
		t.Fatalf("workbook=%s", entries["xl/workbook.xml"])
	}
	if !strings.Contains(entries["xl/worksheets/sheet1.xml"], `<c r="B2"><v>28075.50</v></c>`) {
		t.Fatalf("sheet=%s", entries["xl/worksheets/sheet1.xml"])
	}
}

func TestInitialEntitySpecsFieldMetadata(t *testing.T) {
	specs := InitialEntitySpecs()
	product := entitySpec(t, specs, EntityProduct)
	productCode := entityField(t, product, "product_code")
	if productCode.Label != "Stok Kodu" || productCode.Type != FieldTypeString || !productCode.Required || productCode.Example == "" {
		t.Fatalf("product_code metadata = %#v", productCode)
	}
	if !containsString(productCode.Aliases, "Ürün Kodu") {
		t.Fatalf("product_code aliases = %#v, want Turkish source alias", productCode.Aliases)
	}
	if productCode.Name == productCode.Label {
		t.Fatalf("target field ID must not be its Turkish display label: %#v", productCode)
	}
	manualTable, err := NewTable([]string{"Stok Kodu", "Stok Adı", "Birim"}, [][]string{{"STK-001", "Pamuklu Tişört", "ADET"}})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := ResolveMapping(manualTable, product.Fields, MappingOptions{Manual: map[string]string{"product_code": "Stok Kodu"}})
	if err != nil {
		t.Fatalf("stable target ID mapping failed: %v", err)
	}
	if _, ok := mappingColumn(manual.Mapping, "product_code", MappingMethodManual); !ok {
		t.Fatalf("manual mapping did not retain stable target ID: %#v", manual.Mapping.Columns)
	}
	purchasePrice := entityField(t, product, "purchase_price")
	if purchasePrice.Required || purchasePrice.Type != FieldTypeDecimal || purchasePrice.Label != "Alış Fiyatı" {
		t.Fatalf("purchase_price metadata = %#v", purchasePrice)
	}
	vatRate := entityField(t, product, "vat_rate")
	if vatRate.Required || vatRate.Type != FieldTypeDecimal || vatRate.Label != "KDV Oranı (%)" {
		t.Fatalf("vat_rate metadata = %#v", vatRate)
	}
	if active := entityField(t, product, "is_active"); active.Example != "Evet" {
		t.Fatalf("product is_active example = %#v, want Turkish value", active)
	}
	for _, name := range []string{"opening_stock_warehouse_code", "opening_stock_quantity"} {
		field := entityField(t, product, name)
		if field.Required || field.Label == "" || field.Example == "" {
			t.Fatalf("opening stock field %s metadata = %#v", name, field)
		}
	}

	variant := entitySpec(t, specs, EntityVariant)
	variantValues := entityField(t, variant, "variant_values")
	if !variantValues.Required || variantValues.Type != FieldTypeJSON || variantValues.Example != `{"RENK":"KIRMIZI","BEDEN":"M"}` {
		t.Fatalf("variant_values metadata = %#v", variantValues)
	}
	if _, err := ResolveMapping(mustTable(t, []string{"Ürün Kodu", "Varyant Kodu", "Varyant Değerleri"}), variant.Fields, MappingOptions{}); err != nil {
		t.Fatalf("variant mapping by Turkish aliases failed: %v", err)
	}

	warehouse := entitySpec(t, specs, EntityWarehouse)
	branchCode := entityField(t, warehouse, "branch_code")
	if branchCode.Required || branchCode.Type != FieldTypeString || branchCode.Label != "Şube Kodu" {
		t.Fatalf("branch_code metadata = %#v", branchCode)
	}

	barcode := entitySpec(t, specs, EntityBarcode)
	for _, name := range []string{"owner", "barcode_type", "is_primary"} {
		field := entityField(t, barcode, name)
		if field.Example == "" || field.Label == "" || field.Type == "" {
			t.Fatalf("barcode %s metadata = %#v", name, field)
		}
	}
	if owner := entityField(t, barcode, "owner"); owner.Example != "Ürün:STK-001" {
		t.Fatalf("barcode owner example = %#v, want Turkish owner value", owner)
	}
	if primary := entityField(t, barcode, "is_primary"); primary.Example != "Evet" {
		t.Fatalf("barcode is_primary example = %#v, want Turkish value", primary)
	}

	party := entitySpec(t, specs, EntityParty)
	for _, name := range []string{"code", "kind", "roles", "name", "legal_name", "trade_name", "tax_number", "identity_number", "tax_office", "currency", "phone", "email", "address_line", "province_name", "district_name", "neighborhood_name"} {
		field := entityField(t, party, name)
		if field.Label == "" || field.Type == "" || field.Example == "" {
			t.Fatalf("party %s metadata = %#v", name, field)
		}
	}
	if kind := entityField(t, party, "kind"); kind.Example != "Kurum" {
		t.Fatalf("party kind example = %#v, want Turkish value", kind)
	}
	if roles := entityField(t, party, "roles"); roles.Example != `["Müşteri"]` {
		t.Fatalf("party roles example = %#v, want Turkish value", roles)
	}
	if entityHasField(party, "balance") || entityHasField(party, "ledger") || entityHasField(party, "opening_balance") {
		t.Fatalf("party spec must not expose financial history fields: %#v", party.Fields)
	}
	if entityHasField(party, "postal_code") {
		t.Fatalf("party spec must not expose removed postal-code field: %#v", party.Fields)
	}

	encoded, err := json.Marshal(party)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || bytes.Contains(encoded, []byte(`"Name"`)) || !bytes.Contains(encoded, []byte(`"name"`)) {
		t.Fatalf("party JSON metadata does not expose stable JSON keys: %s", encoded)
	}
}

func entitySpec(t *testing.T, specs []EntitySpec, entity EntityType) EntitySpec {
	t.Helper()
	for _, spec := range specs {
		if spec.Type == entity {
			return spec
		}
	}
	t.Fatalf("missing entity spec %s", entity)
	return EntitySpec{}
}

func entityField(t *testing.T, spec EntitySpec, name string) FieldSpec {
	t.Helper()
	for _, field := range spec.Fields {
		if field.Name == name {
			return field
		}
	}
	t.Fatalf("missing %s field in %s", name, spec.Type)
	return FieldSpec{}
}

func entityHasField(spec EntitySpec, name string) bool {
	for _, field := range spec.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func mustTable(t *testing.T, headers []string) Table {
	t.Helper()
	table, err := NewTable(headers, [][]string{{"STK-001", "KIRMIZI-M", `{"RENK":"KIRMIZI"}`}})
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestResolveMappingAutoAndManual(t *testing.T) {
	table, err := NewTable(
		[]string{" ÜRÜN KODU ", "Miktar", "Açıklama", "Kullanılmayan"},
		[][]string{{" A-1 ", "2,50", "Raf ürünü", "not used"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	result, err := ResolveMapping(table, []FieldSpec{
		{Name: "product_code", Aliases: []string{"Ürün Kodu"}, Required: true},
		{Name: "quantity", Aliases: []string{"Miktar"}, Required: true},
		{Name: "description", Aliases: []string{"Açıklama"}},
	}, MappingOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Mapping.Columns) != 3 {
		t.Fatalf("mapped columns = %d, want 3", len(result.Mapping.Columns))
	}
	if _, ok := mappingColumn(result.Mapping, "product_code", MappingMethodAuto); !ok {
		t.Fatal("product_code was not automatically mapped")
	}
	if !hasIssueCode(result.Issues, "unmapped_source_column") {
		t.Fatal("expected an unmapped source-column warning")
	}

	mapped, err := result.Mapping.MapRow(table.Rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Values["product_code"] != "A-1" || mapped.Values["quantity"] != "2,50" {
		t.Fatalf("mapped values = %#v", mapped.Values)
	}

	manualTable, err := NewTable([]string{"Kod", "Qty"}, [][]string{{"A-2", "3"}})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := ResolveMapping(manualTable, []FieldSpec{
		{Name: "product_code", Aliases: []string{"Kod"}, Required: true},
		{Name: "quantity", Aliases: []string{"Miktar"}, Required: true},
	}, MappingOptions{Manual: map[string]string{"quantity": "Qty"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mappingColumn(manual.Mapping, "quantity", MappingMethodManual); !ok {
		t.Fatal("quantity was not manually mapped")
	}
}

func TestResolveMappingRejectsAmbiguousColumn(t *testing.T) {
	table, err := NewTable([]string{"Code", "SKU"}, [][]string{{"A-1", "A-1"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ResolveMapping(table, []FieldSpec{{Name: "product_code", Aliases: []string{"Code", "SKU"}, Required: true}}, MappingOptions{})
	if err == nil {
		t.Fatal("expected ambiguous mapping error")
	}
	var mappingErr *MappingError
	if !errors.As(err, &mappingErr) || !hasIssueCode(mappingErr.Issues, "ambiguous_mapping") {
		t.Fatalf("error = %v, issues = %#v", err, mappingErr)
	}
}

func TestQuantityValidationPrimitives(t *testing.T) {
	rule := QuantityRule{Field: "quantity", Required: true, MaxScale: 2}
	tests := []struct {
		name string
		raw  string
		code string
	}{
		{name: "invalid", raw: "1.2.3", code: "invalid_quantity"},
		{name: "negative", raw: "-1", code: "negative_quantity"},
		{name: "precision", raw: "1,234", code: "quantity_precision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := ValidateQuantity(MappedRow{RowNumber: 2, Values: map[string]string{"quantity": test.raw}}, rule)
			if !hasIssueCode(issues, test.code) {
				t.Fatalf("issues = %#v, want %s", issues, test.code)
			}
		})
	}

	quantity, err := ParseQuantity("-0,100")
	if err != nil {
		t.Fatal(err)
	}
	if !quantity.IsNegative() || quantity.Scale() != 3 || quantity.String() != "-0.100" {
		t.Fatalf("quantity = negative:%v scale:%d string:%q", quantity.IsNegative(), quantity.Scale(), quantity.String())
	}
	quantity, err = ParseQuantity("0,100")
	if err != nil || quantity.IsNegative() {
		t.Fatalf("zero quantity = %#v, err = %v", quantity, err)
	}
}

func TestDuplicateQuantityRows(t *testing.T) {
	rows := []MappedRow{
		{RowNumber: 2, Values: map[string]string{"product_code": "A-1", "quantity": "2"}},
		{RowNumber: 3, Values: map[string]string{"product_code": "A-1", "quantity": "2"}},
		{RowNumber: 4, Values: map[string]string{"product_code": "A-1", "quantity": "3"}},
	}
	issues := ValidateDuplicateRows(rows, "product_code")
	if len(issues) != 2 || issues[0].RowNumber != 3 || issues[1].RowNumber != 4 {
		t.Fatalf("duplicate issues = %#v, want rows 3 and 4", issues)
	}
}

func TestRunDryRunDoesNotCommit(t *testing.T) {
	adapter := &testAdapter{}
	engine, err := NewEngine(adapter, []FieldSpec{
		{Name: "product_code", Aliases: []string{"Code"}, Required: true},
		{Name: "quantity", Aliases: []string{"Qty"}, Required: true},
	}, QuantityRule{Field: "quantity", Required: true, MaxScale: 2})
	if err != nil {
		t.Fatal(err)
	}
	job, err := NewImportJob("job-1", "company-1")
	if err != nil {
		t.Fatal(err)
	}
	table, err := NewTable([]string{"Code", "Qty"}, [][]string{{"A-1", "2.50"}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(context.Background(), ProcessRequest{Job: job, Table: table, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Committed || result.Preview.CanCommit == false || !result.Preview.DryRun {
		t.Fatalf("unexpected dry-run result = %#v", result)
	}
	if result.Job.State != JobStateDryRunCompleted {
		t.Fatalf("job state = %s, want %s", result.Job.State, JobStateDryRunCompleted)
	}
	if adapter.validateCalls != 1 || adapter.commitCalls != 0 {
		t.Fatalf("validate calls = %d, commit calls = %d", adapter.validateCalls, adapter.commitCalls)
	}
}

func TestRunCommitIsOneAtomicBatch(t *testing.T) {
	adapter := &testAdapter{commitErr: errors.New("simulated commit failure")}
	engine, err := NewEngine(adapter, []FieldSpec{
		{Name: "product_code", Required: true},
		{Name: "quantity", Required: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, _ := NewImportJob("job-2", "company-1")
	table, err := NewTable([]string{"product_code", "quantity"}, [][]string{{"A-1", "2"}, {"A-2", "3"}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(context.Background(), ProcessRequest{Job: job, Table: table})
	if err == nil || !errors.Is(err, ErrCommitFailed) {
		t.Fatalf("error = %v, want commit failure", err)
	}
	if result.Committed || result.Job.State != JobStateFailed {
		t.Fatalf("unexpected failed result = %#v", result)
	}
	if adapter.commitCalls != 1 || adapter.lastCommitRows != 2 || len(adapter.committed) != 0 {
		t.Fatalf("commit calls = %d, rows = %d, committed = %d", adapter.commitCalls, adapter.lastCommitRows, len(adapter.committed))
	}
}

func TestCSVAndXLSXRoundTrip(t *testing.T) {
	table, err := NewTable([]string{"Ürün Kodu", "Sayılan Miktar"}, [][]string{{"STK-1", "2,50000000"}, {"STK-2", "0"}})
	if err != nil {
		t.Fatal(err)
	}
	var csvPayload bytes.Buffer
	if err = WriteCSV(&csvPayload, table); err != nil {
		t.Fatal(err)
	}
	fromCSV, err := ReadTable(bytes.NewReader(csvPayload.Bytes()), "sayim.csv")
	if err != nil || fromCSV.Rows[0].Values[1] != "2,50000000" {
		t.Fatalf("csv round trip = %#v, err = %v", fromCSV, err)
	}
	var xlsxPayload bytes.Buffer
	if err = WriteXLSX(&xlsxPayload, table); err != nil {
		t.Fatal(err)
	}
	fromXLSX, err := ReadTable(bytes.NewReader(xlsxPayload.Bytes()), "sayim.xlsx")
	if err != nil || fromXLSX.Rows[1].Values[0] != "STK-2" {
		t.Fatalf("xlsx round trip = %#v, err = %v", fromXLSX, err)
	}
}

func TestReadTableRejectsInvalidXLSXCellReference(t *testing.T) {
	// A malformed reference must be reported as a table error, not turn into a
	// negative slice index panic.
	payload := []byte("not an xlsx")
	if _, err := ReadTable(bytes.NewReader(payload), "bad.xlsx"); err == nil {
		t.Fatal("expected invalid xlsx error")
	}
}

type testAdapter struct {
	validateCalls  int
	commitCalls    int
	lastCommitRows int
	commitErr      error
	committed      []MappedRow
}

func (a *testAdapter) Validate(_ context.Context, _ ValidationInput) (ValidationResult, error) {
	a.validateCalls++
	return ValidationResult{}, nil
}

func (a *testAdapter) Commit(_ context.Context, input CommitInput) error {
	a.commitCalls++
	a.lastCommitRows = len(input.Rows)
	if a.commitErr != nil {
		return a.commitErr
	}
	a.committed = append(a.committed, input.Rows...)
	return nil
}

func mappingColumn(mapping Mapping, field string, method MappingMethod) (ColumnMapping, bool) {
	for _, column := range mapping.Columns {
		if column.Field == field && column.Method == method {
			return column, true
		}
	}
	return ColumnMapping{}, false
}

func hasIssueCode(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
