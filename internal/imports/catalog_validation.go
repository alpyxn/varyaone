package imports

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/jackc/pgx/v5"
)

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (a catalogAdapter) validateExtendedRow(ctx context.Context, row dataexchange.MappedRow) ([]dataexchange.Issue, error) {
	switch a.entity {
	case string(dataexchange.EntityProduct):
		return a.validateProductRow(ctx, row)
	case string(dataexchange.EntityVariant):
		return a.validateVariantRow(ctx, row)
	case string(dataexchange.EntityWarehouse):
		return a.validateWarehouseRow(ctx, row)
	case string(dataexchange.EntityParty):
		return a.validatePartyRow(ctx, row)
	default:
		return nil, nil
	}
}

func issue(row int, field, code, message string, severity dataexchange.Severity) dataexchange.Issue {
	return dataexchange.Issue{RowNumber: row, Field: field, Code: code, Severity: severity, Message: message}
}

func valueProvided(values map[string]string, field string) bool {
	return strings.TrimSpace(values[field]) != ""
}

func parseImportPrice(raw string) (string, error) {
	quantity, err := dataexchange.ParseQuantity(raw)
	if err != nil || quantity.IsNegative() || quantity.Scale() > 8 {
		return "", fmt.Errorf("fiyat geçersiz")
	}
	canonical := canonicalImportDecimal(quantity.String())
	integer := strings.SplitN(canonical, ".", 2)[0]
	if strings.HasPrefix(integer, "-") || strings.HasPrefix(integer, "+") {
		integer = integer[1:]
	}
	if len(integer) > 12 {
		return "", fmt.Errorf("fiyat sayısal hassasiyet sınırını aşıyor")
	}
	return quantity.String(), nil
}

func parseImportRate(raw string) (string, error) {
	quantity, err := dataexchange.ParseQuantity(raw)
	if err != nil || quantity.IsNegative() || quantity.Scale() > 8 {
		return "", fmt.Errorf("oran geçersiz")
	}
	ratio, ok := new(big.Rat).SetString(quantity.String())
	if !ok || ratio.Cmp(big.NewRat(100, 1)) > 0 {
		return "", fmt.Errorf("oran 100 değerini aşamaz")
	}
	return quantity.String(), nil
}

func parseImportOpeningStockQuantity(raw string) (string, error) {
	quantity, err := dataexchange.ParseQuantity(raw)
	if err != nil || quantity.IsNegative() || quantity.IsZero() || quantity.Scale() > 8 {
		return "", fmt.Errorf("açılış stoku miktarı geçersiz")
	}
	return quantity.String(), nil
}

func parseImportBool(raw string) (bool, error) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "TRUE", "1", "EVET", "YES", "AKTIF", "AKTİF", "ACTIVE":
		return true, nil
	case "FALSE", "0", "HAYIR", "NO", "PASIF", "PASİF", "INACTIVE":
		return false, nil
	default:
		return false, fmt.Errorf("geçersiz aktiflik değeri")
	}
}

