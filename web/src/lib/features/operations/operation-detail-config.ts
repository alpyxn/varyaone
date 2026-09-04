// Detay sayfalarının alan/başlık tanımları. Bileşenden ayrı tutuluyor ki
// birim testleri UUID sızıntısına karşı bu tabloyu doğrudan tarayabilsin.
import { formatMoney } from '$lib/design/formatters';

export type OperationDetailKind =
  | 'party-movement'
  | 'collection'
  | 'payment'
  | 'stock-movement'
  | 'warehouse'
  | 'transfer'
  | 'count'
  | 'lot'
  | 'serial'
  | 'account'
  | 'account-movement'
  | 'finance-transfer'
  | 'document';

export type RecordValue = Record<string, unknown>;
export type Field = {
  key: string | string[];
  label: string;
  // 'ref' alanları bir kaydın kimliğini (UUID) taşır ama onu asla metin
  // olarak basmaz: yalnızca refText ile adlandırılmış bir bağlantı olarak
  // görünür, bağlantı kurulamıyorsa hiç görünmez.
  kind?: 'text' | 'date' | 'datetime' | 'money' | 'quantity' | 'decimal' | 'ref';
  refText?: string;
  currencyKey?: string | string[];
  linkPath?: string;
  linkKey?: string | string[];
  linkQueryKey?: string | string[];
  appendInactiveLabel?: boolean;
  hideOnPrint?: boolean;
};
export type TableColumn = Field;
export type Table = {
  key: string;
  title: string;
  columns: TableColumn[];
  hideOnPrint?: boolean;
};
export type Config = {
  title: string;
  listPath: string;
  endpoint: string;
  numberKeys: string[];
  // numberKeys hiçbir zaman 'id' içermez. Numarası olmayan kayıtlarda
  // başlık UUID'ye düşmek yerine buradan tarif edilir.
  numberFallback?: (item: RecordValue) => string;
  statusKeys: string[];
  subjectKeys: string[];
  metaKeys: string[];
  fields: Field[];
  tables?: Table[];
  print?: boolean;
};

export function firstValue(item: RecordValue | undefined, keys: string | string[]) {
  if (!item) return undefined;
  const candidates = Array.isArray(keys) ? keys : [keys];
  for (const key of candidates) {
    const value = key.split('.').reduce<unknown>((current, part) => {
      if (!current || typeof current !== 'object') return undefined;
      return (current as RecordValue)[part];
    }, item);
    if (value !== undefined && value !== null && value !== '') return value;
  }
  return undefined;
}

export function textValue(value: unknown) {
  if (value === undefined || value === null || value === '') return '—';
  if (typeof value === 'boolean') return value ? 'Evet' : 'Hayır';
  if (typeof value === 'object') return JSON.stringify(value);
  return String(value);
}

export function hasValue(value: unknown) {
  return value !== undefined && value !== null && value !== '';
}

