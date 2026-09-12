import messages from './error-messages.json';

const catalog: Readonly<Record<string, string>> = Object.assign(Object.create(null), messages);
const catalogMessages = new Set(Object.values(messages));
const terms: Readonly<Record<string, string>> = {
  'If-Match': 'kayıt sürümü',
  'Idempotency-Key': 'işlem kimliği',
  idempotency: 'işlem kimliği',
  cursor: 'sayfalama',
  UUID: 'kayıt kimliği',
  run: 'bordro',
  'post edilemez': 'işlenemez',
  company_id: 'firma',
  warehouse_id: 'depo',
  product_id: 'stok kartı',
  variant_id: 'varyant',
  party_id: 'cari kartı',
  account_id: 'hesap',
  employee_id: 'çalışan',
  user_id: 'kullanıcı',
  actor_user_id: 'işlemi yapan kullanıcı',
  location_id: 'depo konumu',
  lot_id: 'lot',
  serial_id: 'seri numarası',
  source_location_id: 'çıkış konumu',
  destination_location_id: 'varış konumu',
  count_id: 'sayım',
  pass_id: 'sayım turu',
  session_id: 'oturum',
  event_id: 'sayım işlemi',
  unit_id: 'birim',
  tax_rate: 'vergi oranı',
  tax_rate_id: 'vergi oranı',
  discount_rate: 'iskonto oranı',
  description: 'açıklama',
  date: 'tarih',
  due_date: 'vade tarihi',
  document_date: 'belge tarihi',
  quantity: 'miktar',
  unit_price: 'birim fiyat',
  currency: 'para birimi',
  exchange_rate: 'döviz kuru'
};
function readableTerms(message: string) {
  return message.replace(
    /If-Match|Idempotency-Key|post edilemez|\b(?:[a-z]+(?:_[a-z]+)+|idempotency|cursor|UUID|run|quantity|unit_price|currency|exchange_rate)\b/g,
    (term) => terms[term] ?? term
  );
}
export const DEFAULT_ERROR_MESSAGE = 'İşlem tamamlanamadı. Lütfen tekrar deneyin.';

// Server messages may include user-entered names. A Turkish character alone
// does not make a raw English exception safe to show (e.g. "Invalid value: Şube").
const technicalMessage =
  /\b(?:error|exception|failed|invalid|unexpected|required|undefined|null|stack|SQLSTATE|syntax|constraint|panic|fetch|network|timeout|not found|not allowed|not available|cannot|must|missing|payload|permission denied|run)\b/i;
const turkishMessage =
  /(?:girilmemiş|tanımlanmamış|belirtilmemiş|geçersiz|gereklidir|gerekli|zorunlu|bulunamadı|bulunmuyor|başarısız|tamamlanamadı|kaydedilemedi|alınamadı|okunamadı|yüklenemedi|oluşturulamadı|güncellenemedi|silinemedi|yapılamaz|yapılamadı|seçilemez|değiştirilemez|aşamaz|olamaz|eşleşmiyor|uyuşmuyor|desteklenmiyor|yetersiz|eksik|hatalı|yanlış|dolmuş|sona ermiş|iptal edildi|uğradı|doğrulanamadı|erişilemiyor|ulaşılamadı|deneyin|kontrol edin|doldurun|seçin|girin|bekleyin|yenileyin|izin verilmiyor|yetkiniz yok|yetki yok|en az|en fazla|daha önce|zaten|olmalıdır|bırakılamaz|kilitli|kapalı|pasif|hazır değil|uygun değil|çakışıyor|aşıyor|olmalı|seçilmelidir|tanımlı değil|yürütülüyor|düzenlenebilir|kullanılamaz|oluşturulamaz|işlenemez|silinemez|kaydedilemez|başlatılamaz|kapatılabilir|genişletilemez|gönderilemez|kullanılıyor|tekrarıdır|tanımlanmış|boş|yapılabilir|bulunmalıdır|yok)/i;

export function errorMessage(value: unknown, fallback = DEFAULT_ERROR_MESSAGE): string {
  const error = value && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  const raw = typeof value === 'string' ? value : error.message;
  const text = typeof raw === 'string' ? raw.trim() : '';
  const code = typeof error.code === 'string' ? error.code : '';
  const name = typeof error.name === 'string' ? error.name : '';
  if (name === 'AbortError') return 'İstek iptal edildi.';
  if (name === 'TimeoutError' || /timed?\s*out|timeout/i.test(text)) return catalog.REQUEST_TIMEOUT;
  if (
    /failed to fetch|fetch failed|networkerror|network request failed|load failed|offline/i.test(
      text
    )
  )
    return catalog.NETWORK_ERROR;
  // Wrapped validation errors must not expose an internal prefix to the UI.
  const message = readableTerms(text.replace(/^(?:[A-Z][A-Z_0-9]+|validation failed):\s*/, ''));
  if (catalogMessages.has(message)) return message;
  if (
    !technicalMessage.test(message) &&
    !/\b[a-z]+(?:_[a-z0-9]+)+\b|\b[A-Z]+(?:_[A-Z]+)+\b/.test(message) &&
    turkishMessage.test(message)
  )
    return message;
  if (catalog[code]) return catalog[code];
  if (catalog[text]) return catalog[text];
  const status = typeof error.status === 'number' ? error.status : 0;
  if (status === 401) return catalog.UNAUTHENTICATED;
  if (status === 403) return catalog.FORBIDDEN;
  if (status === 404) return catalog.NOT_FOUND;
  if (status === 408 || status === 504) return catalog.REQUEST_TIMEOUT;
  if (status === 429) return 'Çok fazla istek gönderildi. Biraz bekleyip tekrar deneyin.';
  if (status === 502 || status === 503)
    return 'Sunucu şu anda kullanılamıyor. Biraz bekleyip tekrar deneyin.';
  return fallback;
}

/** Field identifiers remain unchanged for focusing inputs; only labels are localized. */
export function errorFieldLabel(field: string): string {
  return terms[field] ?? 'alan';
}
