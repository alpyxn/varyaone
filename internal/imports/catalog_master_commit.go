package imports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/inventory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The catalog commit functions below deliberately keep one transaction for a
// complete job. They update mutable master data, append an opening-stock
// movement only for a newly inserted product, and never mutate posted stock or
// party ledger history.

func (s *Service) commitImportedProductRow(ctx context.Context, tx pgx.Tx, companyID, actorID, jobID string, row catalogImportRow, allowOpeningStock bool) error {
	if err := lockImportedProductCodeSequence(ctx, tx, companyID); err != nil {
		return err
	}
	values := row.Values
	code := strings.ToUpper(normalizedValue(values, "product_code"))
	name := normalizedValue(values, "product_name")
	unit := strings.ToUpper(normalizedValue(values, "unit"))
	description := normalizedValue(values, "description")
	active := true
	if valueProvided(values, "is_active") {
		var err error
		active, err = parseImportBool(values["is_active"])
		if err != nil {
			return err
		}
	}
	purchase, err := importPriceOrEmpty(values["purchase_price"])
	if err != nil {
		return err
	}
	sales, err := importPriceOrEmpty(values["sales_price"])
	if err != nil {
		return err
	}
	vatRate, err := importRateOrEmpty(values["vat_rate"])
	if err != nil {
		return err
	}
	barcode := normalizedValue(values, "barcode")
	barcodeType := normalizedBarcodeType(values["barcode_type"])
	openingLine, openingStockProvided, err := importedOpeningStockLine(values, row.Number)
	if err != nil {
		return err
	}

	var existingID, existingName, existingKind, existingDescription, existingPurchase, existingSales, existingPurchaseTaxRate, existingSalesTaxRate, existingUnit string
	var existingActive, existingPurchaseIncluded, existingSalesIncluded, existingVariantsEnabled bool
	err = tx.QueryRow(ctx, `SELECT p.id::text,p.name,p.kind::text,COALESCE(p.description,''),p.purchase_price::text,p.sales_price::text,
		COALESCE(p.purchase_tax_rate::text,'0'),COALESCE(p.sales_tax_rate::text,'0'),p.is_active,p.purchase_tax_included,p.sales_tax_included,
		p.variants_enabled,
		COALESCE((SELECT pu.unit_code FROM product_units pu WHERE pu.company_id=p.company_id AND pu.product_id=p.id AND pu.is_base ORDER BY pu.unit_code LIMIT 1),'')
		FROM products p WHERE p.company_id=$1 AND p.code=$2 FOR UPDATE`, companyID, code).
		Scan(&existingID, &existingName, &existingKind, &existingDescription, &existingPurchase, &existingSales, &existingPurchaseTaxRate, &existingSalesTaxRate, &existingActive, &existingPurchaseIncluded, &existingSalesIncluded, &existingVariantsEnabled, &existingUnit)
	if err == nil {
		if existingKind != "PHYSICAL" || existingUnit != unit {
			return fmt.Errorf("%w: stok kartı kimliği farklı", ErrIdentityConflict)
		}
		// A committed catalog transaction may be retried after the final import
		// metadata update returned an uncertain result. The deterministic product
		// ID proves that this is the same source row; a different import job must
		// never add opening stock to an already existing product.
		replayingImportedRow := existingID == stableImportID(jobID, row.Number)
		if (openingStockProvided || valueProvided(values, "opening_stock_warehouse_code") || valueProvided(values, "opening_stock_quantity")) && !replayingImportedRow {
			return fmt.Errorf("%w: mevcut ürüne açılış stoğu eklenemez", ErrOpeningStockExistingProduct)
		}
		barcodeChanged := false
		if barcode != "" {
			if existingVariantsEnabled {
				return fmt.Errorf("%w: varyant modu açık üründe ürün barkodu aktarımı yapılamaz", ErrIdentityConflict)
			}
			barcodeChanged, err = syncImportedProductBarcode(ctx, tx, companyID, existingID, jobID, row.Number, barcode, barcodeType, valueProvided(values, "barcode_type"))
			if err != nil {
				return err
			}
		}

		nameChanged := existingName != name
		descriptionChanged := valueProvided(values, "description") && existingDescription != description
		activeChanged := valueProvided(values, "is_active") && existingActive != active
		purchaseChanged := purchase != "" && !sameImportDecimal(existingPurchase, purchase)
		salesChanged := sales != "" && !sameImportDecimal(existingSales, sales)
		vatChanged := vatRate != "" && (!sameImportDecimal(existingPurchaseTaxRate, vatRate) || !sameImportDecimal(existingSalesTaxRate, vatRate))
		if nameChanged || descriptionChanged || activeChanged || purchaseChanged || salesChanged || vatChanged {
			if _, err := tx.Exec(ctx, `UPDATE products SET name=$1,
				description=CASE WHEN $2 THEN $3 ELSE description END,
				is_active=CASE WHEN $4 THEN $5 ELSE is_active END,
				purchase_price=CASE WHEN $6 THEN $7 ELSE purchase_price END,
				sales_price=CASE WHEN $8 THEN $9 ELSE sales_price END,
				purchase_tax_rate=CASE WHEN $10 THEN $11 ELSE purchase_tax_rate END,
				sales_tax_rate=CASE WHEN $10 THEN $11 ELSE sales_tax_rate END,
				updated_at=now(),version=version+1
				WHERE company_id=$12 AND id=$13`, name, descriptionChanged, description, activeChanged, active, purchaseChanged, purchase, salesChanged, sales, vatRate != "", vatRate, companyID, existingID); err != nil {
				return err
			}
		}
		if vatRate != "" {
			if err := upsertImportedProductTaxRate(ctx, tx, companyID, existingID, vatRate, existingPurchaseIncluded, existingSalesIncluded); err != nil {
				return err
			}
		}
		if openingStockProvided {
			if !allowOpeningStock {
				return ErrOpeningStockNotAuthorized
			}
			if err := inventory.PostOpeningStockImportTx(ctx, tx, companyID, actorID, jobID, []inventory.OpeningStockImportLine{openingLine}); err != nil {
				return err
			}
		}
		if nameChanged || descriptionChanged || activeChanged || purchaseChanged || salesChanged || vatChanged || barcodeChanged {
			if err := refreshImportedProductSearch(ctx, tx, companyID, existingID); err != nil {
				return err
			}
			changes := map[string]any{"source_job_id": jobID}
			if nameChanged {
				changes["name"] = name
			}
			if descriptionChanged {
				changes["description"] = description
			}
			if activeChanged {
				changes["is_active"] = active
			}
			if purchaseChanged {
				changes["purchase_price"] = purchase
			}
			if salesChanged {
				changes["sales_price"] = sales
			}
			if vatChanged {
				changes["vat_rate"] = vatRate
			}
			if barcodeChanged {
				changes["barcode"] = barcode
				if valueProvided(values, "barcode_type") {
					changes["barcode_type"] = barcodeType
				}
			}
			return writeImportAudit(ctx, tx, companyID, actorID, "PRODUCT_UPDATED", "product", existingID, changes)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if purchase == "" || sales == "" {
		return fmt.Errorf("yeni stok kartında alış ve satış fiyatı zorunludur")
	}
	id := stableImportID(jobID, row.Number)
	if barcode != "" {
		available, err := ensureImportedBarcode(ctx, tx, companyID, id, "", barcode, barcodeType)
		if err != nil {
			return err
		}
		if available {
			return fmt.Errorf("%w: barkod zaten başka bir kayda ait", ErrIdentityConflict)
		}
	}
	insertedVATRate := vatRate
	if insertedVATRate == "" {
		insertedVATRate = "0"
	}
	if _, err := tx.Exec(ctx, `INSERT INTO products(id,company_id,code,name,kind,description,purchase_price,sales_price,purchase_tax_rate,sales_tax_rate,is_active) VALUES($1,$2,$3,$4,'PHYSICAL',$5,$6,$7,$8,$8,$9)`, id, companyID, code, name, description, purchase, sales, insertedVATRate, active); err != nil {
		return err
	}
	unitResult, err := tx.Exec(ctx, `INSERT INTO product_units(company_id,product_id,unit_code,is_base,conversion_factor,decimal_scale) SELECT $1,$2,code,true,1,decimal_scale FROM units WHERE code=$3`, companyID, id, unit)
	if err != nil {
		return err
	}
	if unitResult.RowsAffected() != 1 {
		return fmt.Errorf("birim bulunamadı")
	}
	if barcode != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,true)`, stableImportID(jobID, row.Number+100000000), companyID, id, barcode, barcodeType); err != nil {
			return err
		}
	}
	if err := refreshImportedProductSearch(ctx, tx, companyID, id); err != nil {
		return err
	}
	if err := upsertImportedProductTaxRate(ctx, tx, companyID, id, insertedVATRate, false, false); err != nil {
		return err
	}
	if openingStockProvided {
		if !allowOpeningStock {
			return ErrOpeningStockNotAuthorized
		}
		if err := inventory.PostOpeningStockImportTx(ctx, tx, companyID, actorID, jobID, []inventory.OpeningStockImportLine{openingLine}); err != nil {
			return err
		}
	}
	return writeImportAudit(ctx, tx, companyID, actorID, "PRODUCT_IMPORTED", "product", id, map[string]any{"source_job_id": jobID})
}

func importRateOrEmpty(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	return parseImportRate(raw)
}

func importedOpeningStockLine(values map[string]string, rowNumber int) (inventory.OpeningStockImportLine, bool, error) {
	warehouseProvided := valueProvided(values, "opening_stock_warehouse_code")
	quantityProvided := valueProvided(values, "opening_stock_quantity")
	if !warehouseProvided && !quantityProvided {
		return inventory.OpeningStockImportLine{}, false, nil
	}
	if warehouseProvided != quantityProvided {
		return inventory.OpeningStockImportLine{}, false, fmt.Errorf("açılış deposu ve açılış miktarı birlikte verilmelidir")
	}
	quantity, err := parseImportOpeningStockQuantity(values["opening_stock_quantity"])
	if err != nil {
		return inventory.OpeningStockImportLine{}, false, err
	}
	return inventory.OpeningStockImportLine{
		WarehouseCode: strings.ToUpper(strings.TrimSpace(values["opening_stock_warehouse_code"])),
		ProductCode:   strings.ToUpper(normalizedValue(values, "product_code")),
		Quantity:      quantity,
		RowNumber:     rowNumber,
	}, true, nil
}

func upsertImportedProductTaxRate(ctx context.Context, tx pgx.Tx, companyID, productID, rate string, purchaseIncluded, salesIncluded bool) error {
	for direction, included := range map[string]bool{"PURCHASE": purchaseIncluded, "SALES": salesIncluded} {
		if _, err := tx.Exec(ctx, `INSERT INTO product_tax_profiles(company_id,product_id,direction,treatment,tax_code,rate,tax_included)
			VALUES($1,$2,$3,'STANDARD','KDV',$4,$5)
			ON CONFLICT(company_id,product_id,direction) DO UPDATE SET
				treatment=CASE WHEN product_tax_profiles.treatment='NOT_APPLICABLE' THEN 'STANDARD' ELSE product_tax_profiles.treatment END,
				rate=excluded.rate,updated_at=now(),
				version=CASE WHEN product_tax_profiles.rate IS DISTINCT FROM excluded.rate OR product_tax_profiles.treatment='NOT_APPLICABLE' THEN product_tax_profiles.version+1 ELSE product_tax_profiles.version END
			WHERE product_tax_profiles.rate IS DISTINCT FROM excluded.rate OR product_tax_profiles.treatment='NOT_APPLICABLE'`, companyID, productID, direction, rate, included); err != nil {
			return err
		}
	}
	return nil
}

func commitImportedVariantRow(ctx context.Context, tx pgx.Tx, companyID, actorID, jobID string, row catalogImportRow) error {
	values := row.Values
	productCode := strings.ToUpper(normalizedValue(values, "product_code"))
	variantCode := strings.ToUpper(normalizedValue(values, "variant_code"))
	var productID string
	var enabled bool
	if err := tx.QueryRow(ctx, `SELECT id::text,variants_enabled FROM products WHERE company_id=$1 AND code=$2 FOR SHARE`, companyID, productCode).Scan(&productID, &enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("stok kartı bulunamadı")
		}
		return err
	}
	if !enabled {
		return fmt.Errorf("varyant modu açık değil")
	}
	signature, pairs, err := resolveImportVariantValues(ctx, tx, companyID, productID, values["variant_values"])
	if err != nil {
		return err
	}
	purchase, err := importPriceOrEmpty(values["purchase_price"])
	if err != nil {
		return err
	}
	sales, err := importPriceOrEmpty(values["sales_price"])
	if err != nil {
		return err
	}
	barcode := normalizedValue(values, "barcode")
	barcodeType := normalizedBarcodeType(values["barcode_type"])

	var existingID, existingProductID, existingSignature string
	err = tx.QueryRow(ctx, `SELECT id::text,product_id::text,variant_signature FROM product_variants WHERE company_id=$1 AND product_id=$2 AND variant_code=$3 FOR UPDATE`, companyID, productID, variantCode).Scan(&existingID, &existingProductID, &existingSignature)
	if err == nil {
		if existingProductID != productID {
			return fmt.Errorf("%w: varyant kodu başka bir stok kartına ait", ErrIdentityConflict)
		}
		if existingSignature != signature {
			return fmt.Errorf("%w: varyant değerleri farklı", ErrIdentityConflict)
		}
		if barcode != "" {
			same, err := ensureImportedBarcode(ctx, tx, companyID, productID, existingID, barcode, barcodeType)
			if err != nil {
				return err
			}
			if !same {
				var hasPrimary bool
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id=$3 AND is_primary)`, companyID, productID, existingID).Scan(&hasPrimary); err != nil {
					return err
				}
				if hasPrimary {
					return fmt.Errorf("%w: varyantın birincil barkodu farklı", ErrIdentityConflict)
				}
				if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,$6,true)`, stableImportID(jobID, row.Number+100000000), companyID, productID, existingID, barcode, barcodeType); err != nil {
					return err
				}
			}
		}
		if err := upsertImportedVariantPrices(ctx, tx, companyID, existingID, purchase, sales); err != nil {
			return err
		}
		if purchase != "" || sales != "" {
			return writeImportAudit(ctx, tx, companyID, actorID, "VARIANT_PRICE_UPDATED", "product_variant", existingID, map[string]any{"source_job_id": jobID, "purchase_price": purchase, "sales_price": sales})
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	id := stableImportID(jobID, row.Number)
	if _, err := tx.Exec(ctx, `INSERT INTO product_variants(id,company_id,product_id,variant_code,variant_signature,is_active) VALUES($1,$2,$3,$4,$5,true)`, id, companyID, productID, variantCode, signature); err != nil {
		return err
	}
	for _, pair := range pairs {
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_values(company_id,variant_id,definition_id,option_id) VALUES($1,$2,$3,$4)`, companyID, id, pair.DefinitionID, pair.OptionID); err != nil {
			return err
		}
	}
	if err := upsertImportedVariantPrices(ctx, tx, companyID, id, purchase, sales); err != nil {
		return err
	}
	if barcode != "" {
		available, err := ensureImportedBarcode(ctx, tx, companyID, productID, id, barcode, barcodeType)
		if err != nil {
			return err
		}
		if available {
			return fmt.Errorf("%w: barkod zaten başka bir kayda ait", ErrIdentityConflict)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,variant_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,$6,true)`, stableImportID(jobID, row.Number+100000000), companyID, productID, id, barcode, barcodeType); err != nil {
			return err
		}
	}
	return writeImportAudit(ctx, tx, companyID, actorID, "VARIANT_IMPORTED", "product_variant", id, map[string]any{"source_job_id": jobID, "product_id": productID})
}

func commitImportedWarehouseRow(ctx context.Context, tx pgx.Tx, companyID, actorID, jobID string, row catalogImportRow) error {
	code := strings.ToUpper(normalizedValue(row.Values, "warehouse_code"))
	name := normalizedValue(row.Values, "warehouse_name")
	branchCode := strings.ToUpper(normalizedValue(row.Values, "branch_code"))
	var existingID, existingName, existingType, existingBranch string
	var active, system, transit bool
	err := tx.QueryRow(ctx, `SELECT id::text,name,warehouse_type,is_active,is_system,is_transit,COALESCE((SELECT code FROM branches b WHERE b.company_id=w.company_id AND b.id=w.branch_id),'') FROM warehouses w WHERE company_id=$1 AND code=$2 FOR UPDATE`, companyID, code).Scan(&existingID, &existingName, &existingType, &active, &system, &transit, &existingBranch)
	if err == nil {
		if existingType != "STANDARD" || !active || system || transit || (branchCode != "" && existingBranch != branchCode) {
			return fmt.Errorf("%w: depo aktif standart kapsamın dışında veya farklı", ErrIdentityConflict)
		}
		if existingName != name {
			if _, err := tx.Exec(ctx, `UPDATE warehouses SET name=$1,updated_at=now(),version=version+1 WHERE company_id=$2 AND id=$3`, name, companyID, existingID); err != nil {
				return err
			}
			return writeImportAudit(ctx, tx, companyID, actorID, "WAREHOUSE_UPDATED", "warehouse", existingID, map[string]any{"source_job_id": jobID, "name": name})
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var branchID any
	if branchCode != "" {
		var value string
		var allowed bool
		if err := tx.QueryRow(ctx, `SELECT b.id::text,(b.is_active AND (NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=b.company_id AND bs.user_id=$2) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=b.company_id AND bs.user_id=$2 AND bs.branch_id=b.id))) FROM branches b WHERE b.company_id=$1 AND b.code=$3`, companyID, actorID, branchCode).Scan(&value, &allowed); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("şube bulunamadı")
			}
			return err
		}
		if !allowed {
			return fmt.Errorf("şube için yetkiniz yok")
		}
		branchID = value
	}
	id := stableImportID(jobID, row.Number)
	_, err = tx.Exec(ctx, `INSERT INTO warehouses(id,company_id,branch_id,code,name,warehouse_type,is_transit,is_system,is_active) VALUES($1,$2,$3,$4,$5,'STANDARD',false,false,true)`, id, companyID, branchID, code, name)
	if err != nil {
		return err
	}
	return writeImportAudit(ctx, tx, companyID, actorID, "WAREHOUSE_IMPORTED", "warehouse", id, map[string]any{"source_job_id": jobID})
}

type importedPartySnapshot struct {
	ID             string
	Kind           string
	IsCustomer     bool
	IsSupplier     bool
	DisplayName    string
	LegalName      string
	TradeName      string
	FirstName      string
	LastName       string
	TaxNumber      string
	IdentityNumber string
	TaxOffice      string
	TaxOfficeID    string
	Currency       string
	IsActive       bool
}

func commitImportedPartyRow(ctx context.Context, tx pgx.Tx, companyID, actorID, jobID string, row catalogImportRow) error {
	if err := lockImportedPartyCodeSequence(ctx, tx, companyID); err != nil {
		return err
	}
	values := row.Values
	code := strings.ToUpper(strings.TrimSpace(values["code"]))
	if code == "" {
		code = strings.ToUpper(strings.TrimSpace(values["party_code"]))
	}
	kind := normalizePartyKind(values["kind"])
	roles := normalizePartyRoles(values["roles"])
	isCustomer, isSupplier := roles == "CUSTOMER" || roles == "BOTH", roles == "SUPPLIER" || roles == "BOTH"
	legalName := normalizedValue(values, "legal_name")
	tradeName := normalizedValue(values, "trade_name")
	firstName := normalizedValue(values, "first_name")
	lastName := normalizedValue(values, "last_name")
	displayName := normalizedValue(values, "name")
	if displayName == "" {
		if kind == "PERSON" {
			displayName = strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
		} else {
			displayName = legalName
		}
	}
	if legalName == "" && kind == "ORGANIZATION" {
		legalName = displayName
	}
	var existing importedPartySnapshot
	err := tx.QueryRow(ctx, `SELECT id::text,kind::text,is_customer,is_supplier,display_name,COALESCE(legal_name,''),COALESCE(trade_name,''),COALESCE(first_name,''),COALESCE(last_name,''),COALESCE(tax_number,''),COALESCE(identity_number,''),COALESCE(tax_office,''),COALESCE(tax_office_id::text,''),btrim(default_currency::text),is_active
		FROM parties WHERE company_id=$1 AND code=$2 FOR UPDATE`, companyID, code).Scan(
		&existing.ID, &existing.Kind, &existing.IsCustomer, &existing.IsSupplier, &existing.DisplayName,
		&existing.LegalName, &existing.TradeName, &existing.FirstName, &existing.LastName,
		&existing.TaxNumber, &existing.IdentityNumber, &existing.TaxOffice, &existing.TaxOfficeID, &existing.Currency, &existing.IsActive,
	)
	if err == nil {
		if existing.Kind != kind {
			return fmt.Errorf("%w: cari türü değiştirilemez", ErrIdentityConflict)
		}
		legalName = importedPartyValue(values, "legal_name", existing.LegalName)
		tradeName = importedPartyValue(values, "trade_name", existing.TradeName)
		firstName = importedPartyValue(values, "first_name", existing.FirstName)
		lastName = importedPartyValue(values, "last_name", existing.LastName)
		taxNumber := importedPartyValue(values, "tax_number", existing.TaxNumber)
		identityNumber := importedPartyValue(values, "identity_number", existing.IdentityNumber)
		taxOffice := importedPartyValue(values, "tax_office", existing.TaxOffice)
		currency := importedPartyValue(values, "currency", existing.Currency)
		active := existing.IsActive
		if valueProvided(values, "is_active") {
			active, err = parseImportBool(values["is_active"])
			if err != nil {
				return err
			}
		}
		if kind == "PERSON" {
			displayName = strings.TrimSpace(strings.Join([]string{firstName, lastName}, " "))
		} else if tradeName != "" {
			displayName = tradeName
		} else {
			displayName = legalName
		}
		if err := ensureImportedPartyIdentityAvailable(ctx, tx, companyID, existing.ID, taxNumber, identityNumber); err != nil {
			return err
		}
		isCustomer, isSupplier := roles == "CUSTOMER" || roles == "BOTH", roles == "SUPPLIER" || roles == "BOTH"
		taxOfficeChanged := existing.TaxOffice != taxOffice
		scalarChanged := existing.IsCustomer != isCustomer || existing.IsSupplier != isSupplier || existing.DisplayName != displayName || existing.LegalName != legalName || existing.TradeName != tradeName || existing.FirstName != firstName || existing.LastName != lastName || existing.TaxNumber != taxNumber || existing.IdentityNumber != identityNumber || taxOfficeChanged || existing.Currency != currency || existing.IsActive != active
		if scalarChanged {
			if _, err := tx.Exec(ctx, `UPDATE parties SET is_customer=$1,is_supplier=$2,display_name=$3,legal_name=NULLIF($4,''),trade_name=NULLIF($5,''),first_name=NULLIF($6,''),last_name=NULLIF($7,''),tax_number=NULLIF($8,''),identity_number=NULLIF($9,''),tax_office=NULLIF($10,''),tax_office_id=CASE WHEN $11 THEN NULL ELSE tax_office_id END,default_currency=$12,is_active=$13,deactivated_at=CASE WHEN $13 THEN NULL ELSE COALESCE(deactivated_at,now()) END,updated_at=now(),version=version+1 WHERE company_id=$14 AND id=$15`, isCustomer, isSupplier, displayName, legalName, tradeName, firstName, lastName, taxNumber, identityNumber, taxOffice, taxOfficeChanged, currency, active, companyID, existing.ID); err != nil {
				return err
			}
		}
		addressChanged, err := updateImportedPartyAddress(ctx, tx, companyID, existing.ID, jobID, row.Number, values)
		if err != nil {
			return err
		}
		contactChanged, err := updateImportedPartyContact(ctx, tx, companyID, existing.ID, jobID, row.Number, displayName, values)
		if err != nil {
			return err
		}
		if scalarChanged || addressChanged || contactChanged {
			changes := map[string]any{"source_job_id": jobID}
			if scalarChanged {
				changes["master_fields"] = true
			}
			if addressChanged {
				changes["address"] = true
			}
			if contactChanged {
				changes["contact"] = true
			}
			return writeImportAudit(ctx, tx, companyID, actorID, "PARTY_UPDATED", "party", existing.ID, changes)
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	currency := strings.ToUpper(strings.TrimSpace(values["currency"]))
	if currency == "" {
		if err := tx.QueryRow(ctx, `SELECT base_currency::text FROM companies WHERE id=$1`, companyID).Scan(&currency); err != nil {
			return err
		}
	}
	active := true
	if valueProvided(values, "is_active") {
		active, err = parseImportBool(values["is_active"])
		if err != nil {
			return err
		}
	}
	var duplicateIdentity bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND (($2<>'' AND tax_number=$2) OR ($3<>'' AND identity_number=$3)))`, companyID, normalizedValue(values, "tax_number"), normalizedValue(values, "identity_number")).Scan(&duplicateIdentity); err != nil {
		return err
	}
	if duplicateIdentity {
		var policy string
		if err := tx.QueryRow(ctx, `SELECT duplicate_party_tax_number_policy::text FROM companies WHERE id=$1`, companyID).Scan(&policy); err != nil {
			return err
		}
		if policy == "BLOCK" {
			return fmt.Errorf("vergi veya T.C. kimlik numarası zaten kullanılıyor")
		}
	}
	id := stableImportID(jobID, row.Number)
	if _, err := tx.Exec(ctx, `INSERT INTO parties(id,company_id,code,kind,is_customer,is_supplier,display_name,legal_name,trade_name,first_name,last_name,tax_number,identity_number,tax_office,default_currency,is_active,deactivated_at) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),NULLIF($13,''),NULLIF($14,''),$15,$16,CASE WHEN $16 THEN NULL ELSE now() END)`, id, companyID, code, kind, isCustomer, isSupplier, displayName, legalName, tradeName, firstName, lastName, normalizedValue(values, "tax_number"), normalizedValue(values, "identity_number"), normalizedValue(values, "tax_office"), currency, active); err != nil {
		return err
	}
	if partyAddressProvided(values) {
		if _, err := updateImportedPartyAddress(ctx, tx, companyID, id, jobID, row.Number, values); err != nil {
			return err
		}
	}
	if _, err := updateImportedPartyContact(ctx, tx, companyID, id, jobID, row.Number, displayName, values); err != nil {
		return err
	}
	return writeImportAudit(ctx, tx, companyID, actorID, "PARTY_IMPORTED", "party", id, map[string]any{"source_job_id": jobID})
}

