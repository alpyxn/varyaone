<script module lang="ts">
  import type { VariantDefinition, VariantOption } from '$lib/features/products/types';

  type ValidationSubject = 'definition' | 'option';

  export type VariantFieldErrors = Record<string, string>;

  type ErrorRecord = Record<string, unknown>;

  function asRecord(value: unknown): ErrorRecord | undefined {
    return typeof value === 'object' && value !== null ? (value as ErrorRecord) : undefined;
  }

  export function normalizeVariantCodeForValidation(value: string): string {
    const replacements: Record<string, string> = {
      İ: 'I',
      I: 'I',
      ı: 'I',
      Ş: 'S',
      Ğ: 'G',
      Ü: 'U',
      Ö: 'O',
      Ç: 'C'
    };
    let result = '';
    let separator = false;
    for (const character of value.trim().toUpperCase()) {
      const normalized = replacements[character] ?? character;
      if (/^[A-Z0-9]$/.test(normalized)) {
        result += normalized;
        separator = false;
      } else if ((normalized === '-' || normalized === '_') && result && !separator) {
        result += normalized;
        separator = true;
      }
    }
    return result.replace(/[-_]$/, '').slice(0, 100);
  }

  export function validateVariantDefinitionFields(
    code: string,
    name: string,
    definitions: VariantDefinition[],
    excludedID = ''
  ): VariantFieldErrors {
    const errors: VariantFieldErrors = {};
    const normalizedCode = normalizeVariantCodeForValidation(code);
    if (!code.trim()) errors.code = 'Boyut kodu zorunludur.';
    else if (!normalizedCode) errors.code = 'Kodda harf, rakam, tire veya alt çizgi kullanın.';
    else if (Array.from(code.trim()).length > 100) errors.code = 'Kod 100 karakterden uzun olamaz.';
    else if (
      definitions.some(
        (definition) =>
          definition.id !== excludedID &&
          normalizeVariantCodeForValidation(definition.code) === normalizedCode
      )
    ) {
      errors.code = 'Bu boyut kodu zaten kullanılıyor.';
    }
    if (!name.trim()) errors.name = 'Boyut adı zorunludur.';
    return errors;
  }

  export function validateVariantOptionFields(
    code: string,
    name: string,
    options: VariantOption[],
    excludedID = ''
  ): VariantFieldErrors {
    const errors: VariantFieldErrors = {};
    const normalizedCode = normalizeVariantCodeForValidation(code);
    if (!code.trim()) errors.code = 'Seçenek kodu zorunludur.';
    else if (!normalizedCode) errors.code = 'Kodda harf, rakam, tire veya alt çizgi kullanın.';
    else if (Array.from(code.trim()).length > 100) errors.code = 'Kod 100 karakterden uzun olamaz.';
    else if (
      options.some(
        (option) =>
          option.id !== excludedID &&
          normalizeVariantCodeForValidation(option.code) === normalizedCode
      )
    ) {
      errors.code = 'Bu seçenek kodu aynı boyutta zaten kullanılıyor.';
    }
    if (!name.trim()) errors.name = 'Seçenek adı zorunludur.';
    return errors;
  }

  export function extractVariantFieldErrors(cause: unknown): VariantFieldErrors {
    const error = asRecord(cause);
    const details = asRecord(error?.details);
    const source =
      asRecord(details?.field_errors) ?? asRecord(details?.fields) ?? details ?? undefined;
    if (!source) return {};

    const result: VariantFieldErrors = {};
    for (const [key, value] of Object.entries(source)) {
      const normalizedKey = key.toLowerCase().replace(/[^a-z]/g, '');
      const field = normalizedKey.includes('code')
        ? 'code'
        : normalizedKey.includes('name')
          ? 'name'
          : undefined;
      if (!field) continue;
      const detail = asRecord(value);
      const message =
        typeof value === 'string'
          ? value
          : typeof detail?.message === 'string'
            ? detail.message
            : undefined;
      if (message) result[field] = message;
    }
    return result;
  }

  export function readableVariantDefinitionError(
    cause: unknown,
    fallback: string,
    subject: ValidationSubject = 'definition'
  ): string {
    const error = asRecord(cause);
    const code = typeof error?.code === 'string' ? error.code : '';
    const message = typeof error?.message === 'string' ? error.message.trim() : '';
    if (code === 'FORBIDDEN' || code === 'PERMISSION_DENIED') return 'Bu işlem için yetkiniz yok.';
    if (code === 'CONFLICT' || code === 'VERSION_CONFLICT') {
      return 'Tanım başka bir kullanıcı tarafından değiştirildi. Sayfayı yenileyin.';
    }
    if (code === 'VALIDATION_ERROR' && /^varyant (tanımı|seçeneği) geçersiz\.?$/i.test(message)) {
      return subject === 'option'
        ? 'Seçenek bilgileri sunucu tarafından kabul edilmedi. Kod ve ad alanlarını kontrol edin.'
        : 'Tanım bilgileri sunucu tarafından kabul edilmedi. Kod ve ad alanlarını kontrol edin.';
    }
    return message || (cause instanceof Error ? cause.message : fallback);
  }

  export function variantErrorTraceID(cause: unknown): string {
    const error = asRecord(cause);
    return typeof error?.trace_id === 'string' ? error.trace_id : '';
  }
