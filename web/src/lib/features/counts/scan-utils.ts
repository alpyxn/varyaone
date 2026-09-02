import type { CountLine, ScanEvent } from './types';

export type ParsedScan = { barcode: string; quantity?: string };

/** Accepts keyboard wedges, manual entry, and common scanner quantity suffixes. */
export function parseWedgeInput(value: string): ParsedScan | null {
  const input = value.replace(/[\r\n]+$/g, '').trim();
  if (!input) return null;
  const match = input.match(/^(.*?)(?:\s*[x*;]\s*(\d+(?:[.,]\d+)?))?$/i);
  const barcode = match?.[1]?.trim() ?? input;
  if (!barcode) return null;
  const quantityText = match?.[2]?.replace(',', '.');
  if (!quantityText) return { barcode };
  if (!/^(?:0|[1-9]\d*)(?:\.\d{1,8})?$/.test(quantityText) || /^0(?:\.0*)?$/.test(quantityText)) {
    return null;
  }
  return { barcode, quantity: quantityText };
}

export function createStableEventId(prefix = 'scan') {
  const randomUUID =
    typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : '';
  return `${prefix}:${randomUUID || `${Date.now()}-${Math.random().toString(36).slice(2)}`}`;
}

export function makeScanEvent(
  barcode: string,
  quantity: string,
  eventID = createStableEventId()
): ScanEvent {
  return {
    event_id: eventID,
    barcode: barcode.trim(),
    quantity,
    scanned_at: new Date().toISOString()
  };
}

export function findLineByBarcode(lines: CountLine[], barcode: string) {
  const normalized = barcode.trim().toLocaleLowerCase('tr-TR');
  return lines.find((line) => line.barcode?.trim().toLocaleLowerCase('tr-TR') === normalized);
}

/** Intentionally line-scoped: a count workspace must never offer bulk zeroing. */
export function zeroConfirmationText(line: CountLine) {
  return `${line.product_name} satırını 0 olarak doğrulamak istediğinize emin misiniz?`;
}