func lockImportedProductCodeSequence(ctx context.Context, tx pgx.Tx, companyID string) error {
	var nextNumber int64
	return tx.QueryRow(ctx, `INSERT INTO company_product_sequences(company_id,prefix,digits,next_number) VALUES($1,'STK',6,1) ON CONFLICT(company_id) DO UPDATE SET next_number=company_product_sequences.next_number,updated_at=now() RETURNING next_number`, companyID).Scan(&nextNumber)
}

func lockImportedPartyCodeSequence(ctx context.Context, tx pgx.Tx, companyID string) error {
	var lockedCompanyID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM companies WHERE id=$1 FOR UPDATE`, companyID).Scan(&lockedCompanyID); err != nil {
		return err
	}
	var nextNumber int64
	return tx.QueryRow(ctx, `INSERT INTO company_party_sequences(company_id,next_number) VALUES($1,1) ON CONFLICT(company_id) DO UPDATE SET next_number=company_party_sequences.next_number RETURNING next_number`, companyID).Scan(&nextNumber)
}

func importedPartyValue(values map[string]string, field, current string) string {
	if valueProvided(values, field) {
		return strings.TrimSpace(values[field])
	}
	return current
}

func ensureImportedPartyIdentityAvailable(ctx context.Context, tx pgx.Tx, companyID, excludeID, taxNumber, identityNumber string) error {
	if taxNumber == "" && identityNumber == "" {
		return nil
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM parties WHERE company_id=$1 AND id<>COALESCE(NULLIF($2,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND (($3<>'' AND tax_number=$3) OR ($4<>'' AND identity_number=$4)))`, companyID, excludeID, taxNumber, identityNumber).Scan(&duplicate); err != nil {
		return err
	}
	if !duplicate {
		return nil
	}
	var policy string
	if err := tx.QueryRow(ctx, `SELECT duplicate_party_tax_number_policy::text FROM companies WHERE id=$1`, companyID).Scan(&policy); err != nil {
		return err
	}
	if policy == "BLOCK" {
		return fmt.Errorf("%w: cari vergi veya T.C. kimlik numarası zaten kullanılıyor", ErrIdentityConflict)
	}
	return nil
}