</script>

<script lang="ts">
  import { onMount } from 'svelte';
  import { ArrowLeft, Check, LockKeyhole, Plus, Save, X } from '@lucide/svelte';
  import { goto } from '$app/navigation';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { api, type Session } from '$lib/api';
  import {
    createVariantDefinition,
    createVariantOption,
    listVariantDefinitions,
    setVariantDefinitionActive,
    setVariantOptionActive,
    updateVariantDefinition,
    updateVariantOption
  } from '$lib/features/products/api';
  let session = $state<Session>();
  let definitions = $state<VariantDefinition[]>([]);
  let selectedID = $state('');
  let loading = $state(true);
  let saving = $state(false);
  let error = $state('');
  let errorTraceID = $state('');
  let message = $state('');
  let newDefinitionCode = $state('');
  let newDefinitionName = $state('');
  let editDefinitionCode = $state('');
  let editDefinitionName = $state('');
  let optionCode = $state('');
  let optionName = $state('');
  let optionDrafts = $state<Record<string, { code: string; name: string }>>({});
  let fieldErrors = $state<VariantFieldErrors>({});

  const selected = $derived(definitions.find((definition) => definition.id === selectedID));
  const activeOptionCount = $derived(
    selected?.options.filter((option) => option.is_active).length ?? 0
  );
  const canManage = $derived(
    Boolean(session?.permissions.includes('product.variant_definition.manage'))
  );

  function resetFeedback() {
    error = '';
    errorTraceID = '';
    message = '';
  }

  function clearFieldError(field: string) {
    if (!fieldErrors[field]) return;
    const next = { ...fieldErrors };
    delete next[field];
    fieldErrors = next;
  }

  function replaceFieldErrors(prefix: string, errors: VariantFieldErrors) {
    const next = Object.fromEntries(
      Object.entries(fieldErrors).filter(([field]) => !field.startsWith(`${prefix}-`))
    );
    for (const [field, value] of Object.entries(errors)) next[`${prefix}-${field}`] = value;
    fieldErrors = next;
  }

  function showValidation(prefix: string, errors: VariantFieldErrors) {
    replaceFieldErrors(prefix, errors);
    if (Object.keys(errors).length) {
      error = 'İşlemi tamamlamak için işaretli alanları düzeltin.';
      errorTraceID = '';
      message = '';
      return false;
    }
    return true;
  }

  function showServerError(
    cause: unknown,
    fallback: string,
    subject: 'definition' | 'option',
    prefix: string
  ) {
    error = readableVariantDefinitionError(cause, fallback, subject);
    errorTraceID = variantErrorTraceID(cause);
    const serverFields = extractVariantFieldErrors(cause);
    if (Object.keys(serverFields).length) replaceFieldErrors(prefix, serverFields);
    else if (asRecord(cause)?.code === 'VALIDATION_ERROR') {
      const inferred =
        subject === 'option'
          ? validateVariantOptionFields(optionCode, optionName, selected?.options ?? [])
          : validateVariantDefinitionFields(newDefinitionCode, newDefinitionName, definitions);
      if (Object.keys(inferred).length) replaceFieldErrors(prefix, inferred);
    }
  }

  function fieldError(prefix: string, field: string): string {
    return fieldErrors[`${prefix}-${field}`] ?? '';
  }

  function chooseDefinition(id: string) {
    selectedID = id;
    resetFeedback();
    replaceFieldErrors('edit-definition', {});
    const item = definitions.find((definition) => definition.id === id);
    if (!item) {
      editDefinitionCode = '';
      editDefinitionName = '';
      optionDrafts = {};
      return;
    }
    editDefinitionCode = item.code;
    editDefinitionName = item.name;
    optionDrafts = Object.fromEntries(
      item.options.map((option) => [option.id, { code: option.code, name: option.name }])
    );
  }

  async function load() {
    loading = true;
    error = '';
    try {
      session = await api<Session>('/session');
      const result = await listVariantDefinitions(true);
      definitions = result.items;
      if (!selectedID && definitions[0]) chooseDefinition(definitions[0].id);
      else if (selectedID && definitions.some((definition) => definition.id === selectedID))
        chooseDefinition(selectedID);
      else if (selectedID) chooseDefinition('');
    } catch (cause) {
      showServerError(cause, 'Varyant tanımları alınamadı.', 'definition', 'definition-list');
    } finally {
      loading = false;
    }
  }

  async function addDefinition() {
    if (!canManage || saving) return;
    const validation = validateVariantDefinitionFields(
      newDefinitionCode,
      newDefinitionName,
      definitions
    );
    if (!showValidation('new-definition', validation)) return;
    const code = normalizeVariantCodeForValidation(newDefinitionCode);
    const name = newDefinitionName.trim();
    saving = true;
    resetFeedback();
    try {
      const created = await createVariantDefinition({ code, name });
      definitions = [...definitions, { ...created, options: created.options ?? [] }];
      newDefinitionCode = '';
      newDefinitionName = '';
      chooseDefinition(created.id);
      message = 'Varyant boyutu oluşturuldu.';
    } catch (cause) {
      showServerError(cause, 'Varyant boyutu oluşturulamadı.', 'definition', 'new-definition');
    } finally {
      saving = false;
    }
  }

  async function saveDefinition() {
    if (!selected || !canManage || saving) return;
    const validation = validateVariantDefinitionFields(
      editDefinitionCode,
      editDefinitionName,
      definitions,
      selected.id
    );
    if (!showValidation('edit-definition', validation)) return;
    const code = normalizeVariantCodeForValidation(editDefinitionCode);
    const name = editDefinitionName.trim();
    saving = true;
    resetFeedback();
    try {
      const updated = await updateVariantDefinition(selected.id, selected.version, {
        code,
        name
      });
      definitions = definitions.map((item) =>
        item.id === updated.id ? { ...updated, options: updated.options ?? item.options } : item
      );
      chooseDefinition(updated.id);
      message = 'Varyant boyutu kaydedildi.';
    } catch (cause) {
      showServerError(cause, 'Varyant boyutu kaydedilemedi.', 'definition', 'edit-definition');
    } finally {
      saving = false;
    }
  }

  async function toggleDefinition(definition: VariantDefinition) {
    if (!canManage || saving) return;
    saving = true;
    resetFeedback();
    try {
      const updated = await setVariantDefinitionActive(
        definition.id,
        definition.version,
        !definition.is_active
      );
      definitions = definitions.map((item) =>
        item.id === updated.id ? { ...updated, options: updated.options ?? item.options } : item
      );
      chooseDefinition(definition.id);
      message = updated.is_active
        ? 'Varyant boyutu aktifleştirildi.'
        : 'Varyant boyutu pasifleştirildi.';
    } catch (cause) {
      showServerError(
        cause,
        'Varyant boyutunun durumu değiştirilemedi.',
        'definition',
        'edit-definition'
      );
    } finally {
      saving = false;
    }
  }

  async function addOption() {
    if (!selected || !canManage || saving) return;
    const validation = validateVariantOptionFields(optionCode, optionName, selected.options);
    if (!showValidation('new-option', validation)) return;
    const code = normalizeVariantCodeForValidation(optionCode);
    const name = optionName.trim();
    saving = true;
    resetFeedback();
    try {
      const created = await createVariantOption(
        selected.id,
        Object.assign(
          {
            code,
            name,
            sort_order: selected.options.length + 1
          },
          { short_code: code }
        )
      );
      definitions = definitions.map((item) =>
        item.id === selected.id ? { ...item, options: [...item.options, created] } : item
      );
      optionCode = '';
      optionName = '';
      chooseDefinition(selected.id);
      message = 'Seçenek oluşturuldu.';
    } catch (cause) {
      showServerError(cause, 'Seçenek oluşturulamadı.', 'option', 'new-option');
    } finally {
      saving = false;
    }
  }

  async function saveOption(option: VariantOption) {
    if (!selected || !canManage || saving) return;
    const draft = optionDrafts[option.id] ?? { code: option.code, name: option.name };
    const validation = validateVariantOptionFields(
      draft.code,
      draft.name,
      selected.options,
      option.id
    );
    if (!showValidation(`option-${option.id}`, validation)) return;
    const code = normalizeVariantCodeForValidation(draft.code);
    const name = draft.name.trim();
    saving = true;
    resetFeedback();
    const existingShortCode = (option as VariantOption & { short_code?: string }).short_code;
    try {
      const updated = await updateVariantOption(
        selected.id,
        option.id,
        option.version,
        Object.assign(
          {
            code,
            name,
            sort_order: option.sort_order
          },
          { short_code: existingShortCode || code }
        )
      );
      definitions = definitions.map((item) =>
        item.id === selected.id
          ? {
              ...item,
              options: item.options.map((current) =>
                current.id === updated.id ? updated : current
              )
            }
          : item
      );
      chooseDefinition(selected.id);
      message = 'Seçenek kaydedildi.';
    } catch (cause) {
      showServerError(cause, 'Seçenek kaydedilemedi.', 'option', `option-${option.id}`);
    } finally {
      saving = false;
    }
  }

  async function toggleOption(option: VariantOption) {
    if (!selected || !canManage || saving) return;
    saving = true;
    resetFeedback();
    try {
      const updated = await setVariantOptionActive(
        selected.id,
        option.id,
        option.version,
        !option.is_active
      );
      definitions = definitions.map((item) =>
        item.id === selected.id
          ? {
              ...item,
              options: item.options.map((current) =>
                current.id === updated.id ? updated : current
              )
            }
          : item
      );
      chooseDefinition(selected.id);
      message = updated.is_active ? 'Seçenek aktifleştirildi.' : 'Seçenek pasifleştirildi.';
    } catch (cause) {
      showServerError(cause, 'Seçeneğin durumu değiştirilemedi.', 'option', `option-${option.id}`);
    } finally {
      saving = false;
    }
  }

  onMount(() => {
    void load();
  });
