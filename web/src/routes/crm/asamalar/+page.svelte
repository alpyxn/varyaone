<script lang="ts">
  import { onMount } from 'svelte';
  import { ArrowLeft, LoaderCircle, Plus } from '@lucide/svelte';
  import { api, APIRequestError, type Session } from '$lib/api';
  import { Button } from '$lib/components/ui/button';
  import { Input } from '$lib/components/ui/input';
  import { createStage, listStages, updateStage } from '$lib/features/crm/api';
  import { stageKindLabel, type Stage, type StageKind } from '$lib/features/crm/types';

  let permissions = $state<string[]>([]);
  let denied = $state(false);
  let loading = $state(true);
  let error = $state('');
  let message = $state('');
  let stages = $state<Stage[]>([]);
  let drafts = $state<Record<string, { name: string; sort_order: number; probability: number }>>(
    {}
  );
  let savingID = $state('');
  let newStage = $state({ name: '', sort_order: 50, probability: 50, kind: 'OPEN' as StageKind });

  const canManage = $derived(permissions.includes('crm.manage'));

  async function load() {
    loading = true;
    try {
      stages = (await listStages()).items;
      drafts = Object.fromEntries(
        stages.map((s) => [
          s.id,
          { name: s.name, sort_order: s.sort_order, probability: s.probability }
        ])
      );
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aşamalar yüklenemedi.';
    } finally {
      loading = false;
    }
  }

  async function save(stage: Stage, isActive = stage.is_active) {
    const draft = drafts[stage.id];
    savingID = stage.id;
    error = '';
    try {
      await updateStage(stage.id, stage.version, {
        name: draft.name.trim(),
        sort_order: Number(draft.sort_order),
        probability: Number(draft.probability),
        kind: stage.kind,
        is_active: isActive
      });
      message = 'Aşama kaydedildi.';
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aşama kaydedilemedi.';
    } finally {
      savingID = '';
    }
  }

  async function add(event: SubmitEvent) {
    event.preventDefault();
    if (!newStage.name.trim()) return;
    savingID = 'new';
    error = '';
    try {
      await createStage({
        ...newStage,
        name: newStage.name.trim(),
        sort_order: Number(newStage.sort_order),
        probability: Number(newStage.probability)
      });
      newStage = { name: '', sort_order: 50, probability: 50, kind: 'OPEN' };
      message = 'Aşama eklendi.';
      await load();
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'Aşama eklenemedi.';
    } finally {
      savingID = '';
    }
  }

  onMount(async () => {
    try {
      const session = await api<Session>('/session');
      permissions = session.permissions ?? [];
      denied = !permissions.includes('crm.read');
    } catch {
      denied = true;
    }
    if (denied) {
      loading = false;
      return;
    }
    await load();
  });
</script>

<svelte:head><title>Fırsat Aşamaları · Varya One</title></svelte:head>

<header class="page-header">
  <div>
    <a class="back" href="/crm/firsatlar"><ArrowLeft size={14} aria-hidden="true" /> Fırsatlar</a>
    <h1>Fırsat Aşamaları</h1>
    <p class="meta">
      Fırsat panosunun sütunları. Olasılık, raporlardaki beklenen ciroyu hesaplar. Kazanıldı ve
      Kaybedildi aşamaları fırsatı kapatır.
    </p>
  </div>
</header>

