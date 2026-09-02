/**
 * Minimal, dependency-free print/PDF engine. Opens the composed document in a
 * detached window and triggers the browser's print dialog, from which the user
 * can "Save as PDF". Reusable for any printable document (cari ekstre, receipts,
 * reports, alış/satış fişleri): pass a title, the company header, and the body
 * as an HTML string.
 *
 * Every document rendered through here carries the Varya One brand mark and the
 * line "Bu belge Varya One ile oluşturulmuştur." in its footer.
 */

/** Varya One brand mark — 3 kırmızı eğik bar + 1 siyah dik bar. */
export const VARYA_ONE_LOGO_SVG = `<svg viewBox="0 0 220 220" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Varya One">
  <g fill="#C1272D">
    <rect x="20" y="100" width="30" height="90" rx="3" transform="rotate(-26 35 190)"/>
    <rect x="70" y="55" width="30" height="135" rx="3" transform="rotate(-12 85 190)"/>
    <rect x="120" y="35" width="30" height="155" rx="3" transform="rotate(-4 135 190)"/>
  </g>
  <rect x="170" y="10" width="30" height="180" rx="3" fill="#1A1A1A"/>
</svg>`;

/** Kalıcı marka satırı — her PDF'in altında görünür. */
export const VARYA_ONE_FOOTER_TEXT = 'Bu belge Varya One ile oluşturulmuştur.';

export type PrintCompany = {
  name: string;
  /** Optional data: URI logo. */
  logo?: string;
  taxNumber?: string;
};

export type PrintStampTone = 'neutral' | 'info' | 'success' | 'danger';

export type PrintStamp = {
  /** Damga metni, örn. "TEKLİF", "FATURA", "İPTAL". */
  label: string;
  tone?: PrintStampTone;
};

export type PrintRecipient = {
  name: string;
  code?: string;
  taxNumber?: string;
  taxOffice?: string;
  address?: string;
  phone?: string;
  email?: string;
};

export type PrintMetaEntry = { label: string; value: string };

export type PrintDocumentInput = {
  /** Browser/print title and the document heading. */
  title: string;
  /** Optional line under the title (e.g. party name, date range). */
  subtitle?: string;
  company: PrintCompany;
  /** Optional diagonal stamp on the top-right of the body (teklif/fatura/iptal…). */
  stamp?: PrintStamp;
  /** Optional "Sayın …" recipient block rendered under the header. */
  recipient?: PrintRecipient;
  /** Optional key/value grid rendered next to the recipient block. */
  meta?: PrintMetaEntry[];
  /** Fully-formed HTML for the document body (tables, summaries, …). */
  bodyHtml: string;
  /** Optional footer note, shown above the permanent Varya One line. */
  footerNote?: string;
  /** Optional extra CSS appended after the base print styles. */
  bodyStyles?: string;
};

