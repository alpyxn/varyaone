<script lang="ts">
  import { tick } from 'svelte';
  import { CalendarDays, ChevronLeft, ChevronRight } from '@lucide/svelte';

  type ParsedDate = { year: number; month: number; day: number };
  type CalendarDay = {
    iso: string;
    day: number;
    outside: boolean;
    label: string;
  };

  const monthNames = [
    'Ocak',
    'Şubat',
    'Mart',
    'Nisan',
    'Mayıs',
    'Haziran',
    'Temmuz',
    'Ağustos',
    'Eylül',
    'Ekim',
    'Kasım',
    'Aralık'
  ];
  const weekdayNames = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'];

  let {
    value = $bindable(''),
    id,
    name,
    ariaLabel = 'Tarih',
    ariaDescribedBy,
    ariaInvalid = false,
    disabled = false,
    required = false,
    onValueChange
  }: {
    value?: string;
    id?: string;
    name?: string;
    ariaLabel?: string;
    ariaDescribedBy?: string;
    ariaInvalid?: boolean;
    disabled?: boolean;
    required?: boolean;
    onValueChange?: (value: string) => void;
  } = $props();

  let rootElement = $state<HTMLSpanElement>();
  let displayElement = $state<HTMLInputElement>();
  let pickerButton = $state<HTMLButtonElement>();
  let calendarElement = $state<HTMLDivElement>();
  let displayValue = $state(formatTurkishDate(value));
  let lastValue = $state(value);
  let isOpen = $state(false);
  let viewYear = $state(new Date().getFullYear());
  let viewMonth = $state(new Date().getMonth());
  const calendarDays = $derived(buildCalendarDays(viewYear, viewMonth));
  const monthLabel = $derived(`${monthNames[viewMonth]} ${viewYear}`);

  $effect(() => {
    if (value !== lastValue) {
      lastValue = value;
      displayValue = formatTurkishDate(value);
    }
  });

  $effect(() => {
    if (!isOpen) return;
    const handleDocumentPointerDown = (event: PointerEvent) => {
      if (rootElement && !rootElement.contains(event.target as Node)) closePicker(false);
    };
    document.addEventListener('pointerdown', handleDocumentPointerDown);
    return () => document.removeEventListener('pointerdown', handleDocumentPointerDown);
  });

  function pad(valueToPad: number) {
    return String(valueToPad).padStart(2, '0');
  }

  function toISODate(year: number, month: number, day: number) {
    return `${String(year).padStart(4, '0')}-${pad(month + 1)}-${pad(day)}`;
  }

  function parseISODate(isoDate: string): ParsedDate | undefined {
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(isoDate);
    if (!match) return undefined;
    const year = Number(match[1]);
    const month = Number(match[2]) - 1;
    const day = Number(match[3]);
    const parsed = new Date(Date.UTC(year, month, day));
    if (
      parsed.getUTCFullYear() !== year ||
      parsed.getUTCMonth() !== month ||
      parsed.getUTCDate() !== day
    ) {
      return undefined;
    }
    return { year, month, day };
  }

  function todayISO() {
    const today = new Date();
    return toISODate(today.getFullYear(), today.getMonth(), today.getDate());
  }

  function formatTurkishDate(isoDate: string) {
    const parsed = parseISODate(isoDate);
    return parsed ? `${pad(parsed.day)}.${pad(parsed.month + 1)}.${parsed.year}` : '';
  }

  function toISOFromDisplay(displayDate: string) {
    const match = /^(\d{2})\.(\d{2})\.(\d{4})$/.exec(displayDate);
    if (!match) return '';
    const day = Number(match[1]);
    const month = Number(match[2]) - 1;
    const year = Number(match[3]);
    const nextValue = toISODate(year, month, day);
    return parseISODate(nextValue) ? nextValue : '';
  }

  function formatInput(rawValue: string) {
    if (/^\d{4}-\d{2}-\d{2}$/.test(rawValue)) return formatTurkishDate(rawValue);
    const digits = rawValue.replace(/\D/g, '').slice(0, 8);
    if (digits.length <= 2) return digits;
    if (digits.length <= 4) return `${digits.slice(0, 2)}.${digits.slice(2)}`;
    return `${digits.slice(0, 2)}.${digits.slice(2, 4)}.${digits.slice(4)}`;
  }

  function commit(nextValue: string) {
    value = nextValue;
    lastValue = nextValue;
    onValueChange?.(nextValue);
  }

  function handleInput(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const nextDisplayValue = formatInput(input.value);
    displayValue = nextDisplayValue;
    if (!nextDisplayValue) {
      input.setCustomValidity('');
      commit('');
    } else if (nextDisplayValue.length === 10) {
      const nextValue = toISOFromDisplay(nextDisplayValue);
      input.setCustomValidity(nextValue ? '' : 'Geçerli bir tarih girin.');
      commit(nextValue);
    } else {
      input.setCustomValidity('Tarihi GG.AA.YYYY biçiminde tamamlayın.');
    }
  }

  function openPicker(event?: Event) {
    if (event?.type !== 'click') event?.preventDefault();
    if (disabled) return;
    const selected = parseISODate(value) ?? parseISODate(todayISO())!;
    viewYear = selected.year;
    viewMonth = selected.month;
    isOpen = true;
    void tick().then(() => {
      const selectedButton = calendarElement?.querySelector<HTMLButtonElement>(
        `[data-date="${value || todayISO()}"]`
      );
      (
        selectedButton ??
        calendarElement?.querySelector<HTMLButtonElement>('.calendar-day:not(.outside)')
      )?.focus();
    });
  }

  function closePicker(restoreFocus = true) {
    isOpen = false;
    if (restoreFocus) pickerButton?.focus();
  }

  function selectDate(isoDate: string) {
    if (!parseISODate(isoDate)) return;
    displayValue = formatTurkishDate(isoDate);
    displayElement?.setCustomValidity('');
    commit(isoDate);
    closePicker();
  }

  function buildCalendarDays(year: number, month: number): CalendarDay[] {
    const firstDay = new Date(Date.UTC(year, month, 1));
    const mondayBasedOffset = (firstDay.getUTCDay() + 6) % 7;
    const days: CalendarDay[] = [];
    for (let index = 0; index < 42; index += 1) {
      const date = new Date(Date.UTC(year, month, index - mondayBasedOffset + 1));
      const dateYear = date.getUTCFullYear();
      const dateMonth = date.getUTCMonth();
      const dateDay = date.getUTCDate();
      const isoDate = toISODate(dateYear, dateMonth, dateDay);
      days.push({
        iso: isoDate,
        day: dateDay,
        outside: dateYear !== year || dateMonth !== month,
        label: `${dateDay} ${monthNames[dateMonth]} ${dateYear}`
      });
    }
    return days;
  }

  async function changeMonth(delta: number) {
    const nextMonth = new Date(Date.UTC(viewYear, viewMonth + delta, 1));
    viewYear = nextMonth.getUTCFullYear();
    viewMonth = nextMonth.getUTCMonth();
    await tick();
    calendarElement?.querySelector<HTMLButtonElement>('.calendar-day:not(.outside)')?.focus();
  }

  function addDays(isoDate: string, amount: number) {
    const parsed = parseISODate(isoDate);
    if (!parsed) return isoDate;
    const date = new Date(Date.UTC(parsed.year, parsed.month, parsed.day));
    date.setUTCDate(date.getUTCDate() + amount);
    return toISODate(date.getUTCFullYear(), date.getUTCMonth(), date.getUTCDate());
  }

  async function moveFocus(isoDate: string, amount: number) {
    const nextDate = addDays(isoDate, amount);
    const parsed = parseISODate(nextDate);
    if (!parsed) return;
    viewYear = parsed.year;
    viewMonth = parsed.month;
    await tick();
    calendarElement?.querySelector<HTMLButtonElement>(`[data-date="${nextDate}"]`)?.focus();
  }

  function handleDayKeydown(event: KeyboardEvent, isoDate: string) {
    if (event.key === 'Escape') {
      event.preventDefault();
      closePicker();
      return;
    }
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      selectDate(isoDate);
      return;
    }
    const movement: Record<string, number> = {
      ArrowLeft: -1,
      ArrowRight: 1,
      ArrowUp: -7,
      ArrowDown: 7
    };
    if (movement[event.key] !== undefined) {
      event.preventDefault();
      void moveFocus(isoDate, movement[event.key]);
      return;
    }
    if (event.key === 'PageUp' || event.key === 'PageDown') {
      event.preventDefault();
      void changeMonth(event.key === 'PageUp' ? -1 : 1);
    }
  }

  function handleCalendarKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault();
      closePicker();
      return;
    }
    if (event.key !== 'Tab' || !calendarElement) return;
    const focusable = Array.from(
      calendarElement.querySelectorAll<HTMLElement>(
        'button:not([disabled]), [tabindex]:not([tabindex="-1"])'
      )
    );
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first.focus();
    }
  }
