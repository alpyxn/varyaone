package dataexchange

// EntityType is the canonical import/export unit. One job may never mix
// entity types; dependencies are shown to the UI before a user commits.
type EntityType string

const (
	EntityProduct      EntityType = "PRODUCT"
	EntityVariant      EntityType = "VARIANT"
	EntityBarcode      EntityType = "BARCODE"
	EntityWarehouse    EntityType = "WAREHOUSE"
	EntityParty        EntityType = "PARTY"
	EntityPriceList    EntityType = "PRICE_LIST"
	EntityOpeningStock EntityType = "OPENING_STOCK"
	EntityStockCount   EntityType = "STOCK_COUNT"
)

type EntitySpec struct {
	Type         EntityType   `json:"type"`
	Label        string       `json:"label"`
	Fields       []FieldSpec  `json:"fields"`
	Dependencies []EntityType `json:"dependencies,omitempty"`
	Importable   bool         `json:"importable"`
	Exportable   bool         `json:"exportable"`
}

// InitialEntitySpecs is the provider-neutral catalog shared by import and
// export screens. Domain adapters add database validation and commits later.
// The Name of every field is a stable target ID; Turkish labels are kept in
// Label and Aliases so they never become part of a mapping payload.
func InitialEntitySpecs() []EntitySpec {
	field := func(name, label string, fieldType FieldType, required bool, example string, aliases ...string) FieldSpec {
		allAliases := make([]string, 0, len(aliases)+1)
		allAliases = append(allAliases, label)
		allAliases = append(allAliases, aliases...)
		return FieldSpec{Name: name, Label: label, Type: fieldType, Aliases: allAliases, Required: required, Example: example}
	}
	return []EntitySpec{
		{
			Type: EntityProduct, Label: "Ürün", Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu", "Kod"),
				field("product_name", "Stok Adı", FieldTypeString, true, "Pamuklu Tişört", "Ürün Adı", "Ad"),
				field("unit", "Birim", FieldTypeString, true, "ADET"),
				field("description", "Açıklama", FieldTypeString, false, "Kırmızı pamuklu tişört"),
				field("is_active", "Aktif", FieldTypeBoolean, false, "Evet", "Aktiflik"),
				field("barcode", "Birincil Barkod", FieldTypeString, false, "869000000001", "Barkod"),
				field("barcode_type", "Barkod Tipi", FieldTypeString, false, "EAN13"),
				field("purchase_price", "Alış Fiyatı", FieldTypeDecimal, false, "125.50"),
				field("sales_price", "Satış Fiyatı", FieldTypeDecimal, false, "199.90"),
				field("vat_rate", "KDV Oranı (%)", FieldTypeDecimal, false, "20", "KDV Oranı", "KDV"),
				field("opening_stock_warehouse_code", "Açılış Deposu", FieldTypeString, false, "MERKEZ", "Açılış Depo Kodu", "Açılış Stok Deposu"),
				field("opening_stock_quantity", "Açılış Miktarı", FieldTypeDecimal, false, "10.5", "Açılış Stoğu", "Açılış Stok Miktarı"),
			},
		},
		{
			Type: EntityVariant, Label: "Varyant", Dependencies: []EntityType{EntityProduct}, Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu"),
				field("variant_code", "Varyant Kodu", FieldTypeString, true, "KIRMIZI-M"),
				field("variant_values", "Varyant Değerleri", FieldTypeJSON, true, `{"RENK":"KIRMIZI","BEDEN":"M"}`, "Değerler"),
				field("barcode", "Birincil Barkod", FieldTypeString, false, "869000000001", "Barkod"),
				field("barcode_type", "Barkod Tipi", FieldTypeString, false, "EAN13"),
				field("purchase_price", "Alış Fiyatı", FieldTypeDecimal, false, "130.00"),
				field("sales_price", "Satış Fiyatı", FieldTypeDecimal, false, "209.90"),
			},
		},
		{
			Type: EntityBarcode, Label: "Barkod", Dependencies: []EntityType{EntityProduct, EntityVariant}, Importable: false, Exportable: true,
			Fields: []FieldSpec{
				field("barcode", "Barkod", FieldTypeString, true, "869000000001"),
				field("owner", "Sahip", FieldTypeString, true, "Ürün:STK-001", "Ürün/Varyant Sahibi"),
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu"),
				field("variant_code", "Varyant Kodu", FieldTypeString, false, "KIRMIZI-M"),
				field("barcode_type", "Barkod Tipi", FieldTypeString, true, "EAN13"),
				field("is_primary", "Birincil", FieldTypeBoolean, true, "Evet", "Birincil Barkod"),
			},
		},
		{
			Type: EntityWarehouse, Label: "Depo", Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("warehouse_code", "Depo Kodu", FieldTypeString, true, "MERKEZ", "Kod"),
				field("warehouse_name", "Depo Adı", FieldTypeString, true, "Merkez Depo", "Ad"),
				field("branch_code", "Şube Kodu", FieldTypeString, false, "IST-01"),
			},
		},
		{
			Type: EntityParty, Label: "Cari", Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("code", "Cari Kodu", FieldTypeString, true, "CARI-001", "Kod"),
				field("kind", "Cari Türü", FieldTypeString, true, "Kurum", "Tür"),
				field("roles", "Roller", FieldTypeJSON, true, `["Müşteri"]`, "Müşteri/Tedarikçi Rolleri"),
				field("name", "Cari Adı", FieldTypeString, false, "Örnek Müşteri", "Ad Soyad", "Unvan"),
				field("legal_name", "Resmî Unvan", FieldTypeString, false, "Örnek Müşteri Anonim Şirketi"),
				field("trade_name", "Ticari Ad", FieldTypeString, false, "Örnek Müşteri"),
				field("first_name", "Ad", FieldTypeString, false, "Ayşe"),
				field("last_name", "Soyad", FieldTypeString, false, "Yılmaz"),
				field("tax_number", "Vergi Numarası", FieldTypeString, false, "1234567890", "Vergi No"),
				field("identity_number", "T.C. Kimlik Numarası", FieldTypeString, false, "12345678901", "Kimlik No"),
				field("tax_office", "Vergi Dairesi", FieldTypeString, false, "Kadıköy"),
				field("currency", "Para Birimi", FieldTypeString, false, "TRY"),
				field("phone", "Telefon", FieldTypeString, false, "+90 212 555 00 00"),
				field("email", "E-posta", FieldTypeString, false, "ornek@example.com", "E-posta Adresi"),
				field("address_line", "Adres", FieldTypeString, false, "Bağdat Caddesi No: 1"),
				field("province_name", "İl", FieldTypeString, false, "İstanbul", "Şehir"),
				field("district_name", "İlçe", FieldTypeString, false, "Kadıköy"),
				field("neighborhood_name", "Mahalle", FieldTypeString, false, "Caddebostan"),
				field("is_active", "Aktif", FieldTypeBoolean, false, "Evet", "Aktiflik"),
			},
		},
		{
			Type: EntityPriceList, Label: "Fiyat listesi", Dependencies: []EntityType{EntityProduct, EntityVariant}, Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("price_list_code", "Fiyat Listesi Kodu", FieldTypeString, true, "PERAKENDE"),
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu"),
				field("variant_code", "Varyant Kodu", FieldTypeString, false, "KIRMIZI-M"),
				field("price", "Fiyat", FieldTypeDecimal, true, "199.90"),
			},
		},
		{
			Type: EntityOpeningStock, Label: "Açılış stoku", Dependencies: []EntityType{EntityWarehouse, EntityProduct, EntityVariant}, Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("warehouse_code", "Depo Kodu", FieldTypeString, true, "MERKEZ"),
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu"),
				field("variant_code", "Varyant Kodu", FieldTypeString, false, "KIRMIZI-M"),
				field("quantity", "Miktar", FieldTypeDecimal, true, "10.50000000", "Açılış Miktarı"),
			},
		},
		{
			Type: EntityStockCount, Label: "Stok sayımı", Dependencies: []EntityType{EntityWarehouse, EntityProduct, EntityVariant, EntityBarcode}, Importable: true, Exportable: true,
			Fields: []FieldSpec{
				field("line_no", "Sayım Satır No", FieldTypeInteger, true, "1", "Satır No"),
				field("product_code", "Stok Kodu", FieldTypeString, true, "STK-001", "Ürün Kodu"),
				field("variant_code", "Varyant Kodu", FieldTypeString, false, "KIRMIZI-M"),
				field("barcode", "Barkod", FieldTypeString, false, "869000000001"),
				field("unit", "Birim", FieldTypeString, false, "ADET"),
				field("system_quantity", "Sistem Miktarı", FieldTypeDecimal, false, "10.50000000"),
				field("counted_quantity", "Sayılan Miktar", FieldTypeDecimal, false, "11.00000000", "Miktar"),
				field("difference", "Fark", FieldTypeDecimal, false, "0.50000000"),
				field("status", "Durum", FieldTypeString, false, "Sayıldı"),
			},
		},
	}
}
