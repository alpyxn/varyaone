/**
 * Single source of truth for the Raporlar module. Every report is a
 * declarative definition — endpoint, filters, columns — that ReportRunner
 * renders. Adding a report is a few lines here plus (when new) a backend
 * handler under /api/v1/reports.
 */

export type ReportFilterKind = 'dateRange' | 'direction';

export type ReportColumnFormat = 'text' | 'money' | 'qty' | 'date';

export type ReportColumn = {
  key: string;
  label: string;
  align?: 'right';
  format?: ReportColumnFormat;
  /** For 'money' columns: the row key that holds the ISO currency code. */
  currencyKey?: string;
};

export type ReportDef = {
  id: string;
  label: string;
  description?: string;
  endpoint: string;
  /** Extra permission required on top of reporting.read. */
  permission?: string;
  filters: ReportFilterKind[];
  columns: ReportColumn[];
  /** Row key whose truthy value flags the row with a warning badge. */
  warnKey?: string;
  warnText?: string;
};

const money = (key: string, label: string, currencyKey = 'currency'): ReportColumn => ({
  key,
  label,
  align: 'right',
  format: 'money',
  currencyKey
});

export const REPORTS: ReportDef[] = [
  {
    id: 'vadesi-gecen-alacaklar',
    label: 'Vadesi Geçen Alacaklar',
    endpoint: '/reports/overdue-receivables',
    filters: [],
    columns: [
      { key: 'document_no', label: 'Belge No' },
      { key: 'party_name', label: 'Cari' },
      { key: 'due_date', label: 'Vade', format: 'date' },
      { key: 'days_overdue', label: 'Gecikme (gün)', align: 'right' },
      money('amount_due', 'Kalan Tutar')
    ]
  },
  {
    id: 'vadesi-gecen-borclar',
    label: 'Vadesi Geçen Borçlar',
    endpoint: '/reports/overdue-payables',
    filters: [],
    columns: [
      { key: 'document_no', label: 'Belge No' },
      { key: 'party_name', label: 'Tedarikçi' },
      { key: 'due_date', label: 'Vade', format: 'date' },
      { key: 'days_overdue', label: 'Gecikme (gün)', align: 'right' },
      money('amount_due', 'Kalan Tutar')
    ]
  },
  {
    id: 'stok-degerleme',
    label: 'Stok Değerleme',
    endpoint: '/reports/stock-valuation',
    filters: [],
    warnKey: 'has_unpriced_stock',
    warnText: 'Fiyatsız stok — değer eksik olabilir',
    columns: [
      { key: 'warehouse_name', label: 'Depo' },
      { key: 'product_name', label: 'Ürün' },
      { key: 'quantity', label: 'Miktar', align: 'right', format: 'qty' },
      money('value', 'Değer', 'base_currency')
    ]
  },
  {
    id: 'en-cok-satanlar',
    label: 'En Çok Satan Ürünler',
    endpoint: '/reports/top-selling-products',
    filters: ['dateRange'],
    columns: [
      { key: 'product_name', label: 'Ürün' },
      { key: 'quantity', label: 'Miktar', align: 'right', format: 'qty' },
      money('revenue', 'Ciro', 'base_currency')
    ]
  },
  {
    id: 'satis-karliligi',
    label: 'Satış Kârlılığı',
    endpoint: '/reports/sales-profitability',
    permission: 'sales.cost.read',
    filters: ['dateRange'],
    warnKey: 'has_unpriced_cost',
    warnText: 'Maliyet eksik/provizyonel — marj düşük görünebilir',
    columns: [
      { key: 'document_no', label: 'Belge No' },
      { key: 'party_name', label: 'Cari' },
      money('revenue', 'Ciro', 'base_currency'),
      money('cost', 'Maliyet', 'base_currency'),
      money('gross_profit', 'Brüt Kâr', 'base_currency')
    ]
  },
  {
    id: 'vergi-ozeti',
    label: 'Vergi Özeti',
    endpoint: '/reports/tax-summary',
    filters: ['dateRange', 'direction'],
    columns: [
      { key: 'rate', label: 'Oran (%)', align: 'right' },
      money('tax_base', 'Matrah', 'base_currency'),
      money('tax_amount', 'KDV', 'base_currency'),
      money('withholding_amount', 'Tevkifat', 'base_currency')
    ]
  }
];

/** Report shown when the user lands on /raporlar without a specific report. */
export const DEFAULT_REPORT_ID = 'vadesi-gecen-alacaklar';

export function reportByID(id: string): ReportDef | undefined {
  return REPORTS.find((report) => report.id === id);
}

export function canSeeReport(def: ReportDef, permissions: readonly string[]): boolean {
  if (!permissions.includes('reporting.read')) return false;
  return def.permission ? permissions.includes(def.permission) : true;
}
