/**
 * Uzun formlar için yerel kurtarma taslağı.
 *
 * Bu taslak resmî bir ERP kaydı değildir: stok hareketi, muhasebe kaydı veya
 * sunucudaki "taslak belge" üretmez. Yalnızca tarayıcı çöktüğünde, sekme
 * kapandığında veya sayfa yenilendiğinde kullanıcının yazdıklarını geri
 * getirmek içindir. Resmî kayıt her zaman normal "Kaydet" ile oluşur.
 */

/** Şema sürümü. Artırıldığında eski taslaklar kontrollü biçimde temizlenir. */
export const DRAFT_SCHEMA_VERSION = 1;

const PREFIX = `varyaone:draft:${DRAFT_SCHEMA_VERSION}:`;
/** Sürümü ne olursa olsun bu uygulamanın taslaklarını tanıyan önek. */
const ANY_VERSION_PREFIX = 'varyaone:draft:';
const TAB_STORAGE_KEY = 'varyaone:draft-tab';

/** Saklama süresi: 7 gün. */
export const DRAFT_MAX_AGE_MS = 7 * 24 * 60 * 60 * 1000;
/** Tek bir taslağın üst sınırı; kota hatasını baştan engeller. */
const MAX_DRAFT_BYTES = 512 * 1024;

export type DraftScope = {
  /** Kullanıcı kimliği. Farklı kullanıcı, başka taslak demektir. */
  userID: string;
  /** Şirket kimliği. Şirket değişiminde eski forma veri taşınmaz. */
  companyID: string;
  /** Form türü, örneğin 'sales-invoice' ya da 'stock-transfer'. */
  formType: string;
  /** Mevcut kaydın kimliği; yeni kayıtta boş bırakılır. */
  recordID?: string;
};

export type DraftEnvelope<T> = {
  schema: number;
  scope: Required<DraftScope>;
  /** Taslağı yazan sekme. Eşzamanlı sekmeler birbirinin taslağını ezmez. */
  tabID: string;
  savedAt: string;
  /**
   * Taslak alındığı andaki sunucu kaydının sürümü. Geri yüklemede sunucudaki
   * kayıt değişmişse sessizce üzerine yazılmaz.
   */
  recordVersion: string;
  data: T;
};

export type DraftWriteResult = { ok: true } | { ok: false; reason: 'storage' | 'too-large' };

function storage(kind: 'local' | 'session'): Storage | null {
  try {
    const store = kind === 'local' ? globalThis.localStorage : globalThis.sessionStorage;
    if (!store) return null;
    // Gizli sekmede erişim okuma anında da hata verebilir.
    store.getItem(TAB_STORAGE_KEY);
    return store;
  } catch {
    return null;
  }
}