</script>

<svelte:head><title>Varyant Tanımları · Varya One</title></svelte:head>

<div class="page-shell">
  <header class="page-header">
    <div>
      <a class="back" href="/ayarlar/tanimlar"><ArrowLeft size={14} />Tanımlar</a>
      <h1>Varyant tanımları</h1>
    </div>
    <Button variant="outline" onclick={() => goto('/ayarlar/tanimlar')}><X size={14} />Kapat</Button
    >
  </header>

  {#if error}<div class="notice error" role="alert">{error}</div>{/if}
  {#if message}<div class="notice success" role="status">{message}</div>{/if}

  {#if loading}
    <div class="panel loading" role="status">Varyant tanımları yükleniyor…</div>
  {:else if !session}
    <div class="panel empty-panel">
      <strong>Tanımlar açılamadı.</strong><span>{error || 'Oturum bilgisi alınamadı.'}</span>
    </div>
  {:else}
    {#if !canManage}<div class="permission-panel" role="alert">
        <LockKeyhole size={16} />
        <div>
          <strong>Salt okunur görünüm</strong><span
            >Bu sayfada yalnızca görüntüleme yetkiniz var.</span
          >
        </div>
      </div>{/if}
    <div class="definition-layout">
      <section class="panel definition-list" aria-labelledby="definition-list-heading">
        <div class="section-heading">
          <div>
            <h2 id="definition-list-heading">Özellikler</h2>
            <p>Örneğin Renk, Beden veya Numara.</p>
          </div>
          <span>{definitions.length} kayıt</span>
        </div>
        {#if definitions.length === 0}
          <div class="empty-panel">
            <strong>Henüz özellik yok.</strong><span>İlk özelliği aşağıdaki formdan ekleyin.</span>
          </div>
        {:else}
          <div class="definition-items" role="list">
            {#each definitions as definition}
              <button
                type="button"
                class:selected={definition.id === selectedID}
                class="definition-item"
                onclick={() => chooseDefinition(definition.id)}
              >
                <span
                  ><strong>{definition.name}</strong><small
                    >{definition.code} · {definition.options.length} seçenek</small
                  ></span
                >
                <span class:active={definition.is_active} class="item-status"
                  >{definition.is_active ? 'Aktif' : 'Pasif'}</span
                >
              </button>
            {/each}
          </div>
        {/if}
        <div class="new-definition-form">
          <h3>Yeni özellik ekle</h3>
          <label class:error-field={Boolean(fieldError('new-definition', 'code'))}
            ><span>Kod</span><Input
              bind:value={newDefinitionCode}
              disabled={!canManage || saving}
              aria-invalid={Boolean(fieldError('new-definition', 'code'))}
            />{#if fieldError('new-definition', 'code')}<small class="field-error"
                >{fieldError('new-definition', 'code')}</small
              >{/if}</label
          >
          <label class:error-field={Boolean(fieldError('new-definition', 'name'))}
            ><span>Ad</span><Input
              bind:value={newDefinitionName}
              disabled={!canManage || saving}
              aria-invalid={Boolean(fieldError('new-definition', 'name'))}
            />{#if fieldError('new-definition', 'name')}<small class="field-error"
                >{fieldError('new-definition', 'name')}</small
              >{/if}</label
          >
          <p class="form-hint">
            Kod kısa ve benzersiz olmalı; seçenekleri bu özelliğin altında tanımlayabilirsiniz.
          </p>
          <Button disabled={!canManage || saving} onclick={() => void addDefinition()}
            ><Plus size={14} />Özellik ekle</Button
          >
        </div>
      </section>

      <section class="panel definition-detail" aria-labelledby="definition-detail-heading">
        {#if !selected}
          <div class="empty-panel">
            <strong>Özellik seçin</strong><span
              >Seçenekleri görmek ve düzenlemek için listeden bir özellik seçin.</span
            >
          </div>
        {:else}
          <div class="section-heading">
            <div>
              <h2 id="definition-detail-heading">Özellik bilgileri</h2>
              <p>
                <strong class="selected-title">{selected.name}</strong> · {selected.code}
                <span class="detail-status"
                  >{selected.is_active ? 'Aktif özellik' : 'Pasif özellik'}</span
                >
              </p>
            </div>
            <Button
              variant="outline"
              disabled={!canManage || saving}
              onclick={() => void toggleDefinition(selected)}
              >{selected.is_active ? 'Pasifleştir' : 'Aktifleştir'}</Button
            >
          </div>
          <div class="edit-definition-form">
            <label
              ><span>Özellik kodu</span><Input
                value={selected.code}
                readonly
                disabled={saving}
                aria-describedby="definition-code-hint"
              /></label
            >
            <label class:error-field={Boolean(fieldError('edit-definition', 'name'))}
              ><span>Özellik adı</span><Input
                bind:value={editDefinitionName}
                disabled={!canManage || saving}
                aria-invalid={Boolean(fieldError('edit-definition', 'name'))}
              />{#if fieldError('edit-definition', 'name')}<small class="field-error"
                  >{fieldError('edit-definition', 'name')}</small
                >{/if}</label
            >
            <p id="definition-code-hint" class="form-hint wide">
              Özellik kodu kimlik niteliğindedir ve değiştirilemez. Özellik adını
              güncelleyebilirsiniz.
            </p>
            <Button
              variant="outline"
              disabled={!canManage || saving}
              onclick={() => void saveDefinition()}><Save size={14} />Özelliği kaydet</Button
            >
          </div>

          <div class="options-heading">
            <div>
              <h3>Seçenekler</h3>
            </div>
          </div>
          {#if selected.options.length === 0}<div class="empty-panel">
              <strong>Seçenek yok.</strong><span>İlk seçeneği aşağıdaki formdan ekleyin.</span>
            </div>{/if}
          {#if selected.options.length > 0}
            <div class="option-columns" aria-hidden="true">
              <span>Kod</span><span>Seçenek adı</span><span>Durum</span><span>Kaydet</span><span
                >Durum işlemi</span
              >
            </div>
          {/if}
          <div class="option-list">
            {#each selected.options as option}
              {@const draft = optionDrafts[option.id] ?? { code: option.code, name: option.name }}
              <div class="option-row">
                <div class="option-input">
                  <Input
                    value={draft.code}
                    disabled={!canManage || saving}
                    aria-label={`${option.name} kodu`}
                    oninput={(event) =>
                      (optionDrafts = {
                        ...optionDrafts,
                        [option.id]: { ...draft, code: event.currentTarget.value }
                      })}
                  />
                  {#if fieldError(`option-${option.id}`, 'code')}<small class="field-error"
                      >{fieldError(`option-${option.id}`, 'code')}</small
                    >{/if}
                </div>
                <div class="option-input">
                  <Input
                    value={draft.name}
                    disabled={!canManage || saving}
                    aria-label={`${option.name} adı`}
                    oninput={(event) =>
                      (optionDrafts = {
                        ...optionDrafts,
                        [option.id]: { ...draft, name: event.currentTarget.value }
                      })}
                  />
                  {#if fieldError(`option-${option.id}`, 'name')}<small class="field-error"
                      >{fieldError(`option-${option.id}`, 'name')}</small
                    >{/if}
                </div>
                <span class:active={option.is_active} class="item-status"
                  >{option.is_active ? 'Aktif' : 'Pasif'}</span
                >
                <Button
                  size="icon"
                  variant="ghost"
                  aria-label="Seçeneği kaydet"
                  disabled={!canManage || saving}
                  onclick={() => void saveOption(option)}><Check size={14} /></Button
                >
                <Button
                  size="sm"
                  variant="outline"
                  disabled={!canManage || saving}
                  onclick={() => void toggleOption(option)}
                  >{option.is_active ? 'Pasifleştir' : 'Aktifleştir'}</Button
                >
              </div>
            {/each}
          </div>
          <div class="new-option-form">
            <div class="option-input">
              <Input
                bind:value={optionCode}
                disabled={!canManage || saving}
                aria-label="Yeni seçenek kodu"
              />
              {#if fieldError('new-option', 'code')}<small class="field-error"
                  >{fieldError('new-option', 'code')}</small
                >{/if}
            </div>
            <div class="option-input">
              <Input
                bind:value={optionName}
                disabled={!canManage || saving}
                aria-label="Yeni seçenek adı"
              />
              {#if fieldError('new-option', 'name')}<small class="field-error"
                  >{fieldError('new-option', 'name')}</small
                >{/if}
            </div>
            <Button disabled={!canManage || saving} onclick={() => void addOption()}
              ><Plus size={14} />Seçenek ekle</Button
            >
          </div>
        {/if}
      </section>
    </div>
  {/if}
</div>

<style>
  .page-shell {
    display: grid;
    gap: 1rem;
  }
  .page-header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 1rem;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    margin-bottom: 8px;
    color: var(--primary);
    font-size: 11px;
    text-decoration: none;
  }
  h1 {
    margin: 4px 0 0;
    color: var(--foreground);
    font-size: clamp(1.5rem, 3vw, 2rem);
  }
  h2,
  h3 {
    margin: 0;
    font-size: 14px;
  }
  h3 {
    font-size: 12px;
  }
  p {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 11px;
  }
  .notice {
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    font-size: 12px;
  }
  .notice.error {
    border-color: var(--danger);
    color: var(--danger);
  }
  .notice.success {
    border-color: color-mix(in srgb, var(--success, #15803d) 45%, var(--border));
    color: var(--success, #15803d);
  }
  .page-tip,
  .form-hint {
    color: var(--text-muted);
    font-size: 11px;
  }
  .page-tip {
    margin-top: 8px;
  }
  .selected-title {
    color: var(--text);
    font-weight: 700;
  }
  .detail-status {
    margin-left: 6px;
    color: var(--text-muted);
  }
  .field-error {
    color: var(--danger);
    font-size: 10px;
  }
  .error-field > :global(input) {
    border-color: var(--danger);
  }
  .permission-panel {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 10px 12px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    color: var(--text-muted);
    font-size: 11px;
  }
  .permission-panel div {
    display: grid;
    gap: 2px;
  }
  .definition-layout {
    display: grid;
    grid-template-columns: minmax(240px, 0.7fr) minmax(0, 1.5fr);
    gap: 1rem;
    align-items: start;
  }
  .panel {
    padding: 16px;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 12px;
  }
  .section-heading > span {
    color: var(--text-muted);
    font-size: 10px;
  }
  .definition-items {
    display: grid;
    gap: 5px;
    margin-top: 14px;
  }
  .definition-item {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    width: 100%;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    padding: 10px;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  .definition-item:hover,
  .definition-item.selected {
    border-color: var(--primary);
    background: var(--surface-muted);
  }
  .definition-item > span:first-child {
    display: grid;
    gap: 3px;
  }
  .definition-item small {
    color: var(--text-muted);
    font-size: 10px;
  }
  .item-status {
    color: var(--text-muted);
    font-size: 10px;
    white-space: nowrap;
  }
  .item-status.active {
    color: var(--success, #15803d);
  }
  .new-definition-form,
  .edit-definition-form,
  .new-option-form {
    display: grid;
    gap: 8px;
    margin-top: 16px;
    padding-top: 14px;
    border-top: 1px solid var(--border);
  }
  .option-input {
    display: grid;
    gap: 3px;
    min-width: 0;
  }
  label {
    display: grid;
    gap: 4px;
  }
  label > span {
    color: var(--text-muted);
    font-size: 10px;
  }
  .wide {
    grid-column: 1 / -1;
  }
  .edit-definition-form {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .edit-definition-form :global(button) {
    justify-self: start;
  }
  .edit-definition-form :global(input[readonly]) {
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .options-heading {
    margin-top: 24px;
  }
  .option-list {
    display: grid;
    gap: 6px;
    margin-top: 10px;
  }
  .option-columns {
    display: grid;
    grid-template-columns: minmax(100px, 0.65fr) minmax(150px, 1fr) auto auto auto;
    gap: 6px;
    margin-top: 14px;
    padding: 0 6px;
    color: var(--text-muted);
    font-size: 10px;
    font-weight: 700;
  }
  .option-columns span:nth-child(4),
  .option-columns span:nth-child(5) {
    text-align: center;
  }
  .option-row {
    display: grid;
    grid-template-columns: minmax(100px, 0.65fr) minmax(150px, 1fr) auto auto auto;
    gap: 6px;
    align-items: center;
  }
  .new-option-form {
    grid-template-columns: minmax(100px, 0.65fr) minmax(150px, 1fr) auto;
  }
  .empty-panel {
    display: grid;
    gap: 3px;
    margin-top: 14px;
    padding: 14px;
    border: 1px dashed var(--border);
    border-radius: var(--radius-control);
    color: var(--text-muted);
    font-size: 11px;
  }
  .empty-panel strong {
    color: var(--text);
  }
  .loading {
    color: var(--text-muted);
  }
  @media (max-width: 850px) {
    .definition-layout {
      grid-template-columns: 1fr;
    }
  }
  @media (max-width: 650px) {
    .page-header {
      flex-direction: column;
    }
    .edit-definition-form,
    .option-row,
    .new-option-form,
    .option-columns {
      grid-template-columns: 1fr;
    }
    .option-columns {
      display: none;
    }
  }
</style>