func updateImportedPartyAddress(ctx context.Context, tx pgx.Tx, companyID, partyID, jobID string, rowNumber int, values map[string]string) (bool, error) {
	addressLine := normalizedValue(values, "address_line")
	province := normalizedValue(values, "province_name")
	district := normalizedValue(values, "district_name")
	neighborhood := normalizedValue(values, "neighborhood_name")
	if addressLine == "" && province == "" && district == "" && neighborhood == "" {
		return false, nil
	}
	if province == "" {
		return false, fmt.Errorf("adres bilgisi gönderildiğinde il zorunludur")
	}
	var existingID, existingAddress, existingDistrict, existingCity, existingNeighborhood string
	err := tx.QueryRow(ctx, `SELECT id::text,COALESCE(address_line,''),COALESCE(district,''),city,COALESCE(neighborhood,'') FROM party_addresses WHERE company_id=$1 AND party_id=$2 ORDER BY is_default DESC,created_at,id LIMIT 1 FOR UPDATE`, companyID, partyID).Scan(&existingID, &existingAddress, &existingDistrict, &existingCity, &existingNeighborhood)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	provinceID, districtID, neighborhoodID, err := importedPartyLocationReferences(ctx, tx, province, district, neighborhood)
	if err != nil {
		return false, err
	}
	if err == nil && existingAddress == addressLine && existingDistrict == district && existingCity == province && existingNeighborhood == neighborhood {
		return false, nil
	}
	if existingID == "" {
		_, err = tx.Exec(ctx, `INSERT INTO party_addresses(id,company_id,party_id,address_line,district,city,neighborhood,is_default,province_id,district_id,neighborhood_id) VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),$6,NULLIF($7,''),true,$8,$9,$10)`, stableImportID(jobID, rowNumber+200000000), companyID, partyID, addressLine, district, province, neighborhood, provinceID, districtID, neighborhoodID)
		return err == nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE party_addresses SET address_line=NULLIF($1,''),district=NULLIF($2,''),city=$3,neighborhood=NULLIF($4,''),is_default=true,province_id=$5,district_id=$6,neighborhood_id=$7,updated_at=now(),version=version+1 WHERE company_id=$8 AND id=$9`, addressLine, district, province, neighborhood, provinceID, districtID, neighborhoodID, companyID, existingID)
	return err == nil, err
}

func importedPartyLocationReferences(ctx context.Context, tx pgx.Tx, province, district, neighborhood string) (any, any, any, error) {
	var provinceID int64
	if province == "" {
		return nil, nil, nil, nil
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM turkish_provinces WHERE lower(name)=lower($1) ORDER BY id LIMIT 1`, province).Scan(&provinceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	var districtID int64
	if district != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM turkish_districts WHERE province_id=$1 AND lower(name)=lower($2) ORDER BY id LIMIT 1`, provinceID, district).Scan(&districtID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, err
		}
	}
	var neighborhoodID int64
	if districtID != 0 && neighborhood != "" {
		if err := tx.QueryRow(ctx, `SELECT id FROM turkish_neighborhoods WHERE district_id=$1 AND lower(name)=lower($2) ORDER BY id LIMIT 1`, districtID, neighborhood).Scan(&neighborhoodID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, err
		}
	}
	var districtValue, neighborhoodValue any
	if districtID != 0 {
		districtValue = districtID
	}
	if neighborhoodID != 0 {
		neighborhoodValue = neighborhoodID
	}
	return provinceID, districtValue, neighborhoodValue, nil
}

func updateImportedPartyContact(ctx context.Context, tx pgx.Tx, companyID, partyID, jobID string, rowNumber int, displayName string, values map[string]string) (bool, error) {
	if !valueProvided(values, "phone") && !valueProvided(values, "email") {
		return false, nil
	}
	phone := normalizedValue(values, "phone")
	email := normalizedValue(values, "email")
	var existingID, existingName, existingEmail, existingPhone string
	err := tx.QueryRow(ctx, `SELECT id::text,full_name,COALESCE(email,''),COALESCE(phone,'') FROM party_contacts WHERE company_id=$1 AND party_id=$2 ORDER BY is_primary DESC,created_at,id LIMIT 1 FOR UPDATE`, companyID, partyID).Scan(&existingID, &existingName, &existingEmail, &existingPhone)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		if phone == "" && email == "" {
			return false, nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO party_contacts(id,company_id,party_id,full_name,email,phone,is_primary) VALUES($1,$2,$3,$4,NULLIF($5,''),NULLIF($6,''),true)`, stableImportID(jobID, rowNumber+300000000), companyID, partyID, displayName, email, phone)
		return err == nil, err
	}
	if !valueProvided(values, "phone") {
		phone = existingPhone
	}
	if !valueProvided(values, "email") {
		email = existingEmail
	}
	if existingName == displayName && existingEmail == email && existingPhone == phone {
		return false, nil
	}
	_, err = tx.Exec(ctx, `UPDATE party_contacts SET full_name=$1,email=NULLIF($2,''),phone=NULLIF($3,''),updated_at=now(),version=version+1 WHERE company_id=$4 AND id=$5`, displayName, email, phone, companyID, existingID)
	return err == nil, err
}