func (a catalogAdapter) validateProductRow(ctx context.Context, row dataexchange.MappedRow) ([]dataexchange.Issue, error) {
	values := row.Values
	code := strings.ToUpper(strings.TrimSpace(values["product_code"]))
	unit := strings.ToUpper(strings.TrimSpace(values["unit"]))
	if !valueProvided(values, "barcode") && valueProvided(values, "barcode_type") {
		return []dataexchange.Issue{issue(row.RowNumber, "barcode_type", "barcode_required", "barkod tipi için birincil barkod da verilmelidir", dataexchange.SeverityError)}, nil
	}
	for _, field := range []string{"purchase_price", "sales_price"} {
		if valueProvided(values, field) {
			if _, err := parseImportPrice(values[field]); err != nil {
				return []dataexchange.Issue{issue(row.RowNumber, field, "invalid_price", "fiyat geçerli, negatif olmayan bir ondalık olmalıdır", dataexchange.SeverityError)}, nil
			}
		}
	}
	if valueProvided(values, "vat_rate") {
		if _, err := parseImportRate(values["vat_rate"]); err != nil {
			return []dataexchange.Issue{issue(row.RowNumber, "vat_rate", "invalid_vat_rate", "KDV oranı 0 ile 100 arasında geçerli bir ondalık olmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	if valueProvided(values, "is_active") {
		if _, err := parseImportBool(values["is_active"]); err != nil {
			return []dataexchange.Issue{issue(row.RowNumber, "is_active", "invalid_boolean", "aktiflik değeri Evet, Hayır, Aktif veya Pasif olmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	var existingID, existingName, existingKind, existingDescription, existingUnit string
	var existingActive, variantsEnabled bool
	err := a.pool.QueryRow(ctx, `SELECT p.id::text,p.name,p.kind::text,COALESCE(p.description,''),p.is_active,p.variants_enabled,COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=p.company_id AND pu.product_id=p.id AND pu.is_base ORDER BY pu.unit_code LIMIT 1),'') FROM products p WHERE p.company_id=$1 AND p.code=$2`, a.companyID, code).Scan(&existingID, &existingName, &existingKind, &existingDescription, &existingActive, &variantsEnabled, &existingUnit)
	if err != nil && !isNoRows(err) {
		return nil, err
	}
	if isNoRows(err) {
		for _, field := range []string{"purchase_price", "sales_price"} {
			if !valueProvided(values, field) {
				return []dataexchange.Issue{issue(row.RowNumber, field, "price_required", "yeni stok kartında alış ve satış fiyatı zorunludur", dataexchange.SeverityError)}, nil
			}
		}
		if valueProvided(values, "barcode") {
			var ignored string
			barcodeErr := a.pool.QueryRow(ctx, `SELECT barcode FROM product_barcodes WHERE company_id=$1 AND barcode=$2`, a.companyID, strings.TrimSpace(values["barcode"])).Scan(&ignored)
			if barcodeErr == nil {
				return []dataexchange.Issue{issue(row.RowNumber, "barcode", "existing_barcode_conflict", "barkod başka bir karta bağlı", dataexchange.SeverityError)}, nil
			}
			if !isNoRows(barcodeErr) {
				return nil, barcodeErr
			}
		}
		return a.validateProductOpeningStock(ctx, row, false)
	}
	if existingKind != "PHYSICAL" || existingUnit != unit {
		return []dataexchange.Issue{issue(row.RowNumber, "product_code", "existing_product_conflict", "stok kodu mevcut kayıtla farklı bilgiler taşıyor", dataexchange.SeverityError)}, nil
	}
	if variantsEnabled && valueProvided(values, "barcode") {
		return []dataexchange.Issue{issue(row.RowNumber, "barcode", "variant_barcode_not_allowed", "varyant modu açık üründe ürün barkodu gönderilemez", dataexchange.SeverityError)}, nil
	}
	if openingIssues, err := a.validateProductOpeningStock(ctx, row, true); err != nil || len(openingIssues) > 0 {
		return openingIssues, err
	}
	if valueProvided(values, "barcode") {
		barcode := strings.TrimSpace(values["barcode"])
		var ownerProduct, ownerVariant string
		var ownerType string
		var primary bool
		barcodeErr := a.pool.QueryRow(ctx, `SELECT product_id::text,COALESCE(variant_id::text,''),barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND barcode=$2`, a.companyID, barcode).Scan(&ownerProduct, &ownerVariant, &ownerType, &primary)
		if barcodeErr != nil && !isNoRows(barcodeErr) {
			return nil, barcodeErr
		}
		if barcodeErr == nil && (ownerProduct != existingID || ownerVariant != "" || !primary) {
			return []dataexchange.Issue{issue(row.RowNumber, "barcode", "existing_barcode_conflict", "barkod firma içinde başka bir kayda bağlı", dataexchange.SeverityError)}, nil
		}
	}
	return nil, nil
}

func (a catalogAdapter) validateProductOpeningStock(ctx context.Context, row dataexchange.MappedRow, existing bool) ([]dataexchange.Issue, error) {
	values := row.Values
	warehouseProvided := valueProvided(values, "opening_stock_warehouse_code")
	quantityProvided := valueProvided(values, "opening_stock_quantity")
	if !warehouseProvided && !quantityProvided {
		return nil, nil
	}
	if existing {
		return []dataexchange.Issue{issue(row.RowNumber, "opening_stock_warehouse_code", "opening_stock_existing_product", "açılış stoğu yalnızca yeni ürün eklenirken kullanılabilir", dataexchange.SeverityError)}, nil
	}
	if !a.allowOpeningStock {
		return []dataexchange.Issue{issue(row.RowNumber, "opening_stock_warehouse_code", "opening_stock_not_authorized", "açılış stoğu aktarmak için stok hareketi yetkisi gereklidir", dataexchange.SeverityError)}, nil
	}
	if warehouseProvided != quantityProvided {
		field := "opening_stock_warehouse_code"
		if warehouseProvided {
			field = "opening_stock_quantity"
		}
		return []dataexchange.Issue{issue(row.RowNumber, field, "opening_stock_pair_required", "açılış deposu ve açılış miktarı birlikte verilmelidir", dataexchange.SeverityError)}, nil
	}
	if _, err := parseImportOpeningStockQuantity(values["opening_stock_quantity"]); err != nil {
		return []dataexchange.Issue{issue(row.RowNumber, "opening_stock_quantity", "invalid_opening_stock_quantity", "açılış miktarı sıfırdan büyük, geçerli bir ondalık olmalıdır", dataexchange.SeverityError)}, nil
	}
	warehouseCode := strings.ToUpper(strings.TrimSpace(values["opening_stock_warehouse_code"]))
	var allowed bool
	if strings.TrimSpace(a.actorID) != "" {
		err := a.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM warehouses w
			WHERE w.company_id=$1 AND w.code=$2 AND w.is_active
			  AND w.warehouse_type='STANDARD' AND NOT w.is_system AND NOT w.is_transit
			  AND (w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3)
			       OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=$1 AND bs.user_id=$3 AND bs.branch_id=w.branch_id))
			  AND (NOT EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3)
			       OR EXISTS(SELECT 1 FROM membership_warehouse_scopes ws WHERE ws.company_id=$1 AND ws.user_id=$3 AND ws.warehouse_id=w.id))
		)`, a.companyID, warehouseCode, a.actorID).Scan(&allowed)
		if err != nil {
			return nil, err
		}
	}
	if !allowed {
		return []dataexchange.Issue{issue(row.RowNumber, "opening_stock_warehouse_code", "opening_stock_warehouse_not_authorized", "açılış stoğu için aktif ve yetkili bir standart depo seçin", dataexchange.SeverityError)}, nil
	}
	return nil, nil
}

func (a catalogAdapter) validateVariantRow(ctx context.Context, row dataexchange.MappedRow) ([]dataexchange.Issue, error) {
	values := row.Values
	productCode := strings.ToUpper(strings.TrimSpace(values["product_code"]))
	variantCode := strings.ToUpper(strings.TrimSpace(values["variant_code"]))
	if !valueProvided(values, "barcode") && valueProvided(values, "barcode_type") {
		return []dataexchange.Issue{issue(row.RowNumber, "barcode_type", "barcode_required", "barkod tipi için birincil barkod da verilmelidir", dataexchange.SeverityError)}, nil
	}
	var productID string
	var enabled bool
	err := a.pool.QueryRow(ctx, `SELECT id::text,variants_enabled FROM products WHERE company_id=$1 AND code=$2`, a.companyID, productCode).Scan(&productID, &enabled)
	if isNoRows(err) {
		return []dataexchange.Issue{issue(row.RowNumber, "product_code", "unknown_product", "ürün bulunamadı", dataexchange.SeverityError)}, nil
	}
	if err != nil {
		return nil, err
	}
	if !enabled {
		return []dataexchange.Issue{issue(row.RowNumber, "variant_values", "variant_mode_required", "ürün varyant modunda değil", dataexchange.SeverityError)}, nil
	}
	signature, pairs, pairErr := resolveImportVariantValues(ctx, a.pool, a.companyID, productID, values["variant_values"])
	if pairErr != nil {
		return []dataexchange.Issue{issue(row.RowNumber, "variant_values", "invalid_variant_values", pairErr.Error(), dataexchange.SeverityError)}, nil
	}
	if valueProvided(values, "purchase_price") {
		if _, err := parseImportPrice(values["purchase_price"]); err != nil {
			return []dataexchange.Issue{issue(row.RowNumber, "purchase_price", "invalid_price", "alış fiyatı geçerli, negatif olmayan bir ondalık olmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	if valueProvided(values, "sales_price") {
		if _, err := parseImportPrice(values["sales_price"]); err != nil {
			return []dataexchange.Issue{issue(row.RowNumber, "sales_price", "invalid_price", "satış fiyatı geçerli, negatif olmayan bir ondalık olmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	var existingID, existingProduct, existingSignature string
	var moved bool
	err = a.pool.QueryRow(ctx, `SELECT v.id::text,v.product_id::text,v.variant_signature,EXISTS(SELECT 1 FROM stock_movements sm WHERE sm.company_id=v.company_id AND sm.variant_id=v.id) FROM product_variants v WHERE v.company_id=$1 AND v.product_id=$2 AND v.variant_code=$3`, a.companyID, productID, variantCode).Scan(&existingID, &existingProduct, &existingSignature, &moved)
	if err != nil && !isNoRows(err) {
		return nil, err
	}
	if err == nil && existingSignature != signature {
		message := "varyant değeri mevcut kayıtla farklı; çakışmayı çözün"
		if moved {
			message = "stok hareketi görmüş varyantın kimliği değiştirilemez"
		}
		return []dataexchange.Issue{issue(row.RowNumber, "variant_values", "existing_variant_conflict", message, dataexchange.SeverityError)}, nil
	}
	if valueProvided(values, "barcode") {
		barcode := strings.TrimSpace(values["barcode"])
		barcodeType := strings.ToUpper(strings.TrimSpace(values["barcode_type"]))
		if barcodeType == "" {
			barcodeType = "EAN"
		}
		var ownerProduct, ownerVariant, ownerType string
		var primary bool
		barcodeErr := a.pool.QueryRow(ctx, `SELECT product_id::text,COALESCE(variant_id::text,''),barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND barcode=$2`, a.companyID, barcode).Scan(&ownerProduct, &ownerVariant, &ownerType, &primary)
		if barcodeErr != nil && !isNoRows(barcodeErr) {
			return nil, barcodeErr
		}
		if barcodeErr == nil {
			if ownerProduct != productID || ownerVariant != existingID || ownerType != barcodeType || !primary {
				return []dataexchange.Issue{issue(row.RowNumber, "barcode", "existing_barcode_conflict", "varyantın birincil barkodu değiştirilemez; çakışmayı çözün", dataexchange.SeverityError)}, nil
			}
		} else if existingID != "" {
			var hasPrimary bool
			if err := a.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id=$3 AND is_primary)`, a.companyID, productID, existingID).Scan(&hasPrimary); err != nil {
				return nil, err
			}
			if hasPrimary {
				return []dataexchange.Issue{issue(row.RowNumber, "barcode", "existing_barcode_conflict", "varyantın birincil barkodu değiştirilemez; çakışmayı çözün", dataexchange.SeverityError)}, nil
			}
		}
	}
	_ = pairs
	return nil, nil
}

type variantImportPair struct {
	DefinitionID string
	OptionID     string
}

func resolveImportVariantValues(ctx context.Context, q queryRower, companyID, productID, raw string) (string, []variantImportPair, error) {
	var values map[string]string
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	if err := decoder.Decode(&values); err != nil || len(values) == 0 {
		return "", nil, fmt.Errorf("varyant değerleri standart JSON nesnesi olmalıdır")
	}
	var definitionCount int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM product_variant_definitions WHERE company_id=$1 AND product_id=$2`, companyID, productID).Scan(&definitionCount); err != nil {
		return "", nil, err
	}
	if definitionCount == 0 || len(values) != definitionCount {
		return "", nil, fmt.Errorf("ürünün tanımlı tüm varyant boyutları verilmelidir")
	}
	pairs := make([]variantImportPair, 0, len(values))
	seen := map[string]bool{}
	for definitionCode, optionCode := range values {
		definitionCode = strings.ToUpper(strings.TrimSpace(definitionCode))
		optionCode = strings.ToUpper(strings.TrimSpace(optionCode))
		var pair variantImportPair
		var active bool
		err := q.QueryRow(ctx, `SELECT d.id::text,o.id::text,d.is_active AND o.is_active AND EXISTS(SELECT 1 FROM product_variant_allowed_options a WHERE a.company_id=$1 AND a.product_id=$2 AND a.definition_id=d.id AND a.option_id=o.id) FROM product_variant_definitions pd JOIN variant_definitions d ON d.company_id=pd.company_id AND d.id=pd.definition_id JOIN variant_definition_options o ON o.company_id=d.company_id AND o.definition_id=d.id WHERE pd.company_id=$1 AND pd.product_id=$2 AND d.code=$3 AND o.code=$4`, companyID, productID, definitionCode, optionCode).Scan(&pair.DefinitionID, &pair.OptionID, &active)
		if isNoRows(err) || err == nil && !active {
			return "", nil, fmt.Errorf("varyant boyutu veya seçeneği ürün konfigürasyonunda yok")
		}
		if err != nil {
			return "", nil, err
		}
		if seen[pair.DefinitionID] {
			return "", nil, fmt.Errorf("varyant boyutu tekrarlı")
		}
		seen[pair.DefinitionID] = true
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].DefinitionID < pairs[j].DefinitionID })
	parts := make([]string, len(pairs))
	for i, pair := range pairs {
		parts[i] = pair.DefinitionID + "=" + pair.OptionID
	}
	return strings.Join(parts, "|"), pairs, nil
}

func (a catalogAdapter) validateWarehouseRow(ctx context.Context, row dataexchange.MappedRow) ([]dataexchange.Issue, error) {
	code := strings.ToUpper(strings.TrimSpace(row.Values["warehouse_code"]))
	branchCode := strings.ToUpper(strings.TrimSpace(row.Values["branch_code"]))
	if branchCode != "" {
		var branchID string
		var allowed bool
		err := a.pool.QueryRow(ctx, `SELECT b.id::text,(b.is_active AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=b.company_id AND bs.user_id=$2) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=b.company_id AND bs.user_id=$2 AND bs.branch_id=b.id))) FROM branches b WHERE b.company_id=$1 AND b.code=$3`, a.companyID, a.actorID, branchCode).Scan(&branchID, &allowed)
		if isNoRows(err) || err == nil && !allowed {
			return []dataexchange.Issue{issue(row.RowNumber, "branch_code", "branch_not_authorized", "şube bulunamadı veya bu şubeye yetkiniz yok", dataexchange.SeverityError)}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	var existingName, existingType, existingBranch string
	var active, system, transit bool
	err := a.pool.QueryRow(ctx, `SELECT name,warehouse_type,is_active,is_system,is_transit,COALESCE((SELECT b.code FROM branches b WHERE b.company_id=w.company_id AND b.id=w.branch_id),'') FROM warehouses w WHERE w.company_id=$1 AND w.code=$2`, a.companyID, code).Scan(&existingName, &existingType, &active, &system, &transit, &existingBranch)
	if err != nil && !isNoRows(err) {
		return nil, err
	}
	if err == nil && (existingType != "STANDARD" || system || transit || !active) {
		return []dataexchange.Issue{issue(row.RowNumber, "warehouse_code", "warehouse_out_of_scope", "yalnızca aktif standart operasyon depoları aktarılabilir", dataexchange.SeverityError)}, nil
	}
	if err == nil && branchCode != "" && existingBranch != branchCode {
		return []dataexchange.Issue{issue(row.RowNumber, "branch_code", "existing_warehouse_conflict", "mevcut deponun şubesi değiştirilemez", dataexchange.SeverityError)}, nil
	}
	return nil, nil
}

func (a catalogAdapter) validatePartyRow(ctx context.Context, row dataexchange.MappedRow) ([]dataexchange.Issue, error) {
	values := row.Values
	kind := normalizePartyKind(values["kind"])
	roles := normalizePartyRoles(values["roles"])
	if kind != "PERSON" && kind != "ORGANIZATION" {
		return []dataexchange.Issue{issue(row.RowNumber, "kind", "invalid_party_kind", "cari türü kişi veya kurum olmalıdır", dataexchange.SeverityError)}, nil
	}
	if roles == "" {
		return []dataexchange.Issue{issue(row.RowNumber, "roles", "invalid_party_roles", "cari rolü müşteri, tedarikçi veya her ikisi olmalıdır", dataexchange.SeverityError)}, nil
	}
	currency := strings.ToUpper(strings.TrimSpace(values["currency"]))
	if currency != "" && len(currency) != 3 {
		return []dataexchange.Issue{issue(row.RowNumber, "currency", "invalid_currency", "para birimi üç harfli olmalıdır", dataexchange.SeverityError)}, nil
	}
	for field, size := range map[string]int{"tax_number": 10, "identity_number": 11} {
		if valueProvided(values, field) && (len(strings.TrimSpace(values[field])) != size || !allDigits(values[field])) {
			return []dataexchange.Issue{issue(row.RowNumber, field, "invalid_identity_number", "kimlik bilgisi yalnızca beklenen rakamlardan oluşmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	code := strings.ToUpper(strings.TrimSpace(values["party_code"]))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(values["code"]))
	}
	var existingID, existingKind, existingLegalName, existingFirstName, existingLastName, existingTaxNumber, existingIdentityNumber, existingCurrency string
	err := a.pool.QueryRow(ctx, `SELECT id::text,kind::text,COALESCE(legal_name,''),COALESCE(first_name,''),COALESCE(last_name,''),COALESCE(tax_number,''),COALESCE(identity_number,''),btrim(default_currency::text) FROM parties WHERE company_id=$1 AND code=$2`, a.companyID, code).Scan(&existingID, &existingKind, &existingLegalName, &existingFirstName, &existingLastName, &existingTaxNumber, &existingIdentityNumber, &existingCurrency)
	if err != nil && !isNoRows(err) {
		return nil, err
	}
	effective := func(field, current string) string {
		if valueProvided(values, field) {
			return strings.TrimSpace(values[field])
		}
		return current
	}
	effectiveFirstName := effective("first_name", existingFirstName)
	effectiveLastName := effective("last_name", existingLastName)
	effectiveLegalName := effective("legal_name", existingLegalName)
	if kind == "ORGANIZATION" && effectiveLegalName == "" && valueProvided(values, "name") {
		effectiveLegalName = strings.TrimSpace(values["name"])
	}
	if kind == "PERSON" && (effectiveFirstName == "" || effectiveLastName == "") {
		return []dataexchange.Issue{issue(row.RowNumber, "first_name", "party_name_required", "kişi carisinde ad ve soyad zorunludur", dataexchange.SeverityError)}, nil
	}
	if kind == "ORGANIZATION" && effectiveLegalName == "" {
		return []dataexchange.Issue{issue(row.RowNumber, "legal_name", "party_name_required", "kurum carisinde resmî unvan zorunludur", dataexchange.SeverityError)}, nil
	}
	if err == nil {
		if existingKind != kind {
			return []dataexchange.Issue{issue(row.RowNumber, "kind", "existing_party_identity", "cari türü oluşturulduktan sonra değiştirilemez", dataexchange.SeverityError)}, nil
		}
	}
	effectiveTaxNumber := effective("tax_number", existingTaxNumber)
	effectiveIdentityNumber := effective("identity_number", existingIdentityNumber)
	if effectiveTaxNumber != "" || effectiveIdentityNumber != "" {
		var duplicate bool
		if err := a.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id<>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND (($3<>'' AND tax_number=$3) OR ($4<>'' AND identity_number=$4)))`, a.companyID, existingID, effectiveTaxNumber, effectiveIdentityNumber).Scan(&duplicate); err != nil {
			return nil, err
		}
		if duplicate {
			var policy string
			if err := a.pool.QueryRow(ctx, `SELECT duplicate_party_tax_number_policy::text FROM companies WHERE id=$1`, a.companyID).Scan(&policy); err != nil {
				return nil, err
			}
			switch policy {
			case "BLOCK":
				return []dataexchange.Issue{issue(row.RowNumber, "tax_number", "duplicate_tax_number", "vergi veya T.C. kimlik numarası başka bir caride kayıtlı", dataexchange.SeverityError)}, nil
			case "WARN":
				return []dataexchange.Issue{issue(row.RowNumber, "tax_number", "duplicate_tax_number", "vergi veya T.C. kimlik numarası başka bir caride de kullanılıyor", dataexchange.SeverityWarning)}, nil
			}
		}
	}
	if partyAddressProvided(values) && !valueProvided(values, "province_name") {
		return []dataexchange.Issue{issue(row.RowNumber, "province_name", "incomplete_address", "adres bilgisi gönderildiğinde il zorunludur", dataexchange.SeverityError)}, nil
	}
	if valueProvided(values, "is_active") {
		if _, err := parseImportBool(values["is_active"]); err != nil {
			return []dataexchange.Issue{issue(row.RowNumber, "is_active", "invalid_boolean", "aktiflik değeri Evet, Hayır, Aktif veya Pasif olmalıdır", dataexchange.SeverityError)}, nil
		}
	}
	return nil, nil
}

