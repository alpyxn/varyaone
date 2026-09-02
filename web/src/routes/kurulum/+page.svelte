<script lang="ts">
  import {
    Check,
    Layers3,
    PackageOpen,
    TriangleAlert,
    LayoutGrid,
    ArrowLeft,
    ArrowRight,
    Rocket
  } from '@lucide/svelte';
  import { goto } from '$app/navigation';
  import { api, type APIError, type Session } from '$lib/api';
  import { MODULE_CATALOG, type ModuleCode } from '$lib/modules';
  import AuthShell from '$lib/components/varya/auth/AuthShell.svelte';
  import {
    buildSetupPayload,
    packageConflicts,
    selectedPackageDefinitions,
    SETUP_SECTOR_PACKAGES,
    type SectorPackageId
  } from './setup-options';

  let adminName = $state('');
  let adminEmail = $state('');
  let password = $state('');
  let legalName = $state('');
  let tradeName = $state('');
  let entityType = $state<'LEGAL_ENTITY' | 'SOLE_PROPRIETOR'>('LEGAL_ENTITY');
  let taxNumber = $state('');
  let busy = $state(false);
  let message = $state('');
  let step = $state(1);
  let selectedModuleIDs = $state<ModuleCode[]>(['preaccounting', 'hr', 'fixed_asset']);
  let selectedPackageIDs = $state<SectorPackageId[]>(['GENEL']);

  const preAccountingOn = $derived(selectedModuleIDs.includes('preaccounting'));
  const selectedDefinitions = $derived(selectedPackageDefinitions(selectedPackageIDs));
  const packageConflictsFound = $derived(packageConflicts(selectedPackageIDs));
  // Step 4 (variant packages) only applies when Ön Muhasebe is enabled.
  const steps = $derived(preAccountingOn ? [1, 2, 3, 4, 5] : [1, 2, 3, 5]);
  const stepIndex = $derived(steps.indexOf(step));
  const isLastStep = $derived(stepIndex === steps.length - 1);

  const STEP_TITLES: Record<number, string> = {
    1: 'Yönetici',
    2: 'Şirket',
    3: 'Modüller',
    4: 'Varyant paketleri',
    5: 'Özet'
  };

  function validateStep(current: number): string {
    if (current === 1) {
      if (!adminName.trim() || !adminEmail.trim() || password.length < 12) {
        return 'Ad soyad, e-posta ve en az 12 karakterlik parola gereklidir.';
      }
    }
    if (current === 2 && (!legalName.trim() || !tradeName.trim())) {
      return 'Resmî unvan ve ticari ad gereklidir.';
    }
    if (current === 3 && selectedModuleIDs.length === 0) {
      return 'En az bir modül seçin.';
    }
    if (current === 4) {
      if (selectedPackageIDs.length === 0) return 'En az bir başlangıç paketi seçin.';
      if (packageConflictsFound.length > 0) return 'Seçilen paketlerde tanım çakışması var.';
    }
    return '';
  }

  function next() {
    const error = validateStep(step);
    if (error) {
      message = error;
      return;
    }
    message = '';
    step = steps[Math.min(stepIndex + 1, steps.length - 1)];
  }

  function back() {
    message = '';
    step = steps[Math.max(stepIndex - 1, 0)];
  }

  function toggleModule(id: ModuleCode) {
    selectedModuleIDs = selectedModuleIDs.includes(id)
      ? selectedModuleIDs.filter((item) => item !== id)
      : [...selectedModuleIDs, id];
  }

  function togglePackage(id: SectorPackageId) {
    selectedPackageIDs = selectedPackageIDs.includes(id)
      ? selectedPackageIDs.filter((item) => item !== id)
      : [...selectedPackageIDs, id];
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    for (const current of steps) {
      const error = validateStep(current);
      if (error) {
        message = error;
        step = current;
        return;
      }
    }
    busy = true;
    message = '';
    try {
      const payload = buildSetupPayload(
        {
          admin_name: adminName,
          admin_email: adminEmail,
          password,
          legal_name: legalName,
          trade_name: tradeName,
          entity_type: entityType,
          tax_number: taxNumber || undefined
        },
        preAccountingOn ? selectedPackageIDs : [],
        selectedModuleIDs
      );
      await api<Session>('/setup', { method: 'POST', body: JSON.stringify(payload) });
      await goto('/');
      location.reload();
    } catch (error) {
      message = (error as APIError).message || 'Kurulum tamamlanamadı.';
    } finally {
      busy = false;
    }
  }
</script>

<svelte:head><title>Kurulum · Varya One</title></svelte:head>

<AuthShell
  wide
  tagline="Birkaç dakikada kullanıma hazır"
  highlights={[
    'Yönetici hesabı ve şirket kaydı',
    'Merkez şube ve Ana Depo otomatik oluşur',
    'İhtiyacınız olan modülleri seçin',
    'Her ayarı sonradan değiştirebilirsiniz'
  ]}
