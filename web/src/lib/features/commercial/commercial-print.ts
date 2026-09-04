/**
 * Alış/satış belgelerini (teklif, sipariş, irsaliye, fatura, iade) yazdırılabilir
 * bir "fiş"e dönüştürür. Belge türüne ve durumuna göre bir damga seçer
 * (TEKLİF / SİPARİŞ / İRSALİYE / FATURA / İADE, ya da TASLAK / İPTAL / ÖDENDİ).
 *
 * Saf fonksiyondur; DOM'a dokunmaz. Çıktısı `printDocument`'e verilir.
 */
import type { Company } from '$lib/api';
import { trimDecimalZeros } from '$lib/design/decimal';
import { formatMoney, formatQuantityWithUnit, formatUnitPrice } from '$lib/design/formatters';
import { ph, type PrintDocumentInput, type PrintStamp } from '$lib/design/print';
import type { Party } from '$lib/features/parties/types';
import { lineAmounts, lineComponentAmounts } from './commercial-calc';
import { documentStatusLabel, type CommercialResourceConfig } from './types';
import type { DocumentRecord, LineDraft } from './editor-types';

export type CommercialDocumentTotals = {
  subtotal: string;
  discount: string;
  /** Total tax: KDV plus every additional tax on the lines. */
  tax: string;
  /** The KDV part of `tax`. */
  vat?: string;
  /** ÖTV and every other tax charged besides KDV. */
  additionalTax?: string;
  grand: string;
};

export type BuildCommercialDocumentInput = {
  config: CommercialResourceConfig;
  record: DocumentRecord;
  lines: LineDraft[];
  currency: string;
  totals: CommercialDocumentTotals;
  company?: Company;
  party?: Party;
};

function upper(value: unknown): string {
  return String(value ?? '')
    .trim()
    .toUpperCase();
}

function firstText(...values: unknown[]): string {
  for (const value of values) {
    const text = String(value ?? '').trim();
    if (text) return text;
  }
  return '';
}

function formatDocDate(value?: string): string {
  const raw = String(value ?? '').slice(0, 10);
  if (!raw) return '—';
  const parts = raw.split('-');
  if (parts.length === 3) return `${parts[2]}.${parts[1]}.${parts[0]}`;
  return raw;
}

/** Belge türü + yaşam döngüsü + ödeme durumundan damgayı seçer. */
export function resolveCommercialStamp(
  config: CommercialResourceConfig,
  record: DocumentRecord
): PrintStamp {
  const lifecycle = upper(record.lifecycle_status ?? record.status);
  if (lifecycle === 'CANCELLED') return { label: 'İPTAL', tone: 'danger' };
  if (lifecycle === 'DRAFT') return { label: 'TASLAK', tone: 'neutral' };

  if (config.resource === 'invoices' && upper(record.payment_status) === 'PAID') {
    return { label: 'ÖDENDİ', tone: 'success' };
  }

  const finalized =
    lifecycle === 'POSTED' ||
    lifecycle === 'FINALIZED' ||
    lifecycle === 'ACCEPTED' ||
    lifecycle === 'FULFILLED';
  const tone = finalized ? 'success' : 'info';
  switch (config.resource) {
    case 'quotes':
      return { label: 'TEKLİF', tone };
    case 'orders':
      return { label: 'SİPARİŞ', tone };
    case 'dispatches':
      return { label: 'İRSALİYE', tone };
    case 'invoices':
      return { label: 'FATURA', tone };
    case 'returns':
      return { label: 'İADE', tone };
    default:
      return { label: 'BELGE', tone };
  }
}

/**
 * Belge türüne göre imza satırları. Teklif bir fiyat bildirimidir; "Teslim Alan"
 * gibi bir imza mantıklı değil, o yüzden imza bloğu hiç basılmaz. İrsaliye/iade
 * fiilî teslimi belgeler, fatura ise düzenleyen + teslim alan taşır.
 */
function commercialSignatures(config: CommercialResourceConfig): [string, string] | null {
  switch (config.resource) {
    case 'quotes':
      return null;
    case 'orders':
      return ['Düzenleyen', 'Onaylayan'];
    case 'dispatches':
    case 'returns':
      return ['Teslim Eden', 'Teslim Alan'];
    case 'invoices':
      return ['Düzenleyen', 'Teslim Alan'];
    default:
      return ['Düzenleyen', 'Teslim Alan'];
  }
}

function documentNumber(record: DocumentRecord): string {
  return firstText(
    record.document_no,
    record.invoice_no,
    record.order_no,
    record.receipt_no,
    record.return_no
  );
}

function documentDate(record: DocumentRecord): string {
  return firstText(
    record.document_date,
    record.invoice_date,
    record.order_date,
    record.receipt_date,
    record.return_date
  );
}

