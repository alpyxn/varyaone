<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  import { TimeInput } from '$lib/components/varya/time-input';

  // Date + 24-hour time built from the shared pickers. The value has the
  // same shape as a native datetime-local input ("YYYY-MM-DDTHH:MM", '' when
  // unset), so callers convert it exactly as before. Picking a date without a
  // time uses defaultTime.
  let {
    value = $bindable(''),
    id,
    ariaLabel = 'Tarih',
    defaultTime = '10:00',
    minuteStep = 5,
    disabled = false,
    required = false
  }: {
    value?: string;
    id?: string;
    ariaLabel?: string;
    defaultTime?: string;
    minuteStep?: number;
    disabled?: boolean;
    required?: boolean;
  } = $props();

  const parts = $derived(/^(\d{4}-\d{2}-\d{2})(?:T(\d{2}:\d{2}))?/.exec(value ?? ''));
  const date = $derived(parts?.[1] ?? '');
  const time = $derived(parts?.[2] ?? '');

  function setDate(next: string) {
    value = next ? `${next}T${time || defaultTime}` : '';
  }

  function setTime(next: string) {
    if (!date) return;
    value = `${date}T${next || defaultTime}`;
  }
</script>

<span class="date-time">
  <DateInput {id} value={date} {ariaLabel} {disabled} {required} onValueChange={setDate} />
  <TimeInput
    value={time}
    ariaLabel="{ariaLabel} saati"
    {minuteStep}
    disabled={disabled || !date}
    onValueChange={setTime}
  />
</span>

<style>
  .date-time {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
</style>