>
  <div class="auth-view">
    <span class="auth-eyebrow"><Rocket size={14} aria-hidden="true" /> Bir defalık ilk kurulum</span
    >
    <h1>Varya One’ı kullanıma hazırlayın</h1>

    <ol class="stepper" aria-label="Kurulum adımları">
      {#each steps as value, index}
        <li class:active={value === step} class:done={stepIndex > index}>
          <span class="dot"
            >{#if stepIndex > index}<Check size={12} />{:else}{index + 1}{/if}</span
          >
          <span class="step-label">{STEP_TITLES[value]}</span>
        </li>
      {/each}
    </ol>

    {#if message}<div class="auth-alert" role="alert">{message}</div>{/if}

    <form class="auth-form wizard" aria-label="İlk kurulum formu" onsubmit={submit}>
      {#if step === 1}
        <div class="form-section">
          <h2>İlk yönetici</h2>
          <label class="field"
            >Ad soyad<input bind:value={adminName} required autocomplete="name" /></label
          >
          <label class="field"
            >E-posta<input
              bind:value={adminEmail}
              type="email"
              required
              autocomplete="email"
            /></label
          >
          <label class="field">
            Parola <span class="hint">En az 12 karakter</span>
            <input
              bind:value={password}
              type="password"
              minlength="12"
              required
              autocomplete="new-password"
            />
          </label>
        </div>
      {:else if step === 2}
        <div class="form-section">
          <h2>Şirket</h2>
          <label class="field">Resmî unvan<input bind:value={legalName} required /></label>
          <label class="field">Ticari ad<input bind:value={tradeName} required /></label>
          <label class="field">
            Şirket türü
            <select bind:value={entityType}>
              <option value="LEGAL_ENTITY">Tüzel kişi</option>
              <option value="SOLE_PROPRIETOR">Şahıs işletmesi</option>
            </select>
          </label>
          <label class="field">
            Vergi / T.C. kimlik no <span class="hint">(isteğe bağlı)</span>
            <input bind:value={taxNumber} maxlength="11" inputmode="numeric" />
          </label>
        </div>
      {:else if step === 3}
        <div class="form-section variant-package-section">
          <div class="section-heading">
            <div>
              <h2>Modüller</h2>
              <p>Bu firmada kullanacağınız modülleri seçin. Sonradan aç/kapa yapabilirsiniz.</p>
            </div>
            <LayoutGrid size={20} aria-hidden="true" />
          </div>
          <div class="package-grid" aria-label="Modüller">
            {#each MODULE_CATALOG as item}
              {@const selected = selectedModuleIDs.includes(item.code)}
              <label class="package-card" class:selected>
                <input
                  type="checkbox"
                  checked={selected}
                  onchange={() => toggleModule(item.code)}
                  aria-describedby={`module-${item.code}-description`}
                />
                <span class="package-card-content">
                  <span class="package-card-title">
                    <strong>{item.name}</strong>
                    {#if selected}<Check size={15} aria-label="Seçildi" />{/if}
                  </span>
                  <span id={`module-${item.code}-description`} class="package-description"
                    >{item.description}</span
                  >
                </span>
              </label>
            {/each}
          </div>
          {#if selectedModuleIDs.length === 0}
            <p class="auth-alert" role="alert">Devam etmek için en az bir modül seçin.</p>
          {/if}
        </div>
      {:else if step === 4}
        <div class="form-section variant-package-section">
          <div class="section-heading">
            <div>
              <h2>Başlangıç varyant paketleri</h2>
              <p>
                İhtiyacınız olan sektörleri seçin. Tanımlar ve hazır seçenekler kurulumda bir kez
                eklenir.
              </p>
            </div>
            <PackageOpen size={20} aria-hidden="true" />
          </div>
          <div class="package-grid" aria-label="Başlangıç varyant paketleri">
            {#each SETUP_SECTOR_PACKAGES as item}
              {@const selected = selectedPackageIDs.includes(item.id)}
              <label class="package-card" class:selected>
                <input
                  type="checkbox"
                  checked={selected}
                  onchange={() => togglePackage(item.id)}
                  aria-describedby={`package-${item.id}-description`}
                />
                <span class="package-card-content">
                  <span class="package-card-title">
                    <strong>{item.name}</strong>
                    {#if selected}<Check size={15} aria-label="Seçildi" />{/if}
                  </span>
                  <span id={`package-${item.id}-description`} class="package-description"
                    >{item.description}</span
                  >
                  <span class="package-meta">{item.definitions.length} varyant tanımı</span>
                </span>
              </label>
            {/each}
          </div>
          {#if packageConflictsFound.length > 0}
            <div class="package-warning" role="alert">
              <TriangleAlert size={16} aria-hidden="true" />
              <div>
                <strong>Tanım çakışması</strong>
                {#each packageConflictsFound as conflict}<span>{conflict}</span>{/each}
              </div>
            </div>
          {:else}
            <div class="package-preview" aria-live="polite">
              <Layers3 size={16} aria-hidden="true" />
              <div>
                <strong>Kurulum önizlemesi</strong>
                <span>
                  {selectedDefinitions.length} tanım ve {selectedDefinitions.reduce(
                    (total, definition) => total + definition.options.length,
                    0
                  )} seçenek şirkete eklenecek.
                </span>
              </div>
            </div>
          {/if}
        </div>
      {:else}
        <div class="form-section">
          <h2>Özet</h2>
          <dl class="summary">
            <div>
              <dt>Yönetici</dt>
              <dd>{adminName} · {adminEmail}</dd>
            </div>
            <div>
              <dt>Şirket</dt>
              <dd>{legalName} ({tradeName})</dd>
            </div>
            <div>
              <dt>Modüller</dt>
              <dd>
                {selectedModuleIDs
                  .map((id) => MODULE_CATALOG.find((m) => m.code === id)?.name ?? id)
                  .join(', ') || '—'}
              </dd>
            </div>
            {#if preAccountingOn}
              <div>
                <dt>Varyant paketleri</dt>
                <dd>{selectedPackageIDs.join(', ') || '—'}</dd>
              </div>
            {/if}
          </dl>
        </div>
      {/if}

      <div class="wizard-actions">
        {#if stepIndex > 0}
          <button class="auth-submit ghost" type="button" onclick={back}>
            <ArrowLeft size={15} /> Geri
          </button>
        {:else}
          <span></span>
        {/if}
        {#if isLastStep}
          <button class="auth-submit" type="submit" disabled={busy}>
            {busy ? 'Kuruluyor…' : 'Kurulumu tamamla'}
          </button>
        {:else}
          <button class="auth-submit" type="button" onclick={next}>
            İleri <ArrowRight size={15} />
          </button>
        {/if}
      </div>
    </form>
  </div>
</AuthShell>

<style>
  .stepper {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 16px;
    margin: 0 0 18px;
    padding: 0;
    list-style: none;
  }
  .stepper li {
    display: flex;
    align-items: center;
    gap: 7px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .stepper .dot {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 22px;
    height: 22px;
    border-radius: 50%;
    border: 1px solid var(--border-strong);
    font-size: 11px;
    font-weight: 650;
  }
  .stepper li.active {
    color: var(--text);
    font-weight: 650;
  }
  .stepper li.active .dot,
  .stepper li.done .dot {
    border-color: #c1272d;
    background: color-mix(in srgb, #c1272d 12%, var(--surface));
    color: #c1272d;
  }
  .stepper li.done .dot {
    background: #c1272d;
    color: #fff;
  }

  .wizard {
    gap: 18px;
  }
  .wizard-actions {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-top: 2px;
  }

  .summary {
    display: grid;
    gap: 10px;
    margin: 0;
  }
  .summary div {
    display: grid;
    grid-template-columns: 150px 1fr;
    gap: 10px;
    font-size: 13px;
  }
  .summary dt {
    color: var(--text-muted);
  }
  .summary dd {
    margin: 0;
  }

  .variant-package-section {
    display: grid;
    gap: 13px;
  }
  .section-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
  }
  .section-heading :global(svg) {
    flex: 0 0 auto;
    color: #c1272d;
  }
  .section-heading h2 {
    margin: 0;
  }
  .section-heading p {
    max-width: 580px;
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
    line-height: 1.5;
  }
  .package-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: 10px;
  }
  .package-card {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    min-height: 116px;
    padding: 12px;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--surface);
    cursor: pointer;
    transition:
      border-color 120ms ease,
      background 120ms ease;
  }
  .package-card:hover,
  .package-card.selected {
    border-color: #c1272d;
    background: color-mix(in srgb, #c1272d 5%, var(--surface));
  }
  .package-card input {
    flex: 0 0 auto;
    width: 16px;
    height: 16px;
    margin-top: 2px;
    accent-color: #c1272d;
  }
  .package-card-content {
    display: grid;
    gap: 5px;
  }
  .package-card-title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    color: var(--text);
    font-size: 13px;
  }
  .package-card-title :global(svg) {
    flex: 0 0 auto;
    color: #c1272d;
  }
  .package-description,
  .package-meta,
  .package-preview span,
  .package-warning span {
    color: var(--text-muted);
    font-size: 11px;
    line-height: 1.45;
  }
  .package-meta {
    color: #c1272d;
    font-weight: 650;
  }
  .package-preview,
  .package-warning {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 10px 12px;
    border-radius: 10px;
    background: var(--surface-muted);
  }
  .package-preview :global(svg) {
    flex: 0 0 auto;
    margin-top: 1px;
    color: #c1272d;
  }
  .package-warning {
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    background: color-mix(in srgb, var(--danger) 6%, var(--surface));
    color: var(--danger);
  }
  .package-warning :global(svg) {
    flex: 0 0 auto;
    margin-top: 1px;
  }
  .package-preview div,
  .package-warning div {
    display: grid;
    gap: 2px;
  }
</style>