{#if denied}
  <section class="panel error-panel" role="alert">
    <strong>Erişim yok.</strong>
    <span>CRM yetkiniz bulunmuyor.</span>
  </section>
{:else}
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  {#if message}<p class="notice success" role="status">{message}</p>{/if}

  <section class="panel table-panel">
    {#if loading}
      <p class="state" role="status">Yükleniyor…</p>
    {:else}
      <!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be reachable by keyboard) -->
      <div class="table-scroll" tabindex="0" role="region" aria-label="Aşamalar">
        <table>
          <thead>
            <tr><th>Ad</th><th>Sıra</th><th>Olasılık %</th><th>Tür</th><th>Durum</th><th></th></tr>
          </thead>
          <tbody>
            {#each stages as stage (stage.id)}
              <tr class:inactive={!stage.is_active}>
                <td>
                  {#if canManage}
                    <Input bind:value={drafts[stage.id].name} maxlength={80} aria-label="Ad" />
                  {:else}{stage.name}{/if}
                </td>
                <td class="narrow">
                  {#if canManage}
                    <Input
                      type="number"
                      bind:value={drafts[stage.id].sort_order}
                      aria-label="Sıra"
                    />
                  {:else}{stage.sort_order}{/if}
                </td>
                <td class="narrow">
                  {#if canManage}
                    <Input
                      type="number"
                      min={0}
                      max={100}
                      bind:value={drafts[stage.id].probability}
                      aria-label="Olasılık"
                    />
                  {:else}{stage.probability}{/if}
                </td>
                <td>{stageKindLabel(stage.kind)}</td>
                <td>{stage.is_active ? 'Kullanımda' : 'Kaldırıldı'}</td>
                <td class="actions">
                  {#if canManage}
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={savingID === stage.id}
                      onclick={() => void save(stage)}>Kaydet</Button
                    >
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={savingID === stage.id}
                      onclick={() => void save(stage, !stage.is_active)}
                      >{stage.is_active ? 'Kaldır' : 'Geri al'}</Button
                    >
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if canManage}
        <form class="add" onsubmit={add}>
          <Input
            bind:value={newStage.name}
            maxlength={80}
            placeholder="Yeni aşama adı"
            aria-label="Yeni aşama adı"
          />
          <Input type="number" bind:value={newStage.sort_order} aria-label="Sıra" />
          <Input
            type="number"
            min={0}
            max={100}
            bind:value={newStage.probability}
            aria-label="Olasılık"
          />
          <select class="select" bind:value={newStage.kind} aria-label="Tür">
            <option value="OPEN">Açık</option>
            <option value="WON">Kazanıldı</option>
            <option value="LOST">Kaybedildi</option>
          </select>
          <Button type="submit" disabled={savingID === 'new' || !newStage.name.trim()}>
            {#if savingID === 'new'}<LoaderCircle
                data-icon="inline-start"
                aria-hidden="true"
              />{:else}<Plus size={14} data-icon="inline-start" aria-hidden="true" />{/if}Ekle
          </Button>
        </form>
      {/if}
    {/if}
  </section>
{/if}

<style>
  .page-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 12px;
    flex-wrap: wrap;
  }
  .back {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--text-muted);
    font-size: 12px;
    text-decoration: none;
  }
  .meta {
    margin: 4px 0 0;
    color: var(--text-muted);
    font-size: 12px;
  }
  .panel {
    margin-top: 16px;
  }
  .table-panel {
    padding: 12px 14px;
  }
  .table-scroll {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  th,
  td {
    padding: 7px 8px;
    border-bottom: 1px solid var(--border);
    text-align: left;
    white-space: nowrap;
  }
  th {
    color: var(--text-muted);
    font-weight: 650;
  }
  .narrow {
    width: 90px;
  }
  .narrow :global(input) {
    width: 80px;
  }
  .actions {
    display: flex;
    gap: 6px;
  }
  .inactive {
    opacity: 0.6;
  }
  .add {
    display: grid;
    grid-template-columns: minmax(0, 3fr) 90px 90px minmax(0, 1.4fr) auto;
    gap: 8px;
    margin-top: 12px;
  }
  @media (max-width: 640px) {
    .add {
      grid-template-columns: 1fr 1fr;
    }
  }
  .select {
    height: var(--control-height);
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    padding: 0 10px;
    font-size: 13px;
  }
  .state {
    margin: 0;
    padding: 16px 0;
    color: var(--text-muted);
    font-size: 13px;
    text-align: center;
  }
</style>