function escapeHtml(value: string): string {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

/** Escape a value for safe interpolation into the body HTML that callers build. */
export function ph(value: string | number | null | undefined): string {
  return escapeHtml(String(value ?? ''));
}

/**
 * A base64 `data:` image URI and nothing else. The logo is echoed straight into
 * a `src="..."` attribute of the detached print window, so anything that is not
 * a well-formed base64 image URI (the only shape the settings uploader ever
 * produces, via `FileReader.readAsDataURL`) is dropped rather than trusted — a
 * crafted value such as `data:image/png"><script>…` must never reach the markup.
 */
const SAFE_IMAGE_DATA_URI = /^data:image\/[a-z0-9.+-]+;base64,[A-Za-z0-9+/]+=*$/i;

function safeLogoSrc(logo: string | undefined): string {
  return logo && SAFE_IMAGE_DATA_URI.test(logo) ? logo : '';
}

const STAMP_TONES: Record<PrintStampTone, string> = {
  neutral: '#6b7280',
  info: '#1d4ed8',
  success: '#15803d',
  danger: '#b91c1c'
};

const PRINT_STYLES = `
  * { box-sizing: border-box; }
  body {
    margin: 0;
    padding: 28px 34px 96px;
    font: 12px/1.45 -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
    color: #111827;
    -webkit-print-color-adjust: exact;
    print-color-adjust: exact;
  }
  .doc-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 20px;
    border-bottom: 2px solid #111827;
    padding-bottom: 12px;
    margin-bottom: 18px;
  }
  .doc-header .company { display: flex; align-items: center; gap: 12px; }
  .doc-header img { max-height: 52px; max-width: 180px; object-fit: contain; }
  .doc-header .company-name { font-size: 15px; font-weight: 700; }
  .doc-header .company-meta { font-size: 10.5px; color: #6b7280; margin-top: 2px; }
  .doc-title { text-align: right; }
  .doc-title h1 { margin: 0; font-size: 17px; }
  .doc-title .subtitle { font-size: 11px; color: #6b7280; margin-top: 3px; }
  .doc-title .printed-at { font-size: 10px; color: #9ca3af; margin-top: 4px; }
  .doc-body { position: relative; }
  /* Damga: içeriğin gerisinde, soluk bir filigran. Metnin okunmasını engellemez. */
  .doc-stamp {
    position: absolute;
    top: 38%;
    left: 50%;
    transform: translate(-50%, -50%) rotate(-24deg);
    border: 6px double currentColor;
    border-radius: 14px;
    padding: 10px 44px;
    font-size: 68px;
    font-weight: 800;
    letter-spacing: 0.16em;
    text-transform: uppercase;
    opacity: 0.08;
    pointer-events: none;
    white-space: nowrap;
    z-index: 0;
  }
  .doc-body > *:not(.doc-stamp) { position: relative; z-index: 1; }
  .doc-parties {
    display: grid;
    grid-template-columns: 1.1fr 1fr;
    gap: 18px;
    margin-bottom: 16px;
  }
  .doc-parties .recipient span,
  .doc-parties .meta span { display: block; font-size: 9.5px; color: #6b7280; text-transform: uppercase; letter-spacing: 0.04em; }
  .doc-parties .recipient .name { font-size: 13px; font-weight: 700; color: #111827; margin-top: 2px; }
  .doc-parties .recipient .rec-line { font-size: 11px; color: #374151; margin-top: 1px; }
  .doc-parties .meta { border: 1px solid #e5e7eb; border-radius: 6px; padding: 8px 10px; }
  .doc-parties .meta dl { display: grid; grid-template-columns: auto 1fr; gap: 2px 10px; margin: 4px 0 0; }
  .doc-parties .meta dt { font-size: 10px; color: #6b7280; }
  .doc-parties .meta dd { font-size: 10.5px; margin: 0; text-align: right; font-weight: 600; }
  table { width: 100%; border-collapse: collapse; margin: 10px 0; }
  th, td { padding: 6px 8px; border-bottom: 1px solid #e5e7eb; text-align: left; }
  th { background: #f3f4f6; font-size: 10.5px; text-transform: uppercase; letter-spacing: 0.03em; }
  td.right, th.right { text-align: right; }
  .summary-grid {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 10px;
    margin: 6px 0 14px;
  }
  .summary-grid .cell { border: 1px solid #e5e7eb; border-radius: 6px; padding: 8px 10px; }
  .summary-grid .cell span { display: block; font-size: 10px; color: #6b7280; }
  .summary-grid .cell strong { font-size: 13px; }
  .doc-footer {
    position: fixed;
    left: 34px;
    right: 34px;
    bottom: 18px;
    border-top: 1px solid #e5e7eb;
    padding-top: 8px;
    font-size: 9px;
    color: #9ca3af;
  }
  .doc-footer .note { margin-bottom: 5px; line-height: 1.35; text-align: center; }
  .doc-footer .brand { display: flex; align-items: center; justify-content: center; gap: 6px; color: #6b7280; }
  .doc-footer .brand svg { width: 13px; height: 13px; }
  .doc-footer .brand strong { font-weight: 700; color: #374151; }
  /* @page margin:0 => tarayıcının kendi üstbilgi/altbilgisini (tarih, başlık,
     about:blank, sayfa no) bastırır; kenar boşluklarını body/footer veriyor. */
  @media print {
    body { padding: 16mm 15mm 30mm; }
    .doc-footer { left: 15mm; right: 15mm; bottom: 10mm; }
    @page { margin: 0; }
  }
`;

function renderRecipientBlock(recipient?: PrintRecipient, meta?: PrintMetaEntry[]): string {
  if (!recipient && !(meta && meta.length)) return '';
  const recLines: string[] = [];
  if (recipient?.code) recLines.push(`Kod: ${escapeHtml(recipient.code)}`);
  if (recipient?.taxNumber)
    recLines.push(
      `VKN/TCKN: ${escapeHtml(recipient.taxNumber)}${
        recipient.taxOffice ? ` · ${escapeHtml(recipient.taxOffice)}` : ''
      }`
    );
  else if (recipient?.taxOffice) recLines.push(`Vergi dairesi: ${escapeHtml(recipient.taxOffice)}`);
  if (recipient?.address) recLines.push(escapeHtml(recipient.address));
  const contact = [recipient?.phone, recipient?.email].filter(Boolean).map(String);
  if (contact.length) recLines.push(escapeHtml(contact.join(' · ')));

  const recipientHtml = recipient
    ? `<div class="recipient">
        <span>Sayın</span>
        <div class="name">${escapeHtml(recipient.name)}</div>
        ${recLines.map((line) => `<div class="rec-line">${line}</div>`).join('')}
      </div>`
    : '<div class="recipient"></div>';

  const metaHtml =
    meta && meta.length
      ? `<div class="meta">
          <span>Belge bilgileri</span>
          <dl>${meta
            .map(
              (entry) => `<dt>${escapeHtml(entry.label)}</dt><dd>${escapeHtml(entry.value)}</dd>`
            )
            .join('')}</dl>
        </div>`
      : '<div class="meta"></div>';

  return `<div class="doc-parties">${recipientHtml}${metaHtml}</div>`;
}

export function printDocument(input: PrintDocumentInput): void {
  const printedAt = new Date().toLocaleString('tr-TR');
  const logoSrc = safeLogoSrc(input.company.logo);
  const logo = logoSrc ? `<img src="${logoSrc}" alt="" />` : '';
  const companyMeta = input.company.taxNumber
    ? `<div class="company-meta">VKN/TCKN: ${escapeHtml(input.company.taxNumber)}</div>`
    : '';
  const stamp = input.stamp
    ? `<div class="doc-stamp" style="color:${
        STAMP_TONES[input.stamp.tone ?? 'neutral']
      }">${escapeHtml(input.stamp.label)}</div>`
    : '';
  const footerNote = input.footerNote
    ? `<div class="note">${escapeHtml(input.footerNote)}</div>`
    : '';
  const html = `<!doctype html><html lang="tr"><head><meta charset="utf-8" />
    <title>${escapeHtml(input.title)}</title><style>${PRINT_STYLES}${input.bodyStyles ?? ''}</style></head>
    <body>
      <div class="doc-header">
        <div class="company">
          ${logo}
          <div>
            <div class="company-name">${escapeHtml(input.company.name)}</div>
            ${companyMeta}
          </div>
        </div>
        <div class="doc-title">
          <h1>${escapeHtml(input.title)}</h1>
          ${input.subtitle ? `<div class="subtitle">${escapeHtml(input.subtitle)}</div>` : ''}
          <div class="printed-at">Yazdırma: ${escapeHtml(printedAt)}</div>
        </div>
      </div>
      <div class="doc-body">
        ${stamp}
        ${renderRecipientBlock(input.recipient, input.meta)}
        ${input.bodyHtml}
      </div>
      <div class="doc-footer">
        ${footerNote}
        <div class="brand">${VARYA_ONE_LOGO_SVG}<span><strong>Varya One</strong> · ${escapeHtml(
          VARYA_ONE_FOOTER_TEXT
        )}</span></div>
      </div>
    </body></html>`;

  const win = window.open('', '_blank', 'width=900,height=1000');
  if (!win) return;
  win.document.open();
  win.document.write(html);
  win.document.close();
  win.focus();
  // Give the browser a tick to lay out (and decode the logo) before printing.
  win.setTimeout(() => {
    win.print();
  }, 250);
}