func normalizePartyRoles(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "[") {
		var values []string
		if err := json.Unmarshal([]byte(raw), &values); err == nil {
			raw = strings.Join(values, ",")
		}
	}
	raw = normalizePartyToken(raw)
	if raw == "BOTH" || raw == "HER IKISI" || raw == "MUSTERI VE TEDARIKCI" || raw == "CUSTOMER AND SUPPLIER" {
		return "BOTH"
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '+' || r == '/' || r == ';' || r == '|' || r == ' ' || r == '\t' || r == '\n'
	})
	seenCustomer, seenSupplier := false, false
	for _, part := range parts {
		switch part {
		case "CUSTOMER", "MUSTERI":
			seenCustomer = true
		case "SUPPLIER", "TEDARIKCI":
			seenSupplier = true
		}
	}
	if seenCustomer && seenSupplier {
		return "BOTH"
	}
	if seenCustomer {
		return "CUSTOMER"
	}
	if seenSupplier {
		return "SUPPLIER"
	}
	return ""
}

func normalizePartyKind(raw string) string {
	switch normalizePartyToken(raw) {
	case "PERSON", "KISI":
		return "PERSON"
	case "ORGANIZATION", "ORGANISATION", "KURUM":
		return "ORGANIZATION"
	default:
		return ""
	}
}

func normalizePartyToken(raw string) string {
	return strings.NewReplacer(
		"İ", "I", "Ş", "S", "Ğ", "G", "Ü", "U", "Ö", "O", "Ç", "C",
		"ı", "i", "ş", "s", "ğ", "g", "ü", "u", "ö", "o", "ç", "c",
	).Replace(strings.ToUpper(strings.TrimSpace(raw)))
}

func partyAddressProvided(values map[string]string) bool {
	for _, field := range []string{"address_line", "province_name", "district_name", "neighborhood_name"} {
		if valueProvided(values, field) {
			return true
		}
	}
	return false
}

func allDigits(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isNoRows(err error) bool { return err == pgx.ErrNoRows }
