/**
 * Ortak "kaydedilmemiş değişiklik" denetimi.
 *
 * Bir veri giriş penceresi açıldığında, referansları ve varsayılanları
 * yükledikten sonra `capture()` ile temiz bir başlangıç alır. Kapanma
 * isteklerini (X, Esc, Vazgeç, üst bileşen) `requestClose()` üzerinden
 * geçirir; başlangıçtan farklı veri varsa kullanıcıya çıkış onayı gösterilir.
 *
 * Snapshot DOM'dan değil, kayda giden form modelinden üretilir.
 */

import { untrack } from 'svelte';

export type UnsavedCloseReason = 'clean' | 'discarded' | 'saved';

export type UnsavedChangesOptions = {
  /** Kayda giden form modelinden üretilen karşılaştırma değeri. */
  snapshot: () => unknown;
  /** Kayıt/gönderim sürüyorsa kapanma ve çift gönderim engellenir. */
  isBusy?: () => boolean;
  /** Onay alındıktan (ya da gerekmediği anlaşıldıktan) sonra gerçek kapatma. */
  onClose: (reason: UnsavedCloseReason) => void;
  /** Denetimin hiç çalışmaması gereken durumlar (salt görüntüleme gibi). */
  enabled?: () => boolean;
};

/** Tarih, sayı, boş değer ve satır listelerini tutarlı biçimde karşılaştır. */
function normalize(value: unknown): unknown {
  if (value === null || value === undefined) return null;
  if (typeof value === 'string') {
    const trimmed = value.trim();
    return trimmed === '' ? null : trimmed;
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) return null;
    // 1.50 ile 1.5 aynı sayıdır; string alanlara dokunulmaz.
    return `#${String(value)}`;
  }
  if (typeof value === 'boolean') return value;
  if (value instanceof Date) {
    const time = value.getTime();
    return Number.isNaN(time) ? null : `@${value.toISOString()}`;
  }
  if (Array.isArray(value)) return value.map(normalize);
  if (typeof value === 'object') {
    const source = value as Record<string, unknown>;
    const result: Record<string, unknown> = {};
    for (const key of Object.keys(source).sort()) {
      const normalized = normalize(source[key]);
      // Boş alan ile hiç olmayan alan aynı sayılır.
      if (normalized === null) continue;
      result[key] = normalized;
    }
    return result;
  }
  return null;
}

/** Form modelinden kararlı bir imza üret. */
export function formSignature(value: unknown): string {
  return JSON.stringify(normalize(value) ?? null);
}

export class UnsavedChangesGuard {
  #options: UnsavedChangesOptions;
  #baseline = $state<string | null>(null);
  /** Çıkış onayı penceresinin açık olup olmadığı. */
  confirmOpen = $state(false);
  #touched = false;
  #unregister: (() => void) | null = null;

  constructor(options: UnsavedChangesOptions) {
    this.#options = options;
  }

  /**
   * Düzenlenen alanlar bir alt bileşende yaşıyorsa, model ve meşguliyet
   * bilgisini oradan devral. Üst bileşen kapatma akışını yönetmeye devam eder.
   */
  attach(snapshot: () => unknown, isBusy?: () => boolean): void {
    this.#options = { ...this.#options, snapshot, isBusy: isBusy ?? this.#options.isBusy };
  }