export function configFor(value: OperationDetailKind): Config {
  const commonPosted: Field[] = [
    { key: ['posted_at', 'created_at'], label: 'Oluşturma zamanı', kind: 'datetime' },
    { key: ['actor_name', 'created_by_name'], label: 'Kaydı oluşturan' }
  ];
  const textFallback = (item: RecordValue, keys: string | string[], fallback: string) => {
    const found = firstValue(item, keys);
    return hasValue(found) ? String(found) : fallback;
  };
  const moneyFallback = (item: RecordValue, keys: string | string[]) => {
    const amount = firstValue(item, keys);
    if (!hasValue(amount)) return '';
    const currency = textValue(firstValue(item, 'currency'));
    return formatMoney(String(amount), currency === '—' ? 'TRY' : currency);
  };
  switch (value) {
    case 'party-movement':
      return {
        title: 'Cari Hareket',
        listPath: '/cari/hareketler',
        endpoint: '/party-movements',
        numberKeys: ['document_no', 'business_number'],
        numberFallback: (item) => {
          // Cari hareketlerde tek taraf doludur; sıfır olan tarafı atla.
          const debit = Number(firstValue(item, 'debit') ?? 0);
          const amount = moneyFallback(item, debit > 0 ? 'debit' : 'credit');
          return amount ? `Cari Hareket · ${amount}` : 'Cari Hareket';
        },
        statusKeys: ['status', 'state'],
        subjectKeys: ['party_name', 'party.display_name'],
        metaKeys: ['currency', 'document_date', 'transaction_date'],
        fields: [
          {
            key: ['party_name', 'party.display_name'],
            label: 'Cari',
            linkPath: '/cari/kartlar/{id}',
            linkKey: ['party_id', 'party.id']
          },
          { key: ['party_code', 'party.code'], label: 'Cari Kodu' },
          { key: ['entry_type', 'movement_type'], label: 'Hareket türü' },
          { key: 'debit', label: 'Borç', kind: 'money' },
          { key: 'credit', label: 'Alacak', kind: 'money' },
          { key: 'currency', label: 'Para birimi' },
          { key: 'exchange_rate', label: 'Kur', kind: 'decimal' },
          {
            key: ['base_amount', 'try_amount'],
            label: 'TRY karşılığı',
            kind: 'money',
            currencyKey: 'base_currency'
          },
          { key: ['document_date', 'transaction_date'], label: 'İşlem tarihi', kind: 'date' },
          { key: 'due_date', label: 'Vade tarihi', kind: 'date' },
          { key: ['reference_no', 'reference'], label: 'Referans no' },
          { key: 'description', label: 'Açıklama' },
          { key: ['source_label', 'source_type', 'source.type'], label: 'Kaynak' },
          {
            key: 'reversal_of_id',
            label: 'Ters çevrilen kayıt',
            kind: 'ref',
            refText: 'Hareketi aç',
            linkPath: '/cari/hareketler/{id}'
          },
          ...commonPosted
        ]
      };
    case 'collection':
    case 'payment':
      return {
        title: value === 'collection' ? 'Tahsilat' : 'Ödeme',
        listPath: value === 'collection' ? '/cari/tahsilatlar' : '/cari/odemeler',
        endpoint: '/finance/payments',
        numberKeys: ['document_no', 'business_number'],
        numberFallback: (item) => {
          const amount = moneyFallback(item, 'amount');
          const title = value === 'collection' ? 'Tahsilat' : 'Ödeme';
          return amount ? `${title} · ${amount}` : title;
        },
        statusKeys: ['status', 'state'],
        subjectKeys: ['party_name', 'party.display_name'],
        metaKeys: ['payment_method', 'currency', 'transaction_date'],
        print: true,
        fields: [
          {
            key: ['party_name', 'party.display_name'],
            label: 'Cari',
            linkPath: '/cari/kartlar/{id}',
            linkKey: ['party_id', 'party.id']
          },
          { key: ['party_code', 'party.code'], label: 'Cari Kodu' },
          { key: 'payment_method', label: 'Tahsilat yöntemi' },
          {
            key: ['account_name', 'account.name'],
            label: 'Kasa / banka hesabı',
            linkPath: '/finans/hesaplar/{id}',
            linkKey: ['account_id', 'account.id']
          },
          { key: 'amount', label: 'Tutar', kind: 'money' },
          { key: 'currency', label: 'Para birimi' },
          { key: 'exchange_rate', label: 'Kur', kind: 'decimal' },
          {
            key: ['base_amount', 'try_amount'],
            label: 'TRY karşılığı',
            kind: 'money',
            currencyKey: 'base_currency'
          },
          { key: ['transaction_date', 'document_date'], label: 'Tarih', kind: 'date' },
          { key: ['reference_no', 'reference'], label: 'Referans no' },
          { key: 'description', label: 'Açıklama' },
          {
            key: 'party_ledger_entry_id',
            label: 'Cari hareketi',
            kind: 'ref',
            refText: 'Cari hareketini aç',
            linkPath: '/cari/hareketler/{id}'
          },
          {
            key: 'movement_id',
            label: 'Kasa / banka hareketi',
            kind: 'ref',
            refText: 'Hesap hareketini aç',
            linkPath: '/finans/hareketler/{id}'
          },
          ...commonPosted
        ],
        tables: [
          {
            key: 'allocations',
            title: 'Fatura Dağılımları',
            columns: [
              { key: 'document_no', label: 'Fatura' },
              { key: 'due_date', label: 'Vade', kind: 'date' },
              {
                key: ['invoice_amount', 'original_amount'],
                label: 'Fatura tutarı',
                kind: 'money'
              },
              { key: 'amount', label: 'Uygulanan tutar', kind: 'money' },
              { key: ['remaining_amount', 'open_amount'], label: 'Kalan', kind: 'money' }
            ]
          }
        ]
      };
    case 'stock-movement':
      return {
        title: 'Stok Hareketi',
        listPath: '/stok/hareketler',
        endpoint: '/stock-movement-operations',
        numberKeys: ['movement_no', 'document_no', 'business_number'],
        numberFallback: (item) => {
          const source = textFallback(item, ['source_document_no', 'source.document_no'], '');
          return source ? `Stok Hareketi · ${source}` : 'Stok Hareketi';
        },
        statusKeys: ['status', 'state'],
        subjectKeys: ['product_name', 'product.name', 'product_code'],
        metaKeys: ['warehouse_name', 'movement_date', 'posted_at'],
        fields: [
          {
            key: ['product_name', 'product.name'],
            label: 'Stok',
            linkPath: '/stok/urunler/{id}',
            linkKey: ['product_id', 'product.id']
          },
          { key: ['product_code', 'product.code', 'sku'], label: 'SKU' },
          {
            key: ['variant_name', 'variant.name', 'variant_code'],
            label: 'Varyant / SKU',
            linkPath: '/stok/urunler/{id}',
            linkKey: ['product_id', 'product.id'],
            linkQueryKey: ['variant_id'],
            appendInactiveLabel: true
          },
          { key: 'direction', label: 'Türü' },
          { key: 'movement_type', label: 'Hareket türü' },
          { key: ['entered_quantity', 'quantity'], label: 'Miktar', kind: 'quantity' },
          { key: ['unit_code', 'unit'], label: 'Birim' },
          { key: 'stock_unit', label: 'Stok kartı birimi' },
          { key: ['source_document_no', 'source.document_no'], label: 'Kaynak belge' },
          { key: ['source_document_type', 'source.document_type'], label: 'Kaynak belge türü' },
          { key: 'base_quantity', label: 'Temel miktar', kind: 'quantity' },
          { key: 'quantity_delta', label: 'Stok etkisi', kind: 'quantity' },
          {
            key: ['warehouse_name', 'warehouse.name'],
            label: 'Depo',
            linkPath: '/stok/depolar/{id}',
            linkKey: ['warehouse_id', 'warehouse.id']
          },
          {
            key: ['lot_number', 'lot.lot_number'],
            label: 'Lot',
            linkPath: '/stok/lot-seri/lot/{id}',
            linkKey: ['lot_id', 'lot.id']
          },
          {
            key: ['serial_number', 'serial.serial_number'],
            label: 'Seri',
            linkPath: '/stok/lot-seri/seri/{id}',
            linkKey: ['serial_id', 'serial.id']
          },
          { key: ['total_cost', 'total_amount'], label: 'Toplam maliyet', kind: 'money' },
          { key: 'currency', label: 'Para birimi' },
          { key: ['reason_code', 'reason'], label: 'Neden' },
          { key: 'reason_description', label: 'Neden açıklaması' },
          { key: ['reference_no', 'reference'], label: 'Referans' },
          {
            key: ['posted_at', 'movement_date', 'transaction_date'],
            label: 'Tarih',
            kind: 'datetime'
          },
          { key: 'description', label: 'Açıklama' },
          ...commonPosted
        ]
      };
    case 'warehouse':
      return {
        title: 'Depo',
        listPath: '/stok/depolar',
        endpoint: '/warehouses',
        numberKeys: ['code'],
        numberFallback: (item) => textFallback(item, 'name', 'Depo'),
        statusKeys: ['status', 'is_active'],
        subjectKeys: ['name'],
        metaKeys: ['type', 'warehouse_type'],
        fields: [
          { key: 'code', label: 'Depo kodu' },
          { key: 'name', label: 'Depo adı' },
          { key: ['type', 'warehouse_type'], label: 'Depo türü' },
          {
            key: ['responsible_name', 'responsible_user_name'],
            label: 'Sorumlu'
          },
          { key: ['is_active', 'active'], label: 'Aktif' },
          { key: 'address', label: 'Adres' },
          { key: ['created_at'], label: 'Oluşturma zamanı', kind: 'datetime' },
          { key: ['updated_at'], label: 'Son güncelleme', kind: 'datetime' }
        ],
        tables: [
          {
            key: 'stock_positions',
            title: 'Stok Durumu',
            columns: [
              { key: ['sku', 'product_code'], label: 'SKU' },
              { key: ['product_name', 'product.name'], label: 'Stok' },
              { key: ['physical_quantity', 'physical'], label: 'Fiziki', kind: 'quantity' },
              { key: ['reserved_quantity', 'reserved'], label: 'Rezerve', kind: 'quantity' },
              {
                key: ['available_quantity', 'available'],
                label: 'Kullanılabilir',
                kind: 'quantity'
              },
              { key: ['unit_code', 'unit'], label: 'Birim' }
            ]
          }
        ]
      };
    case 'transfer':
      return {
        title: 'Transfer',
        listPath: '/stok/transferler',
        endpoint: '/warehouse-transfers',
        numberKeys: ['transfer_no', 'business_number'],
        numberFallback: () => 'Transfer',
        statusKeys: ['state', 'status'],
        subjectKeys: ['from_warehouse_name', 'source_warehouse_name', 'source_warehouse.name'],
        metaKeys: [
          'to_warehouse_name',
          'destination_warehouse_name',
          'destination_warehouse.name',
          'created_at'
        ],
        print: true,
        fields: [
          {
            key: ['source_warehouse_name', 'from_warehouse_name', 'source_warehouse.name'],
            label: 'Çıkış deposu',
            linkPath: '/stok/depolar/{id}',
            linkKey: ['source_warehouse_id', 'from_warehouse_id', 'source_warehouse.id']
          },
          {
            key: ['destination_warehouse_name', 'to_warehouse_name', 'destination_warehouse.name'],
            label: 'Varış deposu',
            linkPath: '/stok/depolar/{id}',
            linkKey: ['destination_warehouse_id', 'to_warehouse_id', 'destination_warehouse.id']
          },
          { key: ['transfer_type', 'type'], label: 'Transfer tipi', hideOnPrint: true },
          { key: '__transfer_status', label: 'Sevk durumu' },
          { key: 'created_at', label: 'Oluşturma tarihi', kind: 'date' },
          { key: 'arrival_at', label: 'Varış tarihi', kind: 'date' },
          { key: 'description', label: 'Açıklama' },
          { key: 'requested_at', label: 'Talep zamanı', kind: 'datetime' },
          { key: 'approved_at', label: 'Onay zamanı', kind: 'datetime' }
        ],
        tables: [
          {
            key: 'lines',
            title: 'Stoklar',
            columns: [
              {
                key: ['product_name', 'product.name'],
                label: 'Stok',
                linkPath: '/stok/urunler/{id}',
                linkKey: ['product_id', 'product.id']
              },
              {
                key: ['variant_code', 'variant.variant_code'],
                label: 'Varyant / SKU',
                linkPath: '/stok/urunler/{id}',
                linkKey: ['product_id', 'product.id'],
                linkQueryKey: ['variant_id'],
                appendInactiveLabel: true
              },
              { key: ['sent_quantity', 'quantity'], label: 'Miktar', kind: 'quantity' },
              { key: '__transfer_status', label: 'Sevk durumu' }
            ]
          },
          {
            key: 'events',
            title: 'Geçmiş',
            hideOnPrint: true,
            columns: [
              { key: ['occurred_at', 'created_at'], label: 'Zaman', kind: 'datetime' },
              { key: ['event', 'type', 'state'], label: 'İşlem' },
              { key: ['actor_name'], label: 'Kullanıcı' }
            ]
          }
        ]
      };
    case 'count':
      return {
        title: 'Sayım',
        listPath: '/stok/sayim',
        endpoint: '/stock-counts',
        numberKeys: ['count_no', 'business_number'],
        numberFallback: () => 'Sayım',
        statusKeys: ['state', 'status'],
        subjectKeys: ['warehouse_name', 'warehouse.name'],
        metaKeys: ['count_type', 'snapshot_at'],
        print: true,
        fields: [
          {
            key: ['warehouse_name', 'warehouse.name'],
            label: 'Depo',
            linkPath: '/stok/depolar/{id}',
            linkKey: ['warehouse_id', 'warehouse.id']
          },
          { key: ['count_type', 'type'], label: 'Sayım türü' },
          { key: ['count_date', 'document_date'], label: 'Sayım tarihi', kind: 'date' },
          { key: 'snapshot_at', label: 'Snapshot zamanı', kind: 'datetime' },
          { key: ['scope_description', 'scope'], label: 'Kapsam' },
          { key: 'description', label: 'Açıklama' }
        ],
        tables: [
          {
            key: 'lines',
            title: 'Sayım Satırları',
            columns: [
              { key: ['product_name', 'product.name'], label: 'Stok' },
              {
                key: ['system_quantity', 'snapshot_quantity', 'expected_quantity'],
                label: 'Sistem',
                kind: 'quantity'
              },
              { key: ['counted_quantity', 'counted'], label: 'Sayılan', kind: 'quantity' },
              { key: ['difference', 'difference_quantity'], label: 'Fark', kind: 'quantity' },
              { key: ['unit_code', 'unit'], label: 'Birim' }
            ]
          }
        ]
      };
    case 'lot':
      return {
        title: 'Lot',
        listPath: '/stok/lot-seri',
        endpoint: '/lots',
        numberKeys: ['lot_number', 'code'],
        numberFallback: () => 'Lot',
        statusKeys: ['status', 'state'],
        subjectKeys: ['product_name', 'product.name'],
        metaKeys: ['expires_at', 'expiry_date'],
        fields: [
          { key: ['lot_number', 'code'], label: 'Lot no' },
          {
            key: ['product_name', 'product.name'],
            label: 'Stok',
            linkPath: '/stok/urunler/{id}',
            linkKey: ['product_id', 'product.id']
          },
          { key: ['product_code', 'product.code'], label: 'Stok kodu' },
          { key: ['manufactured_at', 'manufactured_date'], label: 'Üretim tarihi', kind: 'date' },
          { key: ['expires_at', 'expiry_date'], label: 'SKT', kind: 'date' },
          { key: ['supplier_reference', 'supplier_lot_no'], label: 'Tedarikçi lot no' },
          { key: ['total_quantity', 'quantity'], label: 'Toplam', kind: 'quantity' },
          { key: ['reserved_quantity', 'reserved'], label: 'Rezerve', kind: 'quantity' },
          { key: ['available_quantity', 'available'], label: 'Kullanılabilir', kind: 'quantity' }
        ],
        tables: [
          {
            key: 'warehouse_distribution',
            title: 'Depo Dağılımı',
            columns: [
              { key: ['warehouse_name', 'warehouse.name'], label: 'Depo' },
              { key: ['quantity', 'available_quantity'], label: 'Miktar', kind: 'quantity' }
            ]
          },
          {
            key: 'movements',
            title: 'Hareketler',
            columns: [
              { key: ['posted_at', 'movement_date'], label: 'Tarih', kind: 'datetime' },
              { key: ['direction', 'movement_type'], label: 'İşlem' },
              { key: ['quantity', 'quantity_delta'], label: 'Miktar', kind: 'quantity' },
              {
                key: ['movement_id', 'id'],
                label: 'Hareket',
                kind: 'ref',
                refText: 'Aç',
                linkPath: '/stok/hareketler/{id}'
              }
            ]
          }
        ]
      };
    case 'serial':
      return {
        title: 'Seri Numarası',
        listPath: '/stok/lot-seri',
        endpoint: '/serial-numbers',
        numberKeys: ['serial_number', 'code'],
        numberFallback: () => 'Seri Numarası',
        statusKeys: ['status', 'state'],
        subjectKeys: ['product_name', 'product.name'],
        metaKeys: ['warehouse_name', 'active_warehouse_name', 'updated_at'],
        fields: [
          { key: ['serial_number', 'code'], label: 'Seri no' },
          {
            key: ['product_name', 'product.name'],
            label: 'Stok',
            linkPath: '/stok/urunler/{id}',
            linkKey: ['product_id', 'product.id']
          },
          { key: ['product_code', 'product.code'], label: 'SKU' },
          { key: ['status', 'state'], label: 'Durum' },
          {
            key: ['warehouse_name', 'active_warehouse_name'],
            label: 'Depo',
            linkPath: '/stok/depolar/{id}',
            linkKey: ['active_warehouse_id', 'warehouse_id']
          },
          { key: ['created_at', 'inbound_at'], label: 'Giriş tarihi', kind: 'datetime' },
          { key: ['source_document_no', 'source.document_no'], label: 'Kaynak belge' },
          { key: ['updated_at'], label: 'Son güncelleme', kind: 'datetime' }
        ],
        tables: [
          {
            key: 'timeline',
            title: 'Seri Geçmişi',
            columns: [
              { key: ['occurred_at', 'created_at'], label: 'Tarih', kind: 'datetime' },
              { key: ['event', 'movement_type', 'type'], label: 'İşlem' },
              { key: ['from_warehouse_name', 'source_warehouse_name'], label: 'Kaynak' },
              { key: ['to_warehouse_name', 'destination_warehouse_name'], label: 'Hedef' },
              { key: ['document_no', 'source_document_no'], label: 'Belge' },
              { key: ['party_name', 'party.display_name'], label: 'Cari' }
            ]
          }
        ]
      };
    case 'account':
      return {
        title: 'Finans Hesabı',
        listPath: '/finans/hesaplar',
        endpoint: '/finance/accounts',
        numberKeys: ['code'],
        numberFallback: (item) => textFallback(item, 'name', 'Finans Hesabı'),
        statusKeys: ['status', 'is_active'],
        subjectKeys: ['name'],
        metaKeys: ['account_type', 'currency'],
        fields: [
          { key: 'code', label: 'Hesap kodu' },
          { key: 'name', label: 'Hesap adı' },
          { key: 'account_type', label: 'Hesap türü' },
          { key: 'currency', label: 'Para birimi' },
          { key: ['bank_name', 'bank'], label: 'Banka' },
          { key: 'iban', label: 'IBAN' },
          { key: 'account_number', label: 'Hesap no' },
          { key: 'description', label: 'Açıklama' },
          { key: ['created_at'], label: 'Oluşturma zamanı', kind: 'datetime' }
        ]
      };
    case 'account-movement':
      return {
        title: 'Hesap Hareketi',
        listPath: '/finans/hareketler',
        endpoint: '/finance/movements',
        // Hesap hareketinin kendi numarası yoktur; kaynak belgesinden anılır.
        numberKeys: ['source_document_no'],
        // Rozet zaten movement_kind'ı, "Kaynak" alanı da source_label'ı
        // gösteriyor; başlıkta onları tekrarlamadan tutarı veriyoruz.
        numberFallback: (item) => {
          const amount = moneyFallback(item, 'amount');
          return amount ? `Hesap Hareketi · ${amount}` : 'Hesap Hareketi';
        },
        statusKeys: ['movement_kind'],
        subjectKeys: ['description'],
        metaKeys: ['transaction_date', 'currency'],
        fields: [
          { key: 'movement_kind', label: 'Hareket türü' },
          { key: 'direction', label: 'Yön' },
          { key: 'amount', label: 'Tutar', kind: 'money' },
          { key: 'currency', label: 'Para birimi' },
          { key: 'transaction_date', label: 'İşlem tarihi', kind: 'date' },
          { key: 'description', label: 'Açıklama' },
          { key: 'external_reference', label: 'Dış referans' },
          { key: ['source_label', 'source_type'], label: 'Kaynak' },
          {
            key: 'reversal_of_id',
            label: 'Ters kayıt ilişkisi',
            kind: 'ref',
            refText: 'Ters çevrilen hareketi aç',
            linkPath: '/finans/hareketler/{id}'
          },
          ...commonPosted
        ]
      };
    case 'finance-transfer':
      return {
        title: 'Hesap Transferi',
        listPath: '/finans/transferler',
        endpoint: '/finance/transfers',
        numberKeys: ['document_no'],
        numberFallback: (item) => {
          const amount = moneyFallback(item, 'amount');
          return amount ? `Hesap Transferi · ${amount}` : 'Hesap Transferi';
        },
        statusKeys: ['status'],
        subjectKeys: ['description'],
        metaKeys: ['transaction_date', 'currency'],
        fields: [
          { key: 'document_no', label: 'Belge no' },
          {
            key: ['from_account_name'],
            label: 'Kaynak hesap',
            linkPath: '/finans/hesaplar/{id}',
            linkKey: ['from_account_id']
          },
          {
            key: ['to_account_name'],
            label: 'Hedef hesap',
            linkPath: '/finans/hesaplar/{id}',
            linkKey: ['to_account_id']
          },
          { key: 'amount', label: 'Tutar', kind: 'money' },
          { key: 'currency', label: 'Para birimi' },
          { key: 'transaction_date', label: 'İşlem tarihi', kind: 'date' },
          { key: 'external_reference', label: 'Dış referans' },
          { key: 'description', label: 'Açıklama' },
          {
            key: 'reversal_of_id',
            label: 'Ters kayıt ilişkisi',
            kind: 'ref',
            refText: 'Ters çevrilen transferi aç',
            linkPath: '/finans/transferler/{id}'
          },
          ...commonPosted
        ]
      };
    case 'document':
      return {
        title: 'Belge',
        listPath: '/belgeler',
        endpoint: '/documents',
        numberKeys: ['document_no', 'number'],
        numberFallback: (item) =>
          textFallback(item, ['document_type_name', 'document_type_code'], 'Belge'),
        statusKeys: ['status', 'state'],
        subjectKeys: ['document_type_name', 'document_type_code', 'party_name'],
        metaKeys: ['document_date', 'currency'],
        fields: [
          { key: ['document_no', 'number'], label: 'Belge no' },
          { key: ['document_type_name', 'document_type_code'], label: 'Belge türü' },
          {
            key: ['party_name', 'party.display_name'],
            label: 'Cari',
            linkPath: '/cari/kartlar/{id}',
            linkKey: ['party_id', 'party.id']
          },
          { key: ['document_date', 'transaction_date'], label: 'Belge tarihi', kind: 'date' },
          { key: 'currency', label: 'Para birimi' },
          { key: ['grand_total', 'total'], label: 'Toplam', kind: 'money' },
          { key: ['notes', 'description'], label: 'Açıklama' }
        ],
        tables: [
          {
            key: 'lines',
            title: 'Satırlar',
            columns: [
              { key: ['product_name', 'product.name'], label: 'Stok' },
              { key: ['quantity', 'amount'], label: 'Miktar', kind: 'quantity' },
              { key: ['line_total', 'total'], label: 'Tutar', kind: 'money' }
            ]
          }
        ]
      };
  }
}
