/**
 * Dependency-free spreadsheet export. Emits an Excel-openable HTML-table
 * workbook (.xls) — Excel and LibreOffice open it as a real sheet with typed
 * cells, unlike a plain CSV. Reusable anywhere a "Excel'e aktar" button is
 * needed.
 */

export type SheetCell = string | number | null | undefined;

function escapeHtml(value: SheetCell): string {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

function cell(value: SheetCell): string {
  if (typeof value === 'number' && Number.isFinite(value)) {
    return `<td style="mso-number-format:'0.00'">${value}</td>`;
  }
  // Numeric strings become real numbers; everything else stays text.
  const asNumber =
    typeof value === 'string' && value.trim() !== '' && !Number.isNaN(Number(value))
      ? Number(value)
      : null;
  if (asNumber !== null) {
    return `<td style="mso-number-format:'0.00'">${asNumber}</td>`;
  }
  return `<td style="mso-number-format:'\\@'">${escapeHtml(value)}</td>`;
}

export function downloadXls(
  filename: string,
  sheetName: string,
  rows: SheetCell[][],
  headerRow?: string[]
): void {
  const head = headerRow
    ? `<tr>${headerRow.map((h) => `<th>${escapeHtml(h)}</th>`).join('')}</tr>`
    : '';
  const body = rows.map((r) => `<tr>${r.map(cell).join('')}</tr>`).join('');
  const safeSheet = escapeHtml(sheetName).slice(0, 31) || 'Sayfa1';
  const html = `<html xmlns:o="urn:schemas-microsoft-com:office:office" xmlns:x="urn:schemas-microsoft-com:office:excel">
    <head><meta charset="utf-8" />
      <!--[if gte mso 9]><xml><x:ExcelWorkbook><x:ExcelWorksheets><x:ExcelWorksheet>
        <x:Name>${safeSheet}</x:Name>
        <x:WorksheetOptions><x:DisplayGridlines/></x:WorksheetOptions>
      </x:ExcelWorksheet></x:ExcelWorksheets></x:ExcelWorkbook></xml><![endif]-->
      <style>th{background:#f0f0f0;font-weight:bold;} td,th{border:1px solid #ccc;padding:4px;}</style>
    </head>
    <body><table>${head}${body}</table></body></html>`;

  const blob = new Blob(['\ufeff' + html], { type: 'application/vnd.ms-excel;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename.endsWith('.xls') ? filename : `${filename}.xls`;
  link.click();
  URL.revokeObjectURL(url);
}