</script>

<span class="date-input" bind:this={rootElement}>
  <input
    bind:this={displayElement}
    class="display-input"
    type="text"
    {id}
    {name}
    value={displayValue}
    {required}
    {disabled}
    aria-label={ariaLabel}
    aria-describedby={ariaDescribedBy}
    aria-invalid={ariaInvalid}
    placeholder="GG.AA.YYYY"
    inputmode="numeric"
    autocomplete="off"
    maxlength="10"
    oninput={handleInput}
    onclick={openPicker}
    onkeydown={(event) => {
      if (event.key === 'ArrowDown' || event.key === 'Enter') openPicker(event);
    }}
  />
  <button
    bind:this={pickerButton}
    class="calendar-button"
    type="button"
    id={id ? `${id}-calendar` : undefined}
    aria-label={`${ariaLabel} takvimini aç`}
    aria-haspopup="dialog"
    aria-expanded={isOpen}
    {disabled}
    onclick={openPicker}><CalendarDays size={14} aria-hidden="true" /></button
  >
  {#if isOpen}
    <div
      bind:this={calendarElement}
      class="calendar-popover"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      aria-label={`${ariaLabel} takvimi`}
      onkeydown={handleCalendarKeydown}
    >
      <div class="calendar-header">
        <button
          type="button"
          class="calendar-nav"
          aria-label="Önceki ay"
          onclick={() => void changeMonth(-1)}><ChevronLeft size={15} aria-hidden="true" /></button
        >
        <strong aria-live="polite">{monthLabel}</strong>
        <button
          type="button"
          class="calendar-nav"
          aria-label="Sonraki ay"
          onclick={() => void changeMonth(1)}><ChevronRight size={15} aria-hidden="true" /></button
        >
      </div>
      <div class="calendar-weekdays" aria-hidden="true">
        {#each weekdayNames as weekday}<span>{weekday}</span>{/each}
      </div>
      <div class="calendar-grid" aria-label="Takvim günleri">
        {#each calendarDays as calendarDay (calendarDay.iso)}
          <button
            type="button"
            class="calendar-day"
            class:outside={calendarDay.outside}
            class:selected={calendarDay.iso === value}
            class:today={calendarDay.iso === todayISO()}
            data-date={calendarDay.iso}
            aria-label={`${calendarDay.label}${calendarDay.iso === value ? ' — seçili' : ''}`}
            aria-pressed={calendarDay.iso === value}
            onclick={() => selectDate(calendarDay.iso)}
            onkeydown={(event) => handleDayKeydown(event, calendarDay.iso)}
            >{calendarDay.day}</button
          >
        {/each}
      </div>
      <div class="calendar-footer">
        <span>GG.AA.YYYY</span>
        <button type="button" class="today-button" onclick={() => selectDate(todayISO())}
          >Bugün</button
        >
      </div>
    </div>
  {/if}
</span>

<style>
  .date-input {
    position: relative;
    display: block;
    min-width: 0;
  }
  .display-input {
    width: 100%;
    height: var(--control-height);
    min-width: 0;
    padding: 0 40px 0 9px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text);
    font: inherit;
    font-size: 12px;
  }
  .display-input:focus-visible {
    border-color: var(--primary);
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .display-input:disabled {
    cursor: not-allowed;
    background: var(--surface-muted);
    color: var(--text-muted);
  }
  .calendar-button {
    position: absolute;
    top: 50%;
    right: 5px;
    display: grid;
    width: 28px;
    height: 28px;
    place-items: center;
    transform: translateY(-50%);
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--text-muted);
    cursor: pointer;
  }
  .calendar-button:hover:not(:disabled) {
    background: var(--surface-muted);
    color: var(--primary);
  }
  .calendar-button:focus-visible,
  .calendar-nav:focus-visible,
  .calendar-day:focus-visible,
  .today-button:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 1px;
  }
  .calendar-button:disabled {
    cursor: not-allowed;
    opacity: 0.55;
  }
  .calendar-popover {
    position: absolute;
    z-index: 50;
    top: calc(100% + 6px);
    right: 0;
    width: min(286px, calc(100vw - 24px));
    padding: 10px;
    border: 1px solid var(--border-strong);
    border-radius: var(--radius-card);
    background: var(--surface);
    box-shadow: var(--shadow-lg);
    color: var(--text);
  }
  .calendar-header {
    display: grid;
    grid-template-columns: 32px 1fr 32px;
    align-items: center;
    gap: 4px;
    margin-bottom: 8px;
    text-align: center;
  }
  .calendar-header strong {
    font-size: 12px;
    font-weight: 750;
  }
  .calendar-nav {
    display: grid;
    width: 32px;
    height: 32px;
    place-items: center;
    border: 1px solid var(--border);
    border-radius: var(--radius-control);
    background: var(--surface);
    color: var(--text-subtle);
    cursor: pointer;
  }
  .calendar-nav:hover {
    background: var(--surface-muted);
    color: var(--primary);
  }
  .calendar-weekdays,
  .calendar-grid {
    display: grid;
    grid-template-columns: repeat(7, minmax(0, 1fr));
    gap: 3px;
  }
  .calendar-weekdays {
    margin-bottom: 3px;
    color: var(--text-muted);
    font-size: 9px;
    font-weight: 750;
    text-align: center;
    text-transform: uppercase;
  }
  .calendar-day {
    min-width: 0;
    min-height: 32px;
    padding: 0;
    border: 1px solid transparent;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: 11px;
    cursor: pointer;
  }
  .calendar-day:hover:not(.selected) {
    background: var(--surface-muted);
  }
  .calendar-day.outside {
    color: var(--text-muted);
    opacity: 0.55;
  }
  .calendar-day.today {
    border-color: var(--primary);
  }
  .calendar-day.selected {
    border-color: var(--primary);
    background: var(--primary);
    color: var(--primary-foreground);
    font-weight: 750;
  }
  .calendar-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
    margin-top: 8px;
    padding-top: 8px;
    border-top: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 10px;
  }
  .today-button {
    padding: 4px 7px;
    border: 0;
    border-radius: var(--radius-control);
    background: transparent;
    color: var(--primary);
    cursor: pointer;
    font: inherit;
    font-weight: 750;
  }
  .today-button:hover {
    background: var(--surface-muted);
  }
  @media (max-width: 640px) {
    .calendar-popover {
      position: fixed;
      top: auto;
      right: 12px;
      bottom: 12px;
      left: 12px;
      width: auto;
      max-width: none;
    }
  }
</style>