func importPriceOrEmpty(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	value, err := parseImportPrice(raw)
	if err != nil {
		return "", fmt.Errorf("fiyat geçersiz")
	}
	return value, nil
}

func normalizedBarcodeType(raw string) string {
	value := strings.ToUpper(strings.TrimSpace(raw))
	if value == "" {
		return "EAN"
	}
	return value
}

func sameImportDecimal(left, right string) bool {
	return canonicalImportDecimal(left) == canonicalImportDecimal(right)
}

func canonicalImportDecimal(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	sign := ""
	if value[0] == '+' || value[0] == '-' {
		sign, value = value[:1], value[1:]
	}
	value = strings.ReplaceAll(value, ",", ".")
	parts := strings.SplitN(value, ".", 2)
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = strings.TrimRight(parts[1], "0")
	}
	if integer == "0" && fraction == "" {
		sign = ""
	}
	if fraction == "" {
		return sign + integer
	}
	return sign + integer + "." + fraction
}

// ensureImportedBarcode returns true when a row already owns the exact
// primary barcode. Any other owner, type or primary flag is a conflict.
func ensureImportedBarcode(ctx context.Context, tx pgx.Tx, companyID, productID, variantID, barcode, barcodeType string) (bool, error) {
	var existingProduct, existingVariant, existingType string
	var primary bool
	err := tx.QueryRow(ctx, `SELECT product_id::text,COALESCE(variant_id::text,''),barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND barcode=$2 FOR UPDATE`, companyID, barcode).Scan(&existingProduct, &existingVariant, &existingType, &primary)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existingProduct != productID || existingVariant != variantID || existingType != barcodeType || !primary {
		return false, fmt.Errorf("%w: barkod başka bir kayda ait veya aynı birincil barkod değil", ErrIdentityConflict)
	}
	return true, nil
}

