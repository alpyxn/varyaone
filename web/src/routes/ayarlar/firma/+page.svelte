<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import { api, type APIError, type Company, type Session } from '$lib/api';
  import { FileDrop } from '$lib/components/varya/file-drop';
  import { resetPrintableCompany } from '$lib/features/settings/company-profile';
  import { ConfirmDialog } from '$lib/components/varya/confirm-dialog';
  import DemoResetAction from '$lib/components/varya/demo/DemoResetAction.svelte';

  let session = $state<Session | null>(null);
  let company = $state<Company | null>(null);
  let message = $state('');
  let messageTone = $state<'success' | 'error'>('success');
  let logoError = $state('');
  let deleteOpen = $state(false);
  let deleteConfirmName = $state('');
  const canEdit = $derived(Boolean(session?.permissions.includes('organization.company.edit')));
  // Only the instance owner may delete a company, and never the last one they can reach.
  const canDelete = $derived(
    Boolean(session?.is_instance_owner) && (session?.companies.length ?? 0) > 1
  );

  async function confirmDelete() {
    if (!company) return;
    const typed = deleteConfirmName.trim();
    if (typed.toLocaleLowerCase('tr') !== company.trade_name.trim().toLocaleLowerCase('tr')) {
      throw new Error('Yazdığınız ad firmanın ticari adıyla eşleşmiyor.');
    }
    await api(`/companies/${company.id}`, {
      method: 'DELETE',
      body: JSON.stringify({ confirm_name: typed })
    });
    location.href = '/';
  }

  async function onLogoPick(files: File[]) {
    logoError = '';
    const file = files[0];
    if (!file || !company) return;
    company.logo = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
  }

  onMount(async () => {
    try {
      session = await api<Session>('/session');
      company = await api<Company>('/company');
    } catch {
      await goto('/giris');
    }
  });

  async function save(event: SubmitEvent) {
    event.preventDefault();
    if (!company) return;
    try {
      company = await api<Company>('/company', {
        method: 'PUT',
        headers: { 'If-Match': `"${company.version}"` },
        body: JSON.stringify({
          legal_name: company.legal_name,
          trade_name: company.trade_name,
          entity_type: company.entity_type,
          tax_number: company.tax_number,
          base_currency: company.base_currency,
          timezone: company.timezone,
          logo: company.logo ?? ''
        })
      });
      // Printed documents cache the profile for their header logo.
      resetPrintableCompany();
      message = 'Şirket ayarları kaydedildi.';
      messageTone = 'success';
    } catch (error) {
      message = (error as APIError).message;
      messageTone = 'error';
    }
  }
</script>

<svelte:head><title>Şirket Bilgileri · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Şirket bilgileri</h1>
  </div>
</header>
{#if message}<div class="notice {messageTone}" role={messageTone === 'error' ? 'alert' : 'status'}>
    {message}
  </div>{/if}
{#if company}
  <form class="firma-layout" onsubmit={save}>
    <section class="card">
      <h2 class="panel-title">Resmî bilgiler</h2>
      <div class="form-grid">
        <label class="field">Resmî unvan<input bind:value={company.legal_name} required /></label>
        <label class="field">Ticari ad<input bind:value={company.trade_name} required /></label>
        <label class="field"
          >Şirket türü<select bind:value={company.entity_type}
            ><option value="LEGAL_ENTITY">Tüzel kişi</option><option value="SOLE_PROPRIETOR"
              >Şahıs işletmesi</option
            ></select
          ></label
        >
        <label class="field"
          >Vergi / T.C. kimlik no<input bind:value={company.tax_number} maxlength="11" /></label
        >
      </div>
    </section>

    <section class="card logo-card">
      <h2 class="panel-title">Logo</h2>
      <div class="logo-body">
        {#if company.logo}
          <img class="logo-preview" src={company.logo} alt="Şirket logosu" />
        {:else}
          <div class="logo-preview empty">Logo yok</div>
        {/if}
        <div class="logo-actions">
          <FileDrop
            variant="photo"
            accept="image/png,image/jpeg,image/webp"
            maxSizeKB={400}
            label="Logoyu buraya bırakın"
            hint="PNG/JPG/WebP · en fazla 400 KB"
            ariaLabel="Logo seç"
            onFilesChange={onLogoPick}
          />
          {#if company.logo}
            <button
              type="button"
              class="button secondary"
              onclick={() => company && (company.logo = '')}>Logoyu kaldır</button
            >
          {/if}
        </div>
      </div>
      {#if logoError}<small class="logo-error">{logoError}</small>{/if}
    </section>

    <div class="form-actions">
      <button class="button" type="submit" disabled={!canEdit}>Şirket ayarlarını kaydet</button>
      {#if !canEdit}<span class="hint">Bu ayarları değiştirme yetkiniz yok.</span>{/if}
    </div>
  </form>

  {#if canDelete}
    <div class="danger-row">
      <div class="danger-text">
        <strong>Şirketi sil</strong>
        <span>{company.trade_name} ve tüm verileri kalıcı olarak silinir. Geri alınamaz.</span>
      </div>
      <button
        type="button"
        class="button danger sm"
        onclick={() => {
          deleteConfirmName = '';
          deleteOpen = true;
        }}>Sil</button
      >
    </div>
  {/if}
{/if}

<DemoResetAction />

{#if company}
  <ConfirmDialog
    bind:open={deleteOpen}
    title="Şirketi sil"
    description={`"${company.trade_name}" şirketi ve tüm verileri kalıcı olarak silinecek. Onaylamak için firmanın ticari adını yazın.`}
    confirmLabel="Şirketi kalıcı olarak sil"
    onConfirm={confirmDelete}
  >
    <label class="field"
      >Ticari ad ({company.trade_name})
      <input bind:value={deleteConfirmName} autocomplete="off" placeholder={company.trade_name} />
    </label>
  </ConfirmDialog>
{/if}

<style>
  .firma-layout {
    display: grid;
    gap: 12px;
    max-width: 900px;
  }
  .logo-card {
    display: grid;
    gap: 4px;
  }
  .logo-help {
    margin: 0 0 10px;
    color: var(--text-muted);
    font-size: 12px;
  }
  .logo-body {
    display: flex;
    align-items: center;
    gap: 18px;
    flex-wrap: wrap;
  }
  .logo-preview {
    width: 140px;
    height: 84px;
    flex: 0 0 auto;
    object-fit: contain;
    border: 1px solid var(--border);
    border-radius: var(--radius-panel);
    background: var(--surface);
    padding: 8px;
  }
  .logo-preview.empty {
    display: grid;
    place-items: center;
    color: var(--text-muted);
    font-size: 11px;
  }
  .logo-actions {
    display: grid;
    gap: 8px;
    flex: 1 1 260px;
    min-width: 240px;
  }
  .logo-error {
    display: block;
    margin-top: 8px;
    color: var(--danger);
  }
  .danger-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    max-width: 900px;
    margin-top: 28px;
    padding: 12px 14px;
    border: 1px solid color-mix(in srgb, var(--danger) 35%, var(--border));
    border-radius: var(--radius-panel);
    background: color-mix(in srgb, var(--danger) 4%, var(--surface));
  }
  .danger-text {
    display: grid;
    gap: 2px;
    font-size: 12px;
  }
  .danger-text strong {
    color: var(--danger);
    font-size: 13px;
  }
  .danger-text span {
    color: var(--text-muted);
    line-height: 1.45;
  }
  .button.danger.sm {
    flex: 0 0 auto;
    height: 30px;
    padding: 0 14px;
    border-color: var(--danger);
    background: var(--danger);
    color: #fff;
    font-size: 12px;
  }
</style>
