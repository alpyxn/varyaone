<script lang="ts">
  import { goto } from '$app/navigation';
  import { ArrowDownLeft, ArrowUpRight, FileText, MinusCircle, PlusCircle } from '@lucide/svelte';
  import type { Party } from './types';

  type Props = { party: Party; permissions: string[] };
  let { party, permissions }: Props = $props();

  const currency = $derived(party.default_currency || 'TRY');
  const has = (permission: string) => permissions.includes(permission);

  const canCollect = $derived(
    party.is_active && has('finance.collection.create') && has('finance.collection.post')
  );
  const canPay = $derived(
    party.is_active && has('finance.payment.create') && has('finance.payment.post')
  );
  const canManual = $derived(party.is_active && has('finance.manual.post'));
  const canStatement = $derived(has('party.ledger.read'));

  function link(extra: Record<string, string> = {}) {
    return new URLSearchParams({
      auto_open: 'true',
      party_id: party.id,
      currency,
      ...extra
    }).toString();
  }

  type Action = {
    label: string;
    hint: string;
    icon: typeof FileText;
    href: string;
    enabled: boolean;
    disabledReason: string;
    tone: 'in' | 'out' | 'debit' | 'credit' | 'neutral';
  };

  const secondary = $derived<Action[]>([
    {
      label: 'Cariyi Borçlandır',
      hint: 'Yalnız cari bakiyesi · kasa/banka etkilenmez',
      icon: MinusCircle,
      href: `/cari/hareketler?${link({ entry_kind: 'DEBIT' })}`,
      enabled: canManual,
      disabledReason: 'Manuel cari hareket yetkiniz yok.',
      tone: 'debit'
    },
    {
      label: 'Cariyi Alacaklandır',
      hint: 'Yalnız cari bakiyesi · kasa/banka etkilenmez',
      icon: PlusCircle,
      href: `/cari/hareketler?${link({ entry_kind: 'CREDIT' })}`,
      enabled: canManual,
      disabledReason: 'Manuel cari hareket yetkiniz yok.',
      tone: 'credit'
    },
    {
      label: 'Ekstre',
      hint: 'Hareketler, açık kalemler ve vadeler',
      icon: FileText,
      href: `/cari/kartlar/${party.id}/ekstre`,
      enabled: canStatement,
      disabledReason: 'Cari hareket görüntüleme yetkiniz yok.',
      tone: 'neutral'
    }
  ]);
  const inactiveReason = 'Pasif cari kartlarında yeni finansal hareket oluşturulamaz.';
</script>

<section class="quick-actions panel">
  <h2>Hızlı İşlemler</h2>

  <div class="primary-actions">
    <button
      type="button"
      class="primary in"
      disabled={!canCollect}
      title={canCollect
        ? undefined
        : party.is_active
          ? 'Tahsilat oluşturma yetkiniz yok.'
          : inactiveReason}
      aria-describedby={!canCollect ? 'collect-disabled-reason' : undefined}
      onclick={() => goto(`/cari/tahsilatlar?${link()}`)}
    >
      <ArrowDownLeft size={17} />
      <span>Tahsilat Al</span>
      <small>Cariden para/değer geldi</small>
      {#if !canCollect}<span id="collect-disabled-reason" class="sr-only"
          >{party.is_active ? 'Tahsilat oluşturma yetkiniz yok.' : inactiveReason}</span
        >{/if}
    </button>
    <button
      type="button"
      class="primary out"
      disabled={!canPay}
      title={canPay
        ? undefined
        : party.is_active
          ? 'Ödeme oluşturma yetkiniz yok.'
          : inactiveReason}
      aria-describedby={!canPay ? 'payment-disabled-reason' : undefined}
      onclick={() => goto(`/cari/odemeler?${link()}`)}
    >
      <ArrowUpRight size={17} />
      <span>Ödeme Yap</span>
      <small>Cariye para/değer gönderildi</small>
      {#if !canPay}<span id="payment-disabled-reason" class="sr-only"
          >{party.is_active ? 'Ödeme oluşturma yetkiniz yok.' : inactiveReason}</span
        >{/if}
    </button>
  </div>

  <ul class="secondary-actions">
    {#each secondary as action, index (action.label)}
      {@const disabled = !action.enabled || !party.is_active}
      <li>
        <button
          type="button"
          class={`secondary ${action.tone}`}
          {disabled}
          title={!disabled ? undefined : !party.is_active ? inactiveReason : action.disabledReason}
          aria-describedby={disabled ? `secondary-action-reason-${index}` : undefined}
          onclick={() => goto(action.href)}
        >
          <action.icon size={15} />
          <span class="labels">
            <span class="label">{action.label}</span>
            <span class="hint">{action.hint}</span>
          </span>
          {#if disabled}<span id={`secondary-action-reason-${index}`} class="sr-only"
              >{!party.is_active ? inactiveReason : action.disabledReason}</span
            >{/if}
        </button>
      </li>
    {/each}
  </ul>
</section>

<style>
  .quick-actions {
    padding: 13px;
  }
  .quick-actions h2 {
    margin: 0 0 10px;
    font-size: 13px;
  }
  .primary-actions {
    display: grid;
    gap: 8px;
  }
  .primary {
    display: grid;
    grid-template-columns: auto 1fr;
    grid-template-rows: auto auto;
    column-gap: 10px;
    align-items: center;
    padding: 10px 12px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    text-align: left;
    cursor: pointer;
    transition:
      border-color 0.12s,
      background 0.12s;
  }
  .primary :global(svg) {
    grid-row: 1 / 3;
  }
  .primary span {
    font-size: 13px;
    font-weight: 650;
  }
  .primary small {
    grid-column: 2;
    font-size: 10.5px;
    color: var(--text-muted);
    font-weight: 400;
  }
  .primary.in:hover:not(:disabled) {
    border-color: var(--success);
    background: color-mix(in srgb, var(--success) 8%, var(--surface));
  }
  .primary.in :global(svg) {
    color: var(--success);
  }
  .primary.out:hover:not(:disabled) {
    border-color: var(--danger);
    background: color-mix(in srgb, var(--danger) 8%, var(--surface));
  }
  .primary.out :global(svg) {
    color: var(--danger);
  }

  .secondary-actions {
    list-style: none;
    margin: 10px 0 0;
    padding: 10px 0 0;
    border-top: 1px solid var(--border);
    display: grid;
    gap: 4px;
  }
  .secondary {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 7px 8px;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    text-align: left;
    cursor: pointer;
  }
  .secondary:hover:not(:disabled) {
    background: var(--surface-muted);
    border-color: var(--border);
  }
  .secondary.debit :global(svg) {
    color: var(--danger);
  }
  .secondary.credit :global(svg) {
    color: var(--success);
  }
  .secondary.neutral :global(svg) {
    color: var(--text-muted);
  }
  .labels {
    display: flex;
    flex-direction: column;
    line-height: 1.25;
  }
  .label {
    font-size: 12.5px;
    font-weight: 600;
  }
  .hint {
    font-size: 10px;
    color: var(--text-muted);
  }
  button:disabled {
    opacity: 0.45;
    cursor: not-allowed;
  }
</style>
