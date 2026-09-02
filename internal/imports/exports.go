package imports

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/alpyxn/varyaone/internal/dataexchange"
	"github.com/alpyxn/varyaone/internal/storage"
	"github.com/google/uuid"
)

// CreateExport routes every initial entity through the same immutable artifact
// writer. Stock count keeps its domain-specific snapshot/export rules.
func (s *Service) CreateExport(ctx context.Context, companyID, actorID, entity, targetID, format string) (ExportJob, error) {
	entity = strings.ToUpper(strings.TrimSpace(entity))
	spec, ok := specFor(entity)
	if !ok || !spec.Exportable {
		return ExportJob{}, fmt.Errorf("bu aktarım türü dışa aktarılamaz")
	}
	if entity == string(dataexchange.EntityStockCount) {
		return s.CreateStockCountExport(ctx, companyID, actorID, targetID, format)
	}
	if s == nil || s.pool == nil || s.store == nil {
		return ExportJob{}, fmt.Errorf("dışa aktarım hizmeti yapılandırılmamış")
	}
	format = strings.ToUpper(strings.TrimSpace(format))
	if format == "" {
		format = "XLSX"
	}
	if format != "CSV" && format != "XLSX" {
		return ExportJob{}, fmt.Errorf("dosya biçimi CSV veya XLSX olmalıdır")
	}
	table, err := s.catalogExportTable(ctx, companyID, actorID, entity, targetID)
	if err != nil {
		return ExportJob{}, err
	}
	var payload bytes.Buffer
	if format == "CSV" {
		err = dataexchange.WriteCSV(&payload, table)
	} else {
		err = dataexchange.WriteXLSX(&payload, table)
	}
	if err != nil {
		return ExportJob{}, err
	}
	id := uuid.NewString()
	ext := strings.ToLower(format)
	contentType := "text/csv"
	if format == "XLSX" {
		contentType = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	}
	filename := fmt.Sprintf("%s.%s", exportFilenameStem(entity), ext)
	key := fmt.Sprintf("exports/%s/%s/%s", companyID, id, filename)
	object, err := s.store.Put(ctx, key, bytes.NewReader(payload.Bytes()), storage.PutOptions{ContentType: contentType})
	if err != nil {
		return ExportJob{}, err
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO export_jobs(id,company_id,entity_type,format,status,artifact_storage_key,filename,content_type,row_count,requested_by,completed_at) VALUES($1,$2,$3,$4,'COMPLETED',$5,$6,$7,$8,$9,now())`, id, companyID, entity, format, key, filename, contentType, len(table.Rows), actorID); err != nil {
		_ = s.store.Delete(context.WithoutCancel(ctx), key)
		return ExportJob{}, err
	}
	if _, err = s.pool.Exec(ctx, `INSERT INTO export_artifacts(company_id,export_job_id,storage_key,sha256,size_bytes) VALUES($1,$2,$3,$4,$5)`, companyID, id, key, object.SHA256, object.Size); err != nil {
		return ExportJob{}, err
	}
	return s.GetExport(ctx, companyID, id)
}

func exportFilenameStem(entity string) string {
	switch strings.ToUpper(strings.TrimSpace(entity)) {
	case string(dataexchange.EntityProduct):
		return "urunler"
	case string(dataexchange.EntityVariant):
		return "varyantlar"
	case string(dataexchange.EntityBarcode):
		return "barkodlar"
	case string(dataexchange.EntityWarehouse):
		return "depolar"
	case string(dataexchange.EntityParty):
		return "cariler"
	case string(dataexchange.EntityPriceList):
		return "fiyat-listeleri"
	case string(dataexchange.EntityOpeningStock):
		return "acilis-stoku"
	case string(dataexchange.EntityStockCount):
		return "stok-sayimi"
	default:
		return "aktarim"
	}
}

func (s *Service) catalogExportTable(ctx context.Context, companyID, actorID, entity, targetID string) (dataexchange.Table, error) {
	var headers []string
	var records [][]string
	var decimalColumns []int
	appendRows := func(query string, args ...any) error {
		rows, err := s.pool.Query(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			values := make([]string, len(headers))
			pointers := make([]any, len(values))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				return err
			}
			records = append(records, values)
		}
		return rows.Err()
	}
	switch entity {
	case string(dataexchange.EntityProduct):
		headers = []string{"Stok Kodu", "Stok Adı", "Birim", "Açıklama", "Aktif", "Alış Fiyatı", "Satış Fiyatı", "Birincil Barkod", "Barkod Tipi", "KDV Oranı (%)", "Açılış Deposu", "Açılış Miktarı"}
		decimalColumns = []int{5, 6, 9, 11}
		if err := appendRows(`SELECT p.code,p.name,COALESCE(u.unit_code,''),COALESCE(p.description,''),CASE WHEN p.is_active THEN 'Evet' ELSE 'Hayır' END,p.purchase_price::text,p.sales_price::text,COALESCE(b.barcode,''),COALESCE(b.barcode_type,''),COALESCE(p.purchase_tax_rate::text,'0'),''::text,''::text
			FROM products p
			LEFT JOIN LATERAL (SELECT unit_code FROM product_units WHERE company_id=p.company_id AND product_id=p.id AND is_base ORDER BY unit_code LIMIT 1) u ON true
			LEFT JOIN LATERAL (SELECT barcode,barcode_type FROM product_barcodes WHERE company_id=p.company_id AND product_id=p.id AND variant_id IS NULL AND is_primary ORDER BY id LIMIT 1) b ON true
			WHERE p.company_id=$1 ORDER BY p.code`, companyID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityVariant):
		headers = []string{"Stok Kodu", "Varyant Kodu", "Varyant Değerleri", "Alış Fiyatı", "Satış Fiyatı", "Birincil Barkod", "Barkod Tipi"}
		decimalColumns = []int{3, 4}
		if err := appendRows(`SELECT p.code,v.variant_code,
			COALESCE((SELECT jsonb_object_agg(d.code,o.code ORDER BY d.code)
				FROM product_variant_values vv
				JOIN variant_definitions d ON d.company_id=vv.company_id AND d.id=vv.definition_id
				JOIN variant_definition_options o ON o.company_id=vv.company_id AND o.definition_id=vv.definition_id AND o.id=vv.option_id
				WHERE vv.company_id=v.company_id AND vv.variant_id=v.id),'{}'::jsonb)::text,
			COALESCE((SELECT unit_price::text FROM product_variant_price_overrides po WHERE po.company_id=v.company_id AND po.variant_id=v.id AND po.direction='PURCHASE'),'') ,
			COALESCE((SELECT unit_price::text FROM product_variant_price_overrides po WHERE po.company_id=v.company_id AND po.variant_id=v.id AND po.direction='SALES'),'') ,
			COALESCE(b.barcode,''),COALESCE(b.barcode_type,'')
			FROM product_variants v JOIN products p ON p.company_id=v.company_id AND p.id=v.product_id
			LEFT JOIN LATERAL (SELECT barcode,barcode_type FROM product_barcodes WHERE company_id=v.company_id AND variant_id=v.id AND is_primary ORDER BY id LIMIT 1) b ON true
			WHERE v.company_id=$1 AND p.variants_enabled ORDER BY p.code,v.variant_code`, companyID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityBarcode):
		headers = []string{"Barkod", "Sahip", "Stok Kodu", "Varyant Kodu", "Barkod Tipi", "Birincil"}
		if err := appendRows(`SELECT b.barcode,CASE WHEN b.variant_id IS NULL THEN 'Ürün:'||p.code ELSE 'Varyant:'||p.code||':'||v.variant_code END,p.code,COALESCE(v.variant_code,''),b.barcode_type,CASE WHEN b.is_primary THEN 'Evet' ELSE 'Hayır' END
			FROM product_barcodes b JOIN products p ON p.company_id=b.company_id AND p.id=b.product_id LEFT JOIN product_variants v ON v.company_id=b.company_id AND v.id=b.variant_id
			WHERE b.company_id=$1 AND (b.variant_id IS NULL OR p.variants_enabled) ORDER BY p.code,COALESCE(v.variant_code,''),b.barcode`, companyID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityWarehouse):
		headers = []string{"Depo Kodu", "Depo Adı", "Şube Kodu"}
		if err := appendRows(`SELECT w.code,w.name,COALESCE(b.code,'')
			FROM warehouses w LEFT JOIN branches b ON b.company_id=w.company_id AND b.id=w.branch_id
			WHERE w.company_id=$1 AND w.is_active AND w.warehouse_type='STANDARD' AND NOT w.is_system AND NOT w.is_transit
			AND (w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=w.company_id AND bs.user_id=$2) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=w.company_id AND bs.user_id=$2 AND bs.branch_id=w.branch_id))
			ORDER BY w.code`, companyID, actorID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityParty):
		headers = []string{"Cari Kodu", "Cari Türü", "Roller", "Cari Adı", "Resmî Unvan", "Ticari Ad", "Ad", "Soyad", "Vergi Numarası", "T.C. Kimlik Numarası", "Vergi Dairesi", "Para Birimi", "Telefon", "E-posta", "Adres", "İl", "İlçe", "Mahalle", "Aktif"}
		if err := appendRows(`SELECT p.code,CASE p.kind::text WHEN 'PERSON' THEN 'Kişi' WHEN 'ORGANIZATION' THEN 'Kurum' ELSE p.kind::text END,CASE WHEN p.is_customer AND p.is_supplier THEN '["Müşteri","Tedarikçi"]' WHEN p.is_customer THEN '["Müşteri"]' ELSE '["Tedarikçi"]' END,p.display_name,COALESCE(p.legal_name,''),COALESCE(p.trade_name,''),COALESCE(p.first_name,''),COALESCE(p.last_name,''),COALESCE(p.tax_number,''),COALESCE(p.identity_number,''),COALESCE(p.tax_office,''),p.default_currency,COALESCE(c.phone,''),COALESCE(c.email,''),COALESCE(a.address_line,''),COALESCE(tp.name,a.city,''),COALESCE(td.name,a.district,''),COALESCE(tn.name,a.neighborhood,''),CASE WHEN p.is_active THEN 'Evet' ELSE 'Hayır' END
			FROM parties p
			LEFT JOIN LATERAL (SELECT phone,email FROM party_contacts WHERE company_id=p.company_id AND party_id=p.id ORDER BY is_primary DESC,created_at,id LIMIT 1) c ON true
			LEFT JOIN LATERAL (SELECT a.* FROM party_addresses a WHERE a.company_id=p.company_id AND a.party_id=p.id ORDER BY a.is_default DESC,a.created_at,a.id LIMIT 1) a ON true
			LEFT JOIN turkish_provinces tp ON tp.id=a.province_id
			LEFT JOIN turkish_districts td ON td.province_id=a.province_id AND td.id=a.district_id
			LEFT JOIN turkish_neighborhoods tn ON tn.district_id=a.district_id AND tn.id=a.neighborhood_id
			WHERE p.company_id=$1 ORDER BY p.code`, companyID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityPriceList):
		headers = []string{"Fiyat Listesi Kodu", "Ürün Kodu", "Varyant Kodu", "Fiyat"}
		decimalColumns = []int{3}
		if err := appendRows(`SELECT l.code,p.code,COALESCE(v.variant_code,''),e.unit_price::text FROM price_list_entries e JOIN price_lists l ON l.company_id=e.company_id AND l.id=e.price_list_id JOIN products p ON p.company_id=e.company_id AND p.id=e.item_id LEFT JOIN product_variants v ON v.company_id=e.company_id AND v.id=e.variant_id WHERE e.company_id=$1 AND (e.valid_to IS NULL OR e.valid_to >= CURRENT_DATE) ORDER BY l.code,p.code,v.variant_code,e.valid_from DESC`, companyID); err != nil {
			return dataexchange.Table{}, err
		}
	case string(dataexchange.EntityOpeningStock):
		headers = []string{"Depo Kodu", "Ürün Kodu", "Varyant Kodu", "Miktar"}
		decimalColumns = []int{3}
		if err := appendRows(`SELECT w.code,p.code,COALESCE(v.variant_code,''),sp.physical_quantity::text FROM stock_positions sp JOIN warehouses w ON w.company_id=sp.company_id AND w.id=sp.warehouse_id JOIN products p ON p.company_id=sp.company_id AND p.id=sp.product_id LEFT JOIN product_variants v ON v.company_id=sp.company_id AND v.id=sp.variant_id WHERE sp.company_id=$1 AND w.is_active AND w.warehouse_type='STANDARD' AND NOT w.is_system AND NOT w.is_transit AND (w.branch_id IS NULL OR NOT EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=w.company_id AND bs.user_id=$2) OR EXISTS(SELECT 1 FROM membership_branch_scopes bs WHERE bs.company_id=w.company_id AND bs.user_id=$2 AND bs.branch_id=w.branch_id)) AND sp.lot_id IS NULL AND sp.serial_id IS NULL AND sp.physical_quantity <> 0 ORDER BY w.code,p.code,v.variant_code`, companyID, actorID); err != nil {
			return dataexchange.Table{}, err
		}
	default:
		return dataexchange.Table{}, fmt.Errorf("desteklenmeyen dışa aktarım türü")
	}
	for _, row := range records {
		for _, column := range decimalColumns {
			if column >= 0 && column < len(row) {
				row[column] = trimExportDecimal(row[column])
			}
		}
	}
	return dataexchange.NewTable(headers, records)
}
