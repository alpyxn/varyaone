<script lang="ts">
  import { onMount } from 'svelte';
  import { APIRequestError } from '$lib/api';
  import { createPartyActivity, listCRMMembers, listPartyActivities } from './api';
  import ActivityTimeline from './ActivityTimeline.svelte';
  import OpportunitiesPanel from './OpportunitiesPanel.svelte';
  import type { Activity, Member } from './types';

  let {
    partyID,
    partyName = '',
    permissions = [],
    userID = ''
  }: { partyID: string; partyName?: string; permissions?: string[]; userID?: string } = $props();

  let activities = $state<Activity[]>([]);
  let members = $state<Member[]>([]);
  let error = $state('');
  const canEdit = $derived(permissions.includes('crm.edit'));
  const canManage = $derived(permissions.includes('crm.manage'));

  onMount(async () => {
    try {
      const [timeline, team] = await Promise.all([
        listPartyActivities(partyID),
        listCRMMembers().catch(() => ({ items: [] as Member[] }))
      ]);
      activities = timeline.items;
      members = team.items;
    } catch (cause) {
      error = cause instanceof APIRequestError ? cause.message : 'CRM kayıtları yüklenemedi.';
    }
  });
</script>

<section class="crm" aria-label="Müşteri ilişkileri">
  <h2>Müşteri ilişkileri</h2>
  {#if error}<p class="notice error" role="alert">{error}</p>{/if}
  <div class="layout">
    <div class="col">
      <OpportunitiesPanel partyId={partyID} defaultTitle={partyName} {canEdit} />
    </div>
    <div class="col">
      <ActivityTimeline
        bind:activities
        create={(input) => createPartyActivity(partyID, input)}
        {canEdit}
        {canManage}
        {userID}
        {members}
        allowCall
        showOrigin
        title="Görüşmeler ve notlar"
      />
    </div>
  </div>
</section>

<style>
  .crm {
    margin-top: 20px;
  }
  .crm h2 {
    margin: 0;
    font-size: 15px;
  }
  .layout {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: 0 16px;
  }
  @media (min-width: 960px) {
    .layout {
      grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
    }
  }
  .col {
    min-width: 0;
  }
</style>