function randomID(): string {
  const crypto = globalThis.crypto;
  if (crypto && 'randomUUID' in crypto) return crypto.randomUUID();
  return `${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

let cachedTabID = '';

/**
 * Sekme kimliği. sessionStorage'da durduğu için sayfa yenilemesinde aynı
 * kalır, ikinci bir sekmede ise farklıdır.
 */
export function tabID(): string {
  if (cachedTabID) return cachedTabID;
  const store = storage('session');
  const existing = store?.getItem(TAB_STORAGE_KEY);
  cachedTabID = existing || randomID();
  try {
    store?.setItem(TAB_STORAGE_KEY, cachedTabID);
  } catch {
    // Kimliği bellekte tutmak bu oturum için yeterli.
  }
  return cachedTabID;
}

function segment(value: string): string {
  return encodeURIComponent(value || '-');
}

function normalizeScope(scope: DraftScope): Required<DraftScope> {
  return {
    userID: scope.userID,
    companyID: scope.companyID,
    formType: scope.formType,
    recordID: scope.recordID || ''
  };
}

function scopePrefix(scope: DraftScope): string {
  const normalized = normalizeScope(scope);
  return `${PREFIX}${segment(normalized.userID)}:${segment(normalized.companyID)}:${segment(
    normalized.formType
  )}:${segment(normalized.recordID || 'new')}:`;
}

function draftKey(scope: DraftScope, tab = tabID()): string {
  return `${scopePrefix(scope)}${segment(tab)}`;
}

const SENSITIVE_KEY = /parola|password|sifre|şifre|otp|totp|secret|token|iban|tckn|kimlik_no/i;

/**
 * Parola/OTP, dosya içeriği ve benzeri hassas alanlar yerel taslağa alınmaz.
 * Çağıran taraf zaten yalnızca form modelini verir; bu ikinci bir güvenliktir.
 */
export function scrubSensitive(value: unknown): unknown {
  if (value === null || typeof value !== 'object') {
    // Gömülü dosya içeriği (data: URI) taslağa girmez.
    if (typeof value === 'string' && value.startsWith('data:')) return '';
    return value;
  }
  if (Array.isArray(value)) return value.map(scrubSensitive);
  if (value instanceof Date) return value.toISOString();
  // File/Blob gibi taşınamayan değerler atılır.
  if (typeof File !== 'undefined' && value instanceof File) return undefined;
  if (typeof Blob !== 'undefined' && value instanceof Blob) return undefined;
  const result: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    if (SENSITIVE_KEY.test(key)) continue;
    const scrubbed = scrubSensitive(item);
    if (scrubbed === undefined) continue;
    result[key] = scrubbed;
  }
  return result;
}

function parse<T>(raw: string | null): DraftEnvelope<T> | null {
  if (!raw) return null;
  try {
    const value = JSON.parse(raw) as DraftEnvelope<T>;
    if (!value || value.schema !== DRAFT_SCHEMA_VERSION) return null;
    if (!value.savedAt || !value.scope) return null;
    return value;
  } catch {
    return null;
  }
}

function expired(envelope: DraftEnvelope<unknown>, now: number): boolean {
  const savedAt = Date.parse(envelope.savedAt);
  if (Number.isNaN(savedAt)) return true;
  return now - savedAt > DRAFT_MAX_AGE_MS;
}

/** Taslağı yaz. Kota veya depolama hatası formu ve normal Kaydet'i etkilemez. */
export function saveDraft<T>(scope: DraftScope, data: T, recordVersion = ''): DraftWriteResult {
  const store = storage('local');
  if (!store) return { ok: false, reason: 'storage' };
  const envelope: DraftEnvelope<unknown> = {
    schema: DRAFT_SCHEMA_VERSION,
    scope: normalizeScope(scope),
    tabID: tabID(),
    savedAt: new Date().toISOString(),
    recordVersion,
    data: scrubSensitive(data)
  };
  let payload: string;
  try {
    payload = JSON.stringify(envelope);
  } catch {
    return { ok: false, reason: 'storage' };
  }
  if (payload.length > MAX_DRAFT_BYTES) return { ok: false, reason: 'too-large' };
  try {
    store.setItem(draftKey(scope), payload);
    return { ok: true };
  } catch {
    // Yer açmayı bir kez dene, sonra pes et: kullanıcı formu çalışmaya devam eder.
    sweepDrafts();
    try {
      store.setItem(draftKey(scope), payload);
      return { ok: true };
    } catch {
      return { ok: false, reason: 'storage' };
    }
  }
}

/**
 * Bu kapsamdaki en yeni taslak. Kendi sekmesinin taslağı da dahildir; yenileme
 * sonrası aynı sekme kendi yazdığını bulur.
 */
export function loadDraft<T>(scope: DraftScope, now = Date.now()): DraftEnvelope<T> | null {
  const store = storage('local');
  if (!store) return null;
  const prefix = scopePrefix(scope);
  let newest: DraftEnvelope<T> | null = null;
  for (const key of keysOf(store)) {
    if (!key.startsWith(prefix)) continue;
    const envelope = parse<T>(store.getItem(key));
    if (!envelope) {
      remove(store, key);
      continue;
    }
    if (expired(envelope, now)) {
      remove(store, key);
      continue;
    }
    if (!newest || envelope.savedAt > newest.savedAt) newest = envelope;
  }
  return newest;
}

/**
 * Tek bir taslağı sil: varsayılan olarak bu sekmenin yazdığını, `tab` verilirse
 * o sekmeninkini.
 *
 * Hangi sekmenin taslağının silindiği önemsiz bir ayrıntı değil: açılışta
 * bulunan taslak başka bir sekmenin olabilir ve kullanıcı "bu taslağı sil"
 * dediğinde kastettiği gördüğü taslaktır. Kapsamın tamamını silmek, o sırada
 * başka bir sekmede yazılmakta olan işi de götürür.
 */
export function clearDraft(scope: DraftScope, tab?: string): boolean {
  const store = storage('local');
  if (!store) return false;
  return remove(store, draftKey(scope, tab ?? tabID()));
}

/** Bu kapsamdaki tüm sekmelerin taslaklarını sil. */
export function clearDraftScope(scope: DraftScope): boolean {
  const store = storage('local');
  if (!store) return false;
  const prefix = scopePrefix(scope);
  let ok = true;
  for (const key of keysOf(store)) {
    if (key.startsWith(prefix) && !remove(store, key)) ok = false;
  }
  return ok;
}

/** Süresi dolmuş ve şeması uyumsuz taslakları kontrollü biçimde temizle. */
export function sweepDrafts(now = Date.now()): number {
  const store = storage('local');
  if (!store) return 0;
  let removed = 0;
  for (const key of keysOf(store)) {
    if (!key.startsWith(ANY_VERSION_PREFIX)) continue;
    if (!key.startsWith(PREFIX)) {
      // Eski şema sürümü: geri yüklenemez, bekletilmez.
      remove(store, key);
      removed += 1;
      continue;
    }
    const envelope = parse<unknown>(store.getItem(key));
    if (!envelope || expired(envelope, now)) {
      remove(store, key);
      removed += 1;
    }
  }
  return removed;
}

function keysOf(store: Storage): string[] {
  const keys: string[] = [];
  try {
    for (let index = 0; index < store.length; index += 1) {
      const key = store.key(index);
      if (key) keys.push(key);
    }
  } catch {
    return [];
  }
  return keys;
}

/**
 * Bir anahtarı sil. Dönüş değeri gerçekten silinip silinmediğidir.
 *
 * Silme başarısızlığı sessiz geçilmez: kullanıcı "taslağı sil" dediğinde
 * taslağın kalması, bir sonraki açılışta sildiğini sandığı verinin geri
 * teklif edilmesi demektir. Form çalışmaya devam eder, ama bunu söyleriz.
 */
function remove(store: Storage, key: string): boolean {
  try {
    store.removeItem(key);
    return store.getItem(key) === null;
  } catch {
    return false;
  }
}
