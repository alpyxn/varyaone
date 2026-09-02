<script lang="ts">
  import { goto } from '$app/navigation';
  import { onMount } from 'svelte';
  import {
    BarChart3,
    Contact,
    HeartHandshake,
    Package,
    Settings,
    ShoppingCart,
    Truck,
    Wallet,
    Warehouse,
    ChevronDown,
    Eye,
    PanelsTopLeft,
    Pencil,
    Plus,
    UserPlus,
    X
  } from '@lucide/svelte';
  import { api, APIRequestError, type APIError, type Session } from '$lib/api';
  import {
    CAPABILITY_DOMAINS,
    ROLE_PRESETS,
    LEVEL_UI,
    capabilityLists,
    menusForPermissions,
    permissionLabel,
    permissionsFromSelection,
    selectionFromPermissions,
    uncataloguedPermissions,
    type DomainSelection,
    type LevelKey,
    type RolePreset
  } from '$lib/design/capabilities';

  type Role = {
    id: string;
    name: string;
    is_system: boolean;
    version: number;
    permissions: string[];
  };
  type Member = {
    user: { id: string; email: string; display_name: string };
    is_active: boolean;
    version: number;
    role_ids: string[];
  };

  const DOMAIN_ICONS: Record<string, typeof Wallet> = {
    party: Contact,
    product: Package,
    inventory: Warehouse,
    sales: ShoppingCart,
    purchase: Truck,
    finance: Wallet,
    hr: HeartHandshake,
    reporting: BarChart3,
    system: Settings
  };
  let session = $state<Session | null>(null);
  let roles = $state<Role[]>([]);
  let members = $state<Member[]>([]);
  let message = $state('');
  let messageTone = $state<'success' | 'error'>('success');

  // --- rol editörü ---
  type RoleDraft = {
    id: string | null;
    version: number;
    name: string;
    selection: DomainSelection;
    extras: string[];
  };
  let draft = $state<RoleDraft | null>(null);
  let advancedOpen = $state(false);

  // --- kullanıcı davet formu ---
  let inviteName = $state('');
  let inviteEmail = $state('');
  let invitePassword = $state('');
  let inviteRoleIDs = $state<string[]>([]);

  // --- kullanıcı rol düzenleme ---
  let editingMemberID = $state<string | null>(null);
  let memberRoleDraft = $state<string[]>([]);
  let denied = $state(false);
  let showInvite = $state(false);

  const rolelessCount = $derived(members.filter((m) => m.role_ids.length === 0).length);

  const canManageRoles = $derived(Boolean(session?.permissions.includes('security.role.manage')));
  const canManageUsers = $derived(Boolean(session?.permissions.includes('security.user.manage')));

  const draftPermissions = $derived(
    draft ? permissionsFromSelection(draft.selection, draft.extras) : []
  );
  const draftCaps = $derived(capabilityLists(draftPermissions));
  const draftMenus = $derived(menusForPermissions(draftPermissions));

  onMount(async () => {
    try {
      session = await api<Session>('/session');
    } catch {
      await goto('/giris');
      return;
    }
    if (!session.permissions.includes('security.user.read')) {
      denied = true;
      return;
    }
    try {
      await refresh();
    } catch (error) {
      if (error instanceof APIRequestError && error.status === 401) {
        await goto('/giris');
        return;
      }
      if (error instanceof APIRequestError && error.status === 403) {
        denied = true;
        return;
      }
      notify((error as APIError).message ?? 'Veriler yüklenemedi.', 'error');
    }
  });

  async function refresh() {
    const [roleResponse, memberResponse] = await Promise.all([
      api<{ items: Role[] }>('/roles'),
      api<{ items: Member[] }>('/users')
    ]);
    roles = roleResponse.items;
    members = memberResponse.items;
  }

  function notify(text: string, tone: 'success' | 'error' = 'success') {
    message = text;
    messageTone = tone;
  }

  function userCount(roleID: string) {
    return members.filter((m) => m.role_ids.includes(roleID)).length;
  }

  function emptySelection(): DomainSelection {
    return Object.fromEntries(CAPABILITY_DOMAINS.map((d) => [d.key, 'none'])) as DomainSelection;
  }

  function startNewRole() {
    draft = { id: null, version: 0, name: '', selection: emptySelection(), extras: [] };
    advancedOpen = false;
  }

  function startFromPreset(preset: RolePreset) {
    draft = {
      id: null,
      version: 0,
      name: preset.name,
      selection: { ...emptySelection(), ...preset.selection },
      extras: []
    };
    advancedOpen = false;
  }

  function startEditRole(role: Role) {
    draft = {
      id: role.id,
      version: role.version,
      name: role.name,
      selection: selectionFromPermissions(role.permissions),
      extras: uncataloguedPermissions(role.permissions)
    };
    advancedOpen = draft.extras.length > 0;
  }

  function closeDraft() {
    draft = null;
  }

  function setLevel(domainKey: string, level: LevelKey | 'none') {
    if (!draft) return;
    const current = draft.selection[domainKey];
    draft.selection = { ...draft.selection, [domainKey]: current === level ? 'none' : level };
  }

  /** "Yapabilme" seçiliyken yetkili işlemler (full) kutucuğunu açıp kapatır. */
  function toggleFull(domainKey: string) {
    if (!draft) return;
    const current = draft.selection[domainKey];
    draft.selection = {
      ...draft.selection,
      [domainKey]: current === 'full' ? 'operate' : 'full'
    };
  }

  function domainHasFull(domainKey: string) {
    return CAPABILITY_DOMAINS.find((d) => d.key === domainKey)?.levels.some(
      (l) => l.key === 'full'
    );
  }

  function toggleExtra(code: string) {
    if (!draft) return;
    draft.extras = draft.extras.includes(code)
      ? draft.extras.filter((c) => c !== code)
      : [...draft.extras, code];
  }

  async function saveRole() {
    if (!draft) return;
    const name = draft.name.trim();
    const permissions = permissionsFromSelection(draft.selection, draft.extras);
    if (!name || permissions.length === 0) {
      notify('Rol adı ve en az bir iş alanı gerekli.', 'error');
      return;
    }
    try {
      if (draft.id) {
        await api<Role>(`/roles/${draft.id}`, {
          method: 'PUT',
          headers: { 'If-Match': `"${draft.version}"` },
          body: JSON.stringify({ name, permissions })
        });
        notify('Rol güncellendi.');
      } else {
        await api<Role>('/roles', {
          method: 'POST',
          body: JSON.stringify({ name, permissions })
        });
        notify('Rol oluşturuldu.');
      }
      draft = null;
      await refresh();
    } catch (error) {
      notify((error as APIError).message, 'error');
    }
  }

  async function inviteUser() {
    try {
      await api<Member>('/users', {
        method: 'POST',
        body: JSON.stringify({
          email: inviteEmail.trim(),
          display_name: inviteName.trim(),
          password: invitePassword,
          role_ids: inviteRoleIDs,
          branch_ids: [],
          warehouse_ids: []
        })
      });
      inviteName = '';
      inviteEmail = '';
      invitePassword = '';
      inviteRoleIDs = [];
      showInvite = false;
      notify('Kullanıcı eklendi.');
      await refresh();
    } catch (error) {
      notify((error as APIError).message, 'error');
    }
  }

  function startEditMember(member: Member) {
    editingMemberID = member.user.id;
    memberRoleDraft = [...member.role_ids];
  }

  function toggleMemberRole(roleID: string) {
    memberRoleDraft = memberRoleDraft.includes(roleID)
      ? memberRoleDraft.filter((id) => id !== roleID)
      : [...memberRoleDraft, roleID];
  }

  async function saveMemberRoles(member: Member) {
    try {
      await api<Member>('/users', {
        method: 'POST',
        body: JSON.stringify({
          email: member.user.email,
          display_name: member.user.display_name,
          role_ids: memberRoleDraft,
          branch_ids: [],
          warehouse_ids: []
        })
      });
      editingMemberID = null;
      notify('Kullanıcının rolleri güncellendi.');
      await refresh();
    } catch (error) {
      notify((error as APIError).message, 'error');
    }
  }

  function initials(name: string, email: string) {
    const base = name.trim() || email;
    const parts = base.split(/\s+/).filter(Boolean);
    if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toLocaleUpperCase('tr');
    return base.slice(0, 2).toLocaleUpperCase('tr');
  }

  function roleName(id: string) {
    return roles.find((r) => r.id === id)?.name ?? 'Bilinmeyen rol';
  }

  const LEVEL_STEPS: (LevelKey | 'none')[] = ['none', 'view', 'operate'];

  function isOn(domainKey: string, step: LevelKey | 'none') {
    const sel = draft?.selection[domainKey] ?? 'none';
    if (step === 'operate') return sel === 'operate' || sel === 'full';
    return sel === step;
  }
