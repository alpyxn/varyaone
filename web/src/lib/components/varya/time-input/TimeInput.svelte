<script lang="ts">
  // 24-hour time picker. Deterministic — never shows AM/PM regardless of the
  // browser locale. Value is an "HH:MM" string (empty string when unset).
  let {
    value = $bindable(''),
    id,
    name,
    minuteStep = 5,
    disabled = false,
    required = false,
    ariaLabel = 'Saat',
    onValueChange
  }: {
    value?: string;
    id?: string;
    name?: string;
    minuteStep?: number;
    disabled?: boolean;
    required?: boolean;
    ariaLabel?: string;
    onValueChange?: (value: string) => void;
  } = $props();

  const hours = Array.from({ length: 24 }, (_, i) => String(i).padStart(2, '0'));
  const minutes = $derived(
    Array.from({ length: Math.ceil(60 / minuteStep) }, (_, i) =>
      String(i * minuteStep).padStart(2, '0')
    )
  );

  const parts = $derived(/^(\d{2}):(\d{2})$/.exec(value ?? ''));
  const hh = $derived(parts ? parts[1] : '');
  const mm = $derived(parts ? parts[2] : '');

  function commit(nextHH: string, nextMM: string) {
    value = nextHH && nextMM ? `${nextHH}:${nextMM}` : '';
    onValueChange?.(value);
  }
</script>

<span class="time-input" class:disabled>
  <select
    {id}
    name={name ? `${name}-hh` : undefined}
    class="unit"
    {disabled}
    {required}
    aria-label={`${ariaLabel} — saat`}
    value={hh}
    onchange={(e) => commit(e.currentTarget.value, mm || minutes[0])}
  >
    <option value="" disabled={required}>SS</option>
    {#each hours as h}<option value={h}>{h}</option>{/each}
  </select>
  <span class="sep" aria-hidden="true">:</span>
  <select
    name={name ? `${name}-mm` : undefined}
    class="unit"
    {disabled}
    {required}
    aria-label={`${ariaLabel} — dakika`}
    value={mm}
    onchange={(e) => commit(hh || '09', e.currentTarget.value)}
  >
    <option value="" disabled={required}>DD</option>
    {#each minutes as m}<option value={m}>{m}</option>{/each}
  </select>
</span>

<style>
  .time-input {
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }
  .unit {
    height: var(--control-height, 34px);
    padding: 0 4px 0 6px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
  }
  .unit:focus-visible {
    border-color: var(--primary);
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .time-input.disabled .unit {
    cursor: not-allowed;
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .sep {
    color: var(--text-muted);
    font-weight: 700;
  }
</style>