function lineDescription(line: LineDraft): string {
  const bits: string[] = [];
  const name = firstText(line.product?.title, line.description);
  if (name) bits.push(name);
  if (line.variant?.title) bits.push(line.variant.title);
  if (line.description && line.description !== name) bits.push(line.description);
  return bits.join(' · ') || 'Kalem';
}

function lineTotal(line: LineDraft, currency: string): string {
  if (line.persistedTotal) return formatMoney(line.persistedTotal, currency);
  return formatMoney(lineAmounts(line).total, currency);
}

/**
 * Fişin altına düşen yasal uyarı. Bu çıktılar Varya One içindeki işlem kaydının
 * bir görünümüdür; VUK kapsamında resmi mali belge (fatura/e-Fatura, sevk
 * irsaliyesi/e-İrsaliye) yerine geçmez.
 */
export function commercialDisclaimer(config: CommercialResourceConfig): string {
  const base =
    'Bu belge Varya One içerisindeki işlem kaydının çıktısıdır ve resmi bir mali belge değildir.';
  const specific: Partial<Record<CommercialResourceConfig['resource'], string>> = {
    quotes:
      'Yalnızca bilgilendirme amaçlıdır; fiyat teklifi olup satış taahhüdü ve fatura yerine geçmez.',
    orders: 'Sipariş kaydı olup fatura, sevk irsaliyesi veya e-İrsaliye yerine geçmez.',
    dispatches: 'Vergi Usul Kanunu kapsamında sevk irsaliyesi veya e-İrsaliye yerine geçmez.',
    invoices: 'Vergi Usul Kanunu kapsamında fatura, e-Fatura veya e-Arşiv Fatura yerine geçmez.',
    returns: 'İşlem kaydıdır; iade faturası veya resmi iade belgesi yerine geçmez.'
  };
  const tail = specific[config.resource];
  return tail ? `${base} ${tail}` : base;
}