</script>

<svelte:head><title>Kullanıcılar ve Roller · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <h1>Kullanıcılar ve roller</h1>
  </div>
</header>

{#if message}<div class="notice {messageTone}" role="status">{message}</div>{/if}

{#if denied}
  <section class="card permission-card" role="alert">
    <strong>Bu sayfa için yetkiniz yok.</strong>
    <span>“Kullanıcı görüntüleme” (security.user.read) yetkisi gerekir.</span>
  </section>
{:else}
  <div class="toolbar">
    <span class="count-note">
      {members.length} kullanıcı · {roles.length} rol{rolelessCount
        ? ` · ${rolelessCount} rolsüz`
        : ''}
    </span>
    <div class="toolbar-actions">
      {#if canManageRoles}
        <button class="button secondary" type="button" onclick={startNewRole}>
          <Plus size={14} /> Yeni rol
        </button>
      {/if}
      {#if canManageUsers}
        <button
          class="button"
          type="button"
          onclick={() => {
            showInvite = !showInvite;
          }}
        >
          <UserPlus size={14} /> Kullanıcı ekle
        </button>
      {/if}
    </div>
  </div>

  <!-- ============ KULLANICI EKLE ============ -->
  {#if showInvite && canManageUsers}
    <section class="card invite">
      <div class="invite-fields">
        <label class="field">Ad soyad<input bind:value={inviteName} placeholder="Ad Soyad" /></label
        >
        <label class="field"
          >E-posta<input bind:value={inviteEmail} type="email" placeholder="ad@firma.com" /></label
        >
        <label class="field"
          >İlk parola<input
            bind:value={invitePassword}
            type="password"
            autocomplete="new-password"
            placeholder="en az 12 karakter"
          /></label
        >
      </div>
      <div class="chip-row">
        {#each roles as role (role.id)}
          <label class="chip" class:on={inviteRoleIDs.includes(role.id)}>
            <input
              type="checkbox"
              checked={inviteRoleIDs.includes(role.id)}
              onchange={() =>
                (inviteRoleIDs = inviteRoleIDs.includes(role.id)
                  ? inviteRoleIDs.filter((id) => id !== role.id)
                  : [...inviteRoleIDs, role.id])}
            />
            {role.name}
          </label>
        {/each}
      </div>
      <div class="editor-foot">
        <button
          class="button secondary"
          type="button"
          onclick={() => {
            showInvite = false;
          }}>Vazgeç</button
        >
        <button
          class="button"
          type="button"
          disabled={!inviteEmail.trim() ||
            inviteRoleIDs.length === 0 ||
            invitePassword.length < 12 ||
            !inviteName.trim()}
          onclick={inviteUser}>Kullanıcıyı ekle</button
        >
      </div>
    </section>
  {/if}

  <!-- ============ ROL EDİTÖRÜ ============ -->
  {#if draft}
    <section class="card editor">
      <div class="editor-head">
        <input
          class="role-name-input"
          bind:value={draft.name}
          maxlength="120"
          placeholder={draft.id ? 'Rol adı' : 'Yeni rol adı — ör. Ön Muhasebeci'}
        />
        <button class="icon-x" type="button" onclick={closeDraft} aria-label="Kapat"
          ><X size={16} /></button
        >
      </div>

      {#if !draft.id}
        <div class="chip-row presets">
          <span class="chip-row-label">Hazır başla:</span>
          {#each ROLE_PRESETS as preset (preset.key)}
            <button
              class="chip"
              type="button"
              title={preset.description}
              onclick={() => startFromPreset(preset)}>{preset.name}</button
            >
          {/each}
        </div>
      {/if}

      <div class="domain-list">
        {#each CAPABILITY_DOMAINS as domain (domain.key)}
          {@const Icon = DOMAIN_ICONS[domain.key]}
          {@const steps = domain.key === 'reporting' ? LEVEL_STEPS.slice(0, 2) : LEVEL_STEPS}
          {@const sel = draft.selection[domain.key]}
          <div class="domain" class:active={sel !== 'none'}>
            <div class="domain-icon"><Icon size={16} /></div>
            <div class="domain-copy">
              <strong>{domain.label}</strong>
              <span>{domain.blurb}</span>
            </div>
            <div class="segmented" role="group" aria-label={`${domain.label} erişim`}>
              {#each steps as step (step)}
                <button
                  type="button"
                  class:on={isOn(domain.key, step)}
                  onclick={() => setLevel(domain.key, step)}>{LEVEL_UI[step]}</button
                >
              {/each}
            </div>
            {#if (sel === 'operate' || sel === 'full') && domainHasFull(domain.key)}
              {@const fullLvl = domain.levels.find((l) => l.key === 'full')}
              <label class="full-toggle" title={fullLvl?.phrase}>
                <input
                  type="checkbox"
                  checked={sel === 'full'}
                  onchange={() => toggleFull(domain.key)}
                /> ＋ yetkili işlemler
              </label>
            {/if}
          </div>
        {/each}
      </div>

      <div class="preview">
        <span><Eye size={13} /> {draftCaps.view.join(', ') || '—'}</span>
        <span><Pencil size={13} /> {draftCaps.operate.join(', ') || '—'}</span>
        <span class="menus">
          <PanelsTopLeft size={13} />
          {#each draftMenus as m (m.group)}<span class="menu-chip">{m.group}</span>{:else}<span
              class="hint">yalnızca Ana Sayfa</span
            >{/each}
        </span>
      </div>

      {#if advancedOpen || draft.extras.length}
        <button
          class="advanced-toggle"
          type="button"
          onclick={() => (advancedOpen = !advancedOpen)}
        >
          <ChevronDown size={13} style={advancedOpen ? 'transform:rotate(180deg)' : ''} /> Elle eklenmiş
          yetkiler ({draft.extras.length})
        </button>
        {#if advancedOpen}
          <div class="chip-row">
            {#each draft.extras as code (code)}
              <label class="chip on">
                <input type="checkbox" checked onchange={() => toggleExtra(code)} />
                {permissionLabel(code)}
              </label>
            {:else}
              <span class="hint">Yok.</span>
            {/each}
          </div>
        {/if}
      {/if}

      <div class="editor-foot">
        <button class="button secondary" type="button" onclick={closeDraft}>Vazgeç</button>
        <button
          class="button"
          type="button"
          disabled={!draft.name.trim() || draftPermissions.length === 0}
          onclick={saveRole}
        >
          {draft.id ? 'Kaydet' : 'Rolü oluştur'}
        </button>
      </div>
    </section>
  {/if}

  <!-- ============ ROLLER ============ -->
  <section class="block">
    <h2 class="block-title">Roller</h2>
    <div class="role-grid">
      {#each roles as role (role.id)}
        {@const caps = capabilityLists(role.permissions)}
        <article class="card role-card">
          <div class="rc-top">
            <strong>{role.name}</strong>
            <span class="rc-actions">
              {#if role.is_system}<span
                  class="tag ghost"
                  title="Sistem rolünün yetkileri kilitlidir ve değiştirilemez"
                  >Sistem · kilitli</span
                >{/if}
              {#if !role.is_system && canManageRoles}
                <button
                  class="icon-btn"
                  type="button"
                  aria-label="Düzenle"
                  onclick={() => startEditRole(role)}><Pencil size={13} /></button
                >
              {/if}
            </span>
          </div>
          {#if caps.operate.length}
            <p class="rc-line"><span class="rc-tag do">Yapar</span> {caps.operate.join(', ')}</p>
          {/if}
          {#if caps.view.length}
            <p class="rc-line"><span class="rc-tag see">Görür</span> {caps.view.join(', ')}</p>
          {/if}
          {#if !caps.operate.length && !caps.view.length}
            <p class="rc-line hint">Yetki yok</p>
          {/if}
          <span class="rc-foot"
            >{userCount(role.id)} kullanıcı · {role.permissions.length} yetki</span
          >
        </article>
      {/each}
    </div>
  </section>

  <!-- ============ KULLANICILAR ============ -->
  <section class="block">
    <h2 class="block-title">Kullanıcılar</h2>
    <div class="user-grid">
      {#each members as member (member.user.id)}
        <article class="card user-card" class:inactive={!member.is_active}>
          <span class="avatar">{initials(member.user.display_name, member.user.email)}</span>
          <div class="u-body">
            <strong>{member.user.display_name || member.user.email}</strong>
            <span class="u-mail">{member.user.email}</span>

            {#if editingMemberID === member.user.id}
              <div class="chip-row">
                {#each roles as role (role.id)}
                  <label class="chip" class:on={memberRoleDraft.includes(role.id)}>
                    <input
                      type="checkbox"
                      checked={memberRoleDraft.includes(role.id)}
                      onchange={() => toggleMemberRole(role.id)}
                    />
                    {role.name}
                  </label>
                {/each}
              </div>
              <div class="u-actions">
                <button
                  class="button secondary sm"
                  type="button"
                  onclick={() => (editingMemberID = null)}>Vazgeç</button
                >
                <button
                  class="button sm"
                  type="button"
                  disabled={memberRoleDraft.length === 0}
                  onclick={() => saveMemberRoles(member)}>Kaydet</button
                >
              </div>
            {:else}
              <div class="u-roles">
                {#each member.role_ids as id (id)}
                  <span class="tag">{roleName(id)}</span>
                {/each}
                {#if member.role_ids.length === 0}<span class="tag warn">Rolsüz</span>{/if}
                {#if canManageUsers}
                  <button class="linkish" type="button" onclick={() => startEditMember(member)}
                    >düzenle</button
                  >
                {/if}
              </div>
            {/if}
          </div>
        </article>
      {/each}
    </div>
  </section>
{/if}

<style>
  .page-header {
    margin-bottom: 4px;
  }
  .lead {
    max-width: 620px;
  }

  .toolbar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    flex-wrap: wrap;
    margin: 14px 0 16px;
  }
  .count-note {
    color: var(--text-muted);
    font-size: 12px;
  }
  .toolbar-actions {
    display: flex;
    gap: 8px;
  }
  .button :global(svg) {
    margin-right: 5px;
  }

  .permission-card {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 14px;
    padding: 16px;
  }
  .permission-card strong {
    font-size: 14px;
  }
  .permission-card span {
    color: var(--text-muted);
    font-size: 12.5px;
  }

  /* ---- rol editörü ---- */
  .editor {
    margin-bottom: 16px;
    padding: 16px;
    border-color: var(--primary);
  }
  .editor-head {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-bottom: 12px;
  }
  .role-name-input {
    flex: 1;
    min-height: 34px;
    padding: 0 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 14px;
    font-weight: 650;
  }
  .role-name-input:focus {
    outline: none;
    border-color: var(--primary);
    box-shadow: 0 0 0 3px var(--focus);
  }
  .icon-x {
    flex: 0 0 auto;
    display: grid;
    place-items: center;
    width: 32px;
    height: 32px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon-x:hover {
    background: var(--surface-muted);
  }

  .domain-list {
    display: grid;
    gap: 6px;
  }
  .domain {
    display: grid;
    grid-template-columns: 28px minmax(0, 1fr) auto;
    align-items: center;
    gap: 10px;
    padding: 7px 10px;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
  }
  .domain.active {
    border-color: var(--primary);
    background: color-mix(in srgb, var(--primary-soft) 35%, var(--surface));
  }
  .domain-icon {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    border-radius: 6px;
    background: var(--primary-soft);
    color: var(--primary);
  }
  .domain-copy strong {
    display: block;
    font-size: 12.5px;
    line-height: 1.2;
  }
  .domain-copy span {
    display: block;
    margin-top: 1px;
    color: var(--text-subtle);
    font-size: 11px;
  }
  .full-toggle {
    grid-column: 2 / -1;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    margin-top: 4px;
    font-size: 11px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .full-toggle input {
    accent-color: var(--primary);
  }

  .segmented {
    display: inline-flex;
    flex: 0 0 auto;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    overflow: hidden;
  }
  .segmented button {
    padding: 5px 9px;
    border: 0;
    border-left: 1px solid var(--border);
    background: var(--surface);
    color: var(--text-muted);
    font-size: 11px;
    font-weight: 600;
    white-space: nowrap;
    cursor: pointer;
  }
  .segmented button:first-child {
    border-left: 0;
  }
  .segmented button:hover {
    background: var(--surface-muted);
  }
  .segmented button.on {
    background: var(--primary);
    color: var(--primary-foreground);
  }

  .preview {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 12px;
    padding: 10px 12px;
    border-radius: var(--radius-control);
    background: var(--surface-muted);
    font-size: 11.5px;
    color: var(--text-muted);
  }
  .preview > span {
    display: flex;
    align-items: center;
    gap: 6px;
    line-height: 1.4;
  }
  .preview :global(svg) {
    flex: 0 0 auto;
    color: var(--text-subtle);
  }
  .preview .menus {
    flex-wrap: wrap;
  }
  .menu-chip {
    padding: 1px 7px;
    border-radius: 999px;
    background: var(--surface);
    border: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 10.5px;
  }

  .advanced-toggle {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    margin-top: 10px;
    padding: 0;
    border: 0;
    background: none;
    color: var(--text-muted);
    font-size: 11.5px;
    font-weight: 600;
    cursor: pointer;
  }

  .editor-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 14px;
  }

  /* ---- chips ---- */
  .chip-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    margin-top: 8px;
  }
  .chip-row.presets {
    margin: 0 0 12px;
  }
  .chip-row-label {
    color: var(--text-muted);
    font-size: 11px;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 3px 9px;
    border: 1px solid var(--border-strong);
    border-radius: 999px;
    background: var(--surface);
    color: var(--text-muted);
    font-size: 11px;
    cursor: pointer;
  }
  .chip:hover {
    border-color: var(--primary);
    color: var(--text);
  }
  .chip.on {
    border-color: var(--primary);
    background: var(--primary-soft);
    color: var(--primary);
    font-weight: 600;
  }
  .chip input {
    accent-color: var(--primary);
  }

  /* ---- kullanıcı ekle ---- */
  .invite {
    margin-bottom: 16px;
    padding: 14px;
  }
  .invite-fields {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(190px, 1fr));
    gap: 10px;
  }

  /* ---- bloklar / kartlar ---- */
  .block {
    margin-top: 18px;
  }
  .block-title {
    margin: 0 0 10px;
    font-size: 13px;
    font-weight: 700;
  }
  .role-grid,
  .user-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
    gap: 10px;
  }

  .role-card {
    display: flex;
    flex-direction: column;
    gap: 5px;
    padding: 12px 13px;
  }
  .rc-top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .rc-top strong {
    font-size: 13px;
  }
  .rc-actions {
    display: inline-flex;
    align-items: center;
    gap: 6px;
  }
  .icon-btn {
    display: grid;
    place-items: center;
    width: 26px;
    height: 26px;
    border: 1px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--text-muted);
    cursor: pointer;
  }
  .icon-btn:hover {
    border-color: var(--primary);
    color: var(--primary);
  }
  .rc-line {
    margin: 0;
    font-size: 11.5px;
    line-height: 1.5;
    color: var(--text-muted);
  }
  .rc-tag {
    display: inline-block;
    margin-right: 5px;
    padding: 0 6px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 700;
    vertical-align: 1px;
  }
  .rc-tag.do {
    background: var(--primary-soft);
    color: var(--primary);
  }
  .rc-tag.see {
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .rc-foot {
    margin-top: 3px;
    color: var(--text-subtle);
    font-size: 10.5px;
  }

  .user-card {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    padding: 12px 13px;
  }
  .user-card.inactive {
    opacity: 0.55;
  }
  .avatar {
    flex: 0 0 auto;
    display: grid;
    place-items: center;
    width: 34px;
    height: 34px;
    border-radius: 999px;
    background: var(--primary);
    color: var(--primary-foreground);
    font-size: 12px;
    font-weight: 700;
  }
  .u-body {
    min-width: 0;
    flex: 1;
  }
  .u-body strong {
    display: block;
    font-size: 12.5px;
  }
  .u-mail {
    display: block;
    color: var(--text-subtle);
    font-size: 11px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .u-roles {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 5px;
    margin-top: 7px;
  }
  .u-actions {
    display: flex;
    justify-content: flex-end;
    gap: 6px;
    margin-top: 8px;
  }
  .linkish {
    border: 0;
    background: none;
    padding: 0 2px;
    color: var(--primary);
    font-size: 11px;
    font-weight: 600;
    cursor: pointer;
  }

  .tag {
    display: inline-flex;
    align-items: center;
    padding: 1px 8px;
    border-radius: 999px;
    background: var(--primary-soft);
    color: var(--primary);
    font-size: 10.5px;
    font-weight: 600;
  }
  .tag.ghost {
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .tag.warn {
    background: color-mix(in srgb, var(--warning) 16%, var(--surface));
    color: var(--warning);
  }
  .sm {
    min-height: 28px;
    padding: 0 10px;
    font-size: 11px;
  }

  @media (max-width: 560px) {
    .domain {
      grid-template-columns: 28px minmax(0, 1fr);
    }
    .segmented {
      grid-column: 1 / -1;
      width: 100%;
      margin-top: 4px;
    }
    .segmented button {
      flex: 1;
    }
    .full-toggle {
      grid-column: 1 / -1;
    }
  }
</style>
