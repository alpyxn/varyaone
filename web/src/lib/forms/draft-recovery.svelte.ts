/**
 * Uzun formlarda yerel kurtarma taslağının yaşam döngüsü: düzenleme
 * durduktan kısa süre sonra yazma, açılışta bulma, geri yükleme ve silme.
 *
 * Bu taslak resmî bir ERP kaydı üretmez; sunucudaki "taslak belge" kaydından
 * ayrıdır ve onunla yarışmaz.
 */
import { untrack } from 'svelte';
import {
  clearDraft,
  loadDraft,
  saveDraft,
  sweepDrafts,
  type DraftEnvelope,
  type DraftScope
} from './draft-storage';

/** Düzenleme durduktan sonra taslağın yazılmasına kadar geçen süre. */
export const DRAFT_DEBOUNCE_MS = 1000;

export type DraftRecoveryOptions<T> = {
  /** Kayda giden form modelinden üretilen taslak verisi. */
  data: () => T;
  /** Kullanıcı, şirket, form türü ve kayıt kimliği. */
  scope: () => DraftScope | null;
  /** Sunucudaki kaydın sürümü; boş ise yeni kayıt demektir. */
  recordVersion?: () => string;
  /** Taslak yazılmaması gereken durumlar (salt görüntüleme, kayıt sürüyor). */
  enabled?: () => boolean;
};

export class DraftRecovery<T> {
  #options: DraftRecoveryOptions<T>;
  #timer: ReturnType<typeof setTimeout> | null = null;
  #armed = false;
  /**
   * Taslağın ait olduğu kapsam, yazıldığı andaki hâliyle.
   *
   * Silme buradan okunur, `scope()` çağrısından değil. Şirket değişimi
   * asenkrondur: "sil ve çık" ile gerçek geçiş arasında oturumun şirketi
   * değişebilir ve o anda `scope()` sormak, silinmesi istenen taslağı bırakıp
   * yeni şirketin taslağını silmek olurdu.
   */
  #written: DraftScope | null = null;

  /** Açılışta bulunan taslak; kullanıcı geri yükleyene ya da silene kadar durur. */
  found = $state<DraftEnvelope<T> | null>(null);
  /** Kota veya depolama hatası. Form ve normal Kaydet çalışmaya devam eder. */
  error = $state('');

  constructor(options: DraftRecoveryOptions<T>) {
    this.#options = options;
  }

  get enabled(): boolean {
    return (this.#options.enabled?.() ?? true) && Boolean(this.#options.scope());
  }

  /** Bulunan taslak sunucudaki kaydın başka bir sürümüne mi ait? */
  get staleVersion(): boolean {
    const draft = this.found;
    if (!draft) return false;
    const current = this.#options.recordVersion?.() ?? '';
    return Boolean(current) && draft.recordVersion !== current;
  }

  get savedAt(): Date | null {
    const value = this.found?.savedAt;
    if (!value) return null;
    const parsed = new Date(value);
    return Number.isNaN(parsed.getTime()) ? null : parsed;
  }

  /**
   * Kayıt yüklendikten sonra çağrılır: eski taslakları temizler ve bu kayıt
   * için bekleyen bir taslak varsa kullanıcıya sunar.
   */
  start(): void {
    const scope = this.#options.scope();
    if (!scope) return;
    sweepDrafts();
    this.found = loadDraft<T>(scope);
    this.#written = this.found ? this.found.scope : null;
    this.#armed = true;
  }

  /**
   * Bir düzenleme oldu. Yazma, düzenleme durduktan kısa süre sonra yapılır.
   * Geri yükleme kararı verilmeden yazmaya başlanmaz.
   */
  note(): void {
    if (!this.#armed || this.found || !this.enabled) return;
    this.#cancelTimer();
    this.#timer = setTimeout(() => {
      this.#timer = null;
      this.#write();
    }, DRAFT_DEBOUNCE_MS);
  }

  #write(): void {
    const scope = untrack(() => this.#options.scope());
    if (!scope || !untrack(() => this.enabled)) return;
    const result = saveDraft(
      scope,
      untrack(() => this.#options.data()),
      untrack(() => this.#options.recordVersion?.() ?? '')
    );
    if (result.ok) this.#written = scope;
    this.error = result.ok ? '' : 'Taslak kaydedilemedi.';
  }

  /**
   * "Taslağı geri yükle". Veriyi döndürür; uygulamak çağıranın işidir.
   *
   * Benimsenen taslak kendi anahtarından silinir: başka bir sekmenin taslağı
   * olabilir ve iki yerde birden yaşamaya devam etmesi, bir sonraki açılışta
   * aynı veriyi ikinci kez teklif etmek demektir.
   */
  restore(): T | null {
    const draft = this.found;
    if (!draft) return null;
    this.found = null;
    clearDraft(draft.scope, draft.tabID);
    this.#written = draft.scope;
    return draft.data;
  }

  /** Silme gerçekten olmadıysa bunu söyle; sessiz başarı bildirme. */
  #report(removed: boolean): void {
    this.error = removed ? '' : 'Taslak silinemedi; tarayıcı deposuna yazılamıyor.';
  }

  /**
   * "Taslağı sil" — kullanıcının gördüğü taslak. Yalnız o silinir; başka bir
   * sekme aynı kapsamda çalışmaya devam ediyorsa onun işi durur.
   */
  discard(): void {
    const draft = this.found;
    this.found = null;
    if (draft) this.#report(clearDraft(draft.scope, draft.tabID));
    else this.clear();
  }

  /**
   * Başarılı kayıt ya da "Değişiklikleri sil ve çık" sonrası bu sekmenin
   * taslağını sil. "Düzenlemeye devam et" taslağı korur.
   *
   * Bekleyen yazma önce iptal edilir: sırada duran bir debounce, silinen
   * taslağı bir saniye sonra geri yazardı.
   */
  clear(): void {
    this.#cancelTimer();
    const scope = this.#written ?? this.#options.scope();
    this.found = null;
    this.error = '';
    if (scope) this.#report(clearDraft(scope));
    this.#written = null;
  }

  /**
   * Sayfadan ayrılırken "sil ve çık": taslağı sil ve bir daha yazma.
   *
   * `clear()` tek başına yetmez — form modeli hâlâ doludur, bu yüzden bir
   * sonraki `note()` taslağı yeniden yazar. Ayrılan bir sayfada bunun
   * olmaması gerekir; kullanıcı silinmesini istedi.
   */
  discardForNavigation(): void {
    this.clear();
    this.#armed = false;
  }

  /** Bileşen sökülürken bekleyen yazmayı bırak. */
  destroy(): void {
    this.#cancelTimer();
    this.#armed = false;
  }

  #cancelTimer(): void {
    if (this.#timer === null) return;
    clearTimeout(this.#timer);
    this.#timer = null;
  }
}