export function buildCommercialDocument(input: BuildCommercialDocumentInput): PrintDocumentInput {
  const { config, record, lines, currency, totals, company, party } = input;
  const stamp = resolveCommercialStamp(config, record);
  const docNo = documentNumber(record);

  const recipientName = firstText(
    party?.trade_name,
    party?.legal_name,
    party?.display_name,
    record.party_name,
    record.supplier_name,
    'Cari'
  );

  const recipient = {
    name: recipientName,
    code: firstText(party?.code, record.party_code, record.supplier_code) || undefined,
    taxNumber: firstText(party?.tax_number, party?.identity_number) || undefined,
    taxOffice: firstText(party?.tax_office) || undefined,
    address: firstText(party?.address_summary) || undefined,
    phone: firstText(party?.phone) || undefined,
    email: firstText(party?.email) || undefined
  };

  const rate = firstText(record.exchange_rate);
  const hasRate = rate !== '' && Number(rate) !== 1 && Number.isFinite(Number(rate));
  const meta = [
    { label: 'Belge No', value: docNo || '—' },
    { label: 'Belge Tarihi', value: formatDocDate(documentDate(record)) },
    config.resource === 'quotes' && record.valid_until
      ? { label: 'Geçerlilik', value: formatDocDate(record.valid_until) }
      : record.due_date
        ? { label: 'Vade', value: formatDocDate(record.due_date) }
        : null,
    {
      label: 'Para Birimi',
      value: currency + (hasRate ? ` (kur ${trimDecimalZeros(rate)})` : '')
    },
    firstText(record.warehouse_name, record.default_warehouse_name)
      ? {
          label: 'Depo',
          value: firstText(record.warehouse_name, record.default_warehouse_name)
        }
      : null,
    firstText(record.source_document_no)
      ? { label: 'Kaynak Belge', value: firstText(record.source_document_no) }
      : null,
    {
      label: 'Durum',
      value: documentStatusLabel(record.lifecycle_status ?? record.status)
    }
  ].filter((entry): entry is { label: string; value: string } => entry !== null);

  const rows = lines
    .map((line, index) => {
      const amounts = lineAmounts(line);
      const discountRate = firstText(line.discountRate);
      const taxRate = firstText(line.taxRate);
      const extraTaxes = lineComponentAmounts(line)
        .map(
          (component) =>
            `${component.name || component.code} ${
              component.calculationType === 'PERCENTAGE'
                ? `%${trimDecimalZeros(component.rate)}`
                : formatUnitPrice(component.rate, currency)
            }`
        )
        .join(', ');
      return `<tr>
        <td class="right">${index + 1}</td>
        <td>${ph(lineDescription(line))}${extraTaxes ? `<small class="line-taxes">${ph(extraTaxes)}</small>` : ''}</td>
        <td>${ph(line.warehouse?.title ?? '')}</td>
        <td class="right">${ph(formatQuantityWithUnit(line.quantity, line.unitCode))}</td>
        <td class="right">${ph(formatUnitPrice(line.unitPrice || '0', currency))}</td>
        <td class="right">${discountRate && discountRate !== '0' ? ph(`%${discountRate}`) : '—'}</td>
        <td class="right">${taxRate && taxRate !== '0' ? ph(`%${taxRate}`) : '—'}</td>
        <td class="right">${ph(line.persistedTotal ? lineTotal(line, currency) : formatMoney(amounts.total, currency))}</td>
      </tr>`;
    })
    .join('');

  const settlement = record.settlement;
  const isSales = config.direction === 'sales';
  const settlementRows =
    config.resource === 'invoices' && settlement
      ? `
        <tr><td>${isSales ? 'Tahsil Edilen' : 'Ödenen'}</td><td class="right">${ph(
          formatMoney(
            firstText(isSales ? settlement.collected_total : settlement.paid_total, '0'),
            currency
          )
        )}</td></tr>
        <tr><td>${isSales ? 'Kalan Tahsilat' : 'Kalan Ödeme'}</td><td class="right">${ph(
          formatMoney(
            firstText(isSales ? settlement.amount_due : settlement.amount_payable, '0'),
            currency
          )
        )}</td></tr>`
      : '';

  const signatures = commercialSignatures(config);
  const signaturesHtml = signatures
    ? `<div class="signatures">
      <div><span>${ph(signatures[0])}</span></div>
      <div><span>${ph(signatures[1])}</span></div>
    </div>`
    : '';

  const notes = firstText(record.notes);
  const notesHtml = notes
    ? `<div class="doc-notes"><span>Notlar</span><p>${ph(notes)}</p></div>`
    : '';

  const bodyHtml = `
    <table class="lines">
      <thead>
        <tr>
          <th class="right">#</th>
          <th>Ürün / Hizmet</th>
          <th>Depo</th>
          <th class="right">Miktar</th>
          <th class="right">Birim Fiyat</th>
          <th class="right">İsk.</th>
          <th class="right">KDV</th>
          <th class="right">Tutar</th>
        </tr>
      </thead>
      <tbody>${rows || '<tr><td colspan="8">Kalem yok</td></tr>'}</tbody>
    </table>

    <div class="doc-totals">
      <table class="totals">
        <tr><td>Brüt Toplam</td><td class="right">${ph(formatMoney(totals.subtotal, currency))}</td></tr>
        <tr><td>İskonto</td><td class="right">${ph(formatMoney(totals.discount, currency))}</td></tr>
        <tr><td>KDV</td><td class="right">${ph(formatMoney(totals.vat ?? totals.tax, currency))}</td></tr>
        ${
          totals.additionalTax && trimDecimalZeros(totals.additionalTax) !== '0'
            ? `<tr><td>Ek Vergiler</td><td class="right">${ph(formatMoney(totals.additionalTax, currency))}</td></tr>
        <tr><td>Toplam Vergi</td><td class="right">${ph(formatMoney(totals.tax, currency))}</td></tr>`
            : ''
        }
        <tr class="grand"><td>Genel Toplam</td><td class="right">${ph(formatMoney(totals.grand, currency))}</td></tr>
        ${settlementRows}
      </table>
    </div>

    ${notesHtml}

    ${signaturesHtml}`;

  return {
    title: config.title,
    subtitle: [recipientName, docNo].filter(Boolean).join(' · '),
    company: {
      name: firstText(company?.trade_name, company?.legal_name, 'Şirket'),
      logo: company?.logo || undefined,
      taxNumber: company?.tax_number
    },
    stamp,
    recipient,
    meta,
    bodyHtml,
    footerNote: commercialDisclaimer(config),
    bodyStyles: `
      table.lines th, table.lines td { font-size: 10.5px; }
      table.lines td { vertical-align: top; }
      td small.line-taxes { display: block; color: #4b5563; font-size: 10px; margin-top: 2px; }
      .doc-totals { display: flex; justify-content: flex-end; margin-top: 8px; }
      table.totals { width: 320px; margin: 0; }
      table.totals td { border-bottom: none; padding: 3px 8px; font-size: 11.5px; }
      table.totals tr.grand td { border-top: 2px solid #111827; font-weight: 700; font-size: 13px; padding-top: 6px; }
      .doc-notes { margin-top: 16px; max-width: 520px; }
      .doc-notes span { display: block; font-size: 9.5px; color: #6b7280; text-transform: uppercase; letter-spacing: 0.04em; }
      .doc-notes p { margin: 3px 0 0; font-size: 11px; white-space: pre-wrap; }
      .signatures { display: flex; gap: 40px; margin-top: 48px; }
      .signatures div { flex: 1; border-top: 1px solid #9ca3af; padding-top: 6px; text-align: center; }
      .signatures span { font-size: 10.5px; color: #6b7280; }
    `
  };
}