// syncImportedProductBarcode keeps the product-level barcode identity
// deterministic while allowing a variantless product to move its primary
// barcode. The old primary row is demoted instead of deleted, so historical
// and secondary barcode references remain available for lookup and audit.
func syncImportedProductBarcode(ctx context.Context, tx pgx.Tx, companyID, productID, jobID string, rowNumber int, barcode, barcodeType string, barcodeTypeProvided bool) (bool, error) {
	var existingID, existingProduct, existingVariant, existingType string
	var primary bool
	err := tx.QueryRow(ctx, `SELECT id::text,product_id::text,COALESCE(variant_id::text,''),barcode_type,is_primary FROM product_barcodes WHERE company_id=$1 AND barcode=$2 FOR UPDATE`, companyID, barcode).Scan(&existingID, &existingProduct, &existingVariant, &existingType, &primary)
	if err == nil {
		if existingProduct != productID || existingVariant != "" || !primary {
			return false, fmt.Errorf("%w: barkod başka bir kayda ait veya stok kartının birincil barkodu değil", ErrIdentityConflict)
		}
		if !barcodeTypeProvided || existingType == barcodeType {
			return false, nil
		}
		if _, err := tx.Exec(ctx, `UPDATE product_barcodes SET barcode_type=$1 WHERE company_id=$2 AND id=$3`, barcodeType, companyID, existingID); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}

	var previousID, previousType string
	err = tx.QueryRow(ctx, `SELECT id::text,barcode_type FROM product_barcodes WHERE company_id=$1 AND product_id=$2 AND variant_id IS NULL AND is_primary FOR UPDATE`, companyID, productID).Scan(&previousID, &previousType)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	if !barcodeTypeProvided {
		if previousID != "" {
			barcodeType = previousType
		} else {
			barcodeType = normalizedBarcodeType("")
		}
	}
	if previousID != "" {
		if _, err := tx.Exec(ctx, `UPDATE product_barcodes SET is_primary=false WHERE company_id=$1 AND id=$2`, companyID, previousID); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO product_barcodes(id,company_id,product_id,barcode,barcode_type,is_primary) VALUES($1,$2,$3,$4,$5,true)`, stableImportID(jobID, rowNumber+100000000), companyID, productID, barcode, barcodeType); err != nil {
		return false, err
	}
	return true, nil
}

func upsertImportedVariantPrices(ctx context.Context, tx pgx.Tx, companyID, variantID, purchase, sales string) error {
	if purchase != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_price_overrides(company_id,variant_id,direction,unit_price) VALUES($1,$2,'PURCHASE',$3) ON CONFLICT (company_id,variant_id,direction) DO UPDATE SET unit_price=excluded.unit_price,updated_at=now()`, companyID, variantID, purchase); err != nil {
			return err
		}
	}
	if sales != "" {
		if _, err := tx.Exec(ctx, `INSERT INTO product_variant_price_overrides(company_id,variant_id,direction,unit_price) VALUES($1,$2,'SALES',$3) ON CONFLICT (company_id,variant_id,direction) DO UPDATE SET unit_price=excluded.unit_price,updated_at=now()`, companyID, variantID, sales); err != nil {
			return err
		}
	}
	return nil
}

func writeImportAudit(ctx context.Context, tx pgx.Tx, companyID, actorID, eventType, entityType, entityID string, details map[string]any) error {
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO security_audit_events(id,company_id,actor_user_id,event_type,entity_type,entity_id,details,trace_id,source_ip,user_agent) VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,NULLIF($6,'')::uuid,$7,'','','')`, uuid.NewString(), companyID, actorID, eventType, entityType, entityID, encoded)
	return err
}
