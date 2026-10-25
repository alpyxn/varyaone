<script lang="ts">
  import { DateTimeInput } from '$lib/components/varya/date-time-input';
  import { Input } from '$lib/components/ui/input';
  import * as Field from '$lib/components/ui/field';
  import {
    editableLeadStatuses,
    leadPriorities,
    leadStatusLabel,
    priorityLabel,
    type LeadInput,
    type LeadSummary,
    type Member
  } from './types';

  let {
    value = $bindable(),
    members = [],
    canAssign = false,
    showStatus = true,
    duplicates = [],
    onPhoneBlur
  }: {
    value: LeadInput;
    members?: Member[];
    canAssign?: boolean;
    showStatus?: boolean;
    duplicates?: LeadSummary[];
    onPhoneBlur?: () => void;
  } = $props();

  const id = $props.id();
</script>

<Field.FieldGroup class="lead-form-grid">
  <Field.Field>
    <Field.FieldLabel for="{id}-name">Firma / kişi adı</Field.FieldLabel>
    <Input id="{id}-name" bind:value={value.display_name} maxlength={200} required />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-contact">Yetkili</Field.FieldLabel>
    <Input id="{id}-contact" bind:value={value.contact_name} maxlength={200} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-title">Ünvanı</Field.FieldLabel>
    <Input
      id="{id}-title"
      bind:value={value.contact_title}
      maxlength={120}
      placeholder="Örn. Satın alma müdürü"
    />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-phone">Telefon</Field.FieldLabel>
    <Input
      id="{id}-phone"
      type="tel"
      bind:value={value.phone}
      maxlength={40}
      onblur={() => onPhoneBlur?.()}
      autocomplete="off"
    />
    {#if duplicates.length}
      <div class="dup" role="status">
        <strong>Bu numara başka adayda da var:</strong>
        <ul>
          {#each duplicates as match (match.id)}
            <li>
              <a href="/crm/adaylar/{match.id}" target="_blank" rel="noopener">
                {match.display_name}</a
              >
              · {leadStatusLabel(match.status)}{match.owner_name ? ` · ${match.owner_name}` : ''}
              {#if match.do_not_call}<span class="dnc">Aranmasın</span>{/if}
            </li>
          {/each}
        </ul>
      </div>
    {/if}
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-phone2">İkinci telefon</Field.FieldLabel>
    <Input id="{id}-phone2" type="tel" bind:value={value.phone2} maxlength={40} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-email">E-posta</Field.FieldLabel>
    <Input id="{id}-email" type="email" bind:value={value.email} maxlength={254} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-city">İl</Field.FieldLabel>
    <Input id="{id}-city" bind:value={value.city} maxlength={80} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-district">İlçe</Field.FieldLabel>
    <Input id="{id}-district" bind:value={value.district} maxlength={80} />
  </Field.Field>
  <Field.Field class="wide">
    <Field.FieldLabel for="{id}-address">Adres</Field.FieldLabel>
    <Input id="{id}-address" bind:value={value.address} maxlength={500} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-website">Web sitesi</Field.FieldLabel>
    <Input id="{id}-website" bind:value={value.website} maxlength={200} placeholder="ornek.com" />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-priority">Öncelik</Field.FieldLabel>
    <select id="{id}-priority" class="select" bind:value={value.priority}>
      <option value="">Derecesiz</option>
      {#each leadPriorities as priority (priority)}
        <option value={priority}>{priorityLabel(priority)}</option>
      {/each}
    </select>
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-sector">Sektör</Field.FieldLabel>
    <Input id="{id}-sector" bind:value={value.sector} maxlength={120} />
  </Field.Field>
  <Field.Field>
    <Field.FieldLabel for="{id}-source">Kaynak</Field.FieldLabel>
    <Input
      id="{id}-source"
      bind:value={value.source}
      maxlength={120}
      list="{id}-sources"
      placeholder="Örn. Fuar, referans, liste"
    />
    <datalist id="{id}-sources">
      <option value="Soğuk arama listesi"></option>
      <option value="Referans"></option>
      <option value="Fuar"></option>
      <option value="Web sitesi"></option>
      <option value="Sosyal medya"></option>
    </datalist>
  </Field.Field>
  {#if showStatus}
    <Field.Field>
      <Field.FieldLabel for="{id}-status">Durum</Field.FieldLabel>
      <select id="{id}-status" class="select" bind:value={value.status}>
        {#each editableLeadStatuses as status (status)}
          <option value={status}>{leadStatusLabel(status)}</option>
        {/each}
      </select>
    </Field.Field>
  {/if}
  {#if canAssign}
    <Field.Field>
      <Field.FieldLabel for="{id}-owner">Sorumlu</Field.FieldLabel>
      <select id="{id}-owner" class="select" bind:value={value.owner_user_id}>
        <option value="">Atanmamış (ortak havuz)</option>
        {#each members as member (member.user_id)}
          <option value={member.user_id}>{member.display_name}</option>
        {/each}
      </select>
    </Field.Field>
  {/if}
  <Field.Field>
    <Field.FieldLabel for="{id}-next">Sonraki işlem</Field.FieldLabel>
    <DateTimeInput id="{id}-next" bind:value={value.next_action_at} ariaLabel="Sonraki işlem" />
  </Field.Field>
  <Field.Field class="full">
    <Field.FieldLabel for="{id}-notes">Not</Field.FieldLabel>
    <textarea id="{id}-notes" class="textarea" bind:value={value.notes} maxlength={4000} rows="3"
    ></textarea>
  </Field.Field>
  <Field.Field class="full">
    <label class="check">
      <input type="checkbox" bind:checked={value.privacy_notice} />
      Kişisel verilerin işlenmesi hakkında bilgilendirildi (KVKK aydınlatma)
    </label>
  </Field.Field>
</Field.FieldGroup>

<style>
  :global(.lead-form-grid) {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
    gap: 14px 16px;
  }
  :global(.lead-form-grid) > :global(*) {
    min-width: 0;
  }
  :global(.lead-form-grid .full) {
    grid-column: 1 / -1;
  }
  :global(.lead-form-grid .wide) {
    grid-column: span 2;
  }
  @media (max-width: 560px) {
    :global(.lead-form-grid .wide) {
      grid-column: 1 / -1;
    }
  }
  :global(.lead-form-grid) :global(input:not([type='checkbox'])) {
    width: 100%;
    box-sizing: border-box;
  }
  .select,
  .textarea {
    width: 100%;
    box-sizing: border-box;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font-size: 13px;
  }
  .select {
    height: var(--control-height);
    padding: 0 10px;
  }
  .textarea {
    padding: 8px 10px;
    font: inherit;
    font-size: 13px;
    resize: vertical;
  }
  .select:focus,
  .textarea:focus {
    outline: none;
    border-color: var(--primary);
    box-shadow: 0 0 0 2px var(--focus);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .dup {
    margin-top: 6px;
    padding: 8px 10px;
    border-radius: var(--radius-control);
    background: color-mix(in srgb, var(--warning, #d97706) 12%, transparent);
    font-size: 12px;
  }
  .dup ul {
    margin: 4px 0 0;
    padding-left: 16px;
  }
  .dnc {
    margin-left: 6px;
    padding: 1px 6px;
    border-radius: 999px;
    background: color-mix(in srgb, var(--danger, #dc2626) 15%, transparent);
    color: var(--danger, #dc2626);
    font-weight: 600;
  }
</style>