  get busy(): boolean {
    return Boolean(this.#options.isBusy?.());
  }

  get enabled(): boolean {
    return this.#options.enabled?.() ?? true;
  }

  /** Başlangıç alınmadan önce hiçbir şey kirli sayılmaz. */
  get armed(): boolean {
    return this.#baseline !== null;
  }

  get isDirty(): boolean {
    if (!this.enabled) return false;
    const baseline = this.#baseline;
    if (baseline === null) return false;
    return baseline !== formSignature(this.#options.snapshot());
  }

  /**
   * Temiz durumu al. Referanslar ve başlangıç varsayılanları yüklendikten
   * sonra çağrılır; her açılışta ve kayıt/işlem türü değişiminde yenilenir.
   */
  capture(): void {
    // Temiz durumu almak, formun kendisini bir bağımlılık hâline getirmemeli;
    // aksi hâlde her tuş vuruşu yeni bir başlangıç alır ve form hiç kirlenmez.
    this.#baseline = untrack(() => formSignature(this.#options.snapshot()));
  }

  /**
   * Kullanıcı forma dokundu. Geç gelen referans/varsayılan istekleri bundan
   * sonra temiz durumu yeniden almaz.
   */
  noteUserInput(): void {
    this.#touched = true;
  }

  /**
   * Açılıştaki ve geç gelen varsayılanlardan sonraki temiz durum. Kullanıcı
   * bu arada bir şey yazdıysa yazdıkları temiz kabul edilmez.
   */
  captureInitial(): void {
    if (this.#touched) return;
    this.capture();
  }

  /** Yeni bir açılış/kayıt için sayacı sıfırla ve temiz durumu al. */
  reset(): void {
    this.#touched = false;
    this.confirmOpen = false;
    this.capture();
  }

  /** Denetimi tamamen bırak (pencere kapandı, form unmount edildi). */
  release(): void {
    this.#baseline = null;
    this.#touched = false;
    this.confirmOpen = false;
  }

  /** Mevcut veriyi yeni temiz durum kabul et (başarılı kayıt sonrası). */
  markClean(): void {
    if (this.#baseline === null) return;
    this.capture();
  }

  /**
   * X, Esc, Vazgeç ve üst bileşen kapatmalarının tek giriş noktası.
   * Kayıt sürerken hiçbir kapanma yolu çalışmaz.
   */
  requestClose(): void {
    if (this.busy) return;
    if (this.confirmOpen) return;
    if (!this.isDirty) {
      this.release();
      this.#options.onClose('clean');
      return;
    }
    this.confirmOpen = true;
  }

  /** "Düzenlemeye devam et" — veri ve odak korunur. */
  keepEditing(): void {
    this.confirmOpen = false;
  }

  /** "Değişiklikleri sil ve çık". */
  discardAndClose(): void {
    this.confirmOpen = false;
    this.release();
    this.#options.onClose('discarded');
  }

  /** Başarılı kayıt: uyarısız kapanılır. */
  closeAfterSave(): void {
    this.confirmOpen = false;
    this.release();
    this.#options.onClose('saved');
  }

  /**
   * Sayfadan ayrılma, şirket değişimi ve yenileme denetimine katıl.
   * Dönüş değeri kaydı geri alır; `$effect` içinde kullanılır.
   *
   * `onDiscard`, kullanıcı menüden/geri tuşundan/şirket değişiminden "sil ve
   * çık" dediğinde çalışır. Penceredeki "sil ve çık" ile aynı kararı verir ve
   * aynı işi yapmalıdır: pencerenin kendi kapanış yolu yerel taslağı siliyorsa
   * sayfadan ayrılma yolu da silmelidir, yoksa kullanıcının sildiğini sandığı
   * veri diskte kalır ve bir sonraki açılışta geri teklif edilir.
   *
   * Bu, pencerenin `onClose` akışı değildir: navigasyonda pencere kapatma yan
   * etkileri (odak geri verme, üst bileşeni kapatma) çalıştırılmaz.
   */
  registerPageGuard(label = 'Form', onDiscard?: () => void): () => void {
    this.#unregister?.();
    const unregister = registerUnsavedGuard({
      label,
      isDirty: () => this.isDirty,
      isBusy: () => this.busy,
      discard: () => {
        try {
          onDiscard?.();
        } finally {
          // Denetim her hâlükârda bırakılır: sayfanın kendi temizliği
          // başarısız olsa bile geçiş ikinci kez sorulmamalı.
          this.release();
        }
      }
    });
    this.#unregister = () => {
      unregister();
      this.#unregister = null;
    };
    return () => this.#unregister?.();
  }
}

/* ------------------------------------------------------------------ *
 * Sayfa seviyesinde kayıt: menü, geri tuşu, şirket değişimi, yenileme.
 * ------------------------------------------------------------------ */

export type UnsavedGuardEntry = {
  label: string;
  isDirty: () => boolean;
  /**
   * Kayıt/gönderim sürüyor mu?
   *
   * Kirlilikten ayrı bir sorudur. Süren bir kayıt sırasında sayfadan ayrılmak
   * "değişiklikleri sil" değildir: istek sunucuya gitmiştir ve sonucu
   * gelecektir. O sırada formu serbest bırakıp geçmek, kullanıcıya kaydın
   * iptal edildiğini düşündürür; oysa kayıt tamamlanmış olabilir.
   */
  isBusy?: () => boolean;
  /** Kullanıcı "sil ve çık" dediğinde formu kirli saymayı bırak. */
  discard: () => void;
};

const pageGuards = new Set<UnsavedGuardEntry>();

export function registerUnsavedGuard(entry: UnsavedGuardEntry): () => void {
  pageGuards.add(entry);
  return () => {
    pageGuards.delete(entry);
  };
}

/**
 * Süren bir kayıt var mı? Varsa geçiş hiç sorulmadan engellenir: kullanıcıya
 * silme kararı verdirmenin anlamı yok, çünkü silinecek bir şey değil, sonucu
 * beklenen bir istek var.
 */
export function busyGuardLabel(): string | null {
  for (const guard of pageGuards) {
    try {
      if (guard.isBusy?.()) return guard.label;
    } catch {
      // Sökülmekte olan bir bileşenin hatası geçişi kilitlemesin.
    }
  }
  return null;
}

export function hasUnsavedChanges(): boolean {
  for (const guard of pageGuards) {
    try {
      if (guard.isDirty()) return true;
    } catch {
      // Sökülmekte olan bir bileşenin hatası geçişi kilitlemesin.
    }
  }
  return false;
}

/**
 * "Değişiklikleri sil ve çık" sonrası kirli formları serbest bırak.
 *
 * Meşgul bir form atlanır. Aradaki fark bir yarış: kullanıcı kaydete bastıktan
 * sonra "sil ve çık" derse, süren isteği geri almanın yolu yoktur ve formu
 * silmek yalnız kullanıcının ne olduğunu bilmemesini sağlar. Çağıranlar zaten
 * busyGuardLabel() ile geçişi baştan engeller; bu ikinci savunmadır.
 */
export function discardUnsavedChanges(): void {
  for (const guard of [...pageGuards]) {
    try {
      if (guard.isBusy?.()) continue;
      guard.discard();
    } catch {
      // yoksay
    }
  }
}
