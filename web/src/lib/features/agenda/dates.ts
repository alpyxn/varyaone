import type { AgendaEvent } from './api';

export const WEEKDAYS_SHORT = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'];

const monthYearFmt = new Intl.DateTimeFormat('tr-TR', { month: 'long', year: 'numeric' });
const longDayFmt = new Intl.DateTimeFormat('tr-TR', {
  day: 'numeric',
  month: 'long',
  weekday: 'long'
});
const mediumDayFmt = new Intl.DateTimeFormat('tr-TR', {
  day: 'numeric',
  month: 'long',
  weekday: 'short'
});

export function pad(n: number): string {
  return String(n).padStart(2, '0');
}

export function iso(year: number, month: number, day: number): string {
  return `${year}-${pad(month + 1)}-${pad(day)}`;
}

export function todayISO(): string {
  const now = new Date();
  return iso(now.getFullYear(), now.getMonth(), now.getDate());
}

/** Local Date at midnight from a YYYY-MM-DD string. */
export function fromISO(value: string): Date {
  const [y, m, d] = value.split('-').map(Number);
  return new Date(y, (m ?? 1) - 1, d ?? 1);
}

/** Monday-based weekday index (0 = Monday … 6 = Sunday). */
export function mondayIndex(date: Date): number {
  return (date.getDay() + 6) % 7;
}

export type DayCell = {
  key: string;
  day: number;
  inMonth: boolean;
  isToday: boolean;
  isWeekend: boolean;
};

/** Six-week (42 cell) Monday-first grid covering the given month. */
export function monthMatrix(year: number, month: number): DayCell[] {
  const today = todayISO();
  const lead = mondayIndex(new Date(year, month, 1));
  const start = new Date(year, month, 1 - lead);
  return Array.from({ length: 42 }, (_, index) => {
    const date = new Date(start.getFullYear(), start.getMonth(), start.getDate() + index);
    const weekday = date.getDay();
    const key = iso(date.getFullYear(), date.getMonth(), date.getDate());
    return {
      key,
      day: date.getDate(),
      inMonth: date.getMonth() === month,
      isToday: key === today,
      isWeekend: weekday === 0 || weekday === 6
    };
  });
}

export function monthYearLabel(year: number, month: number): string {
  return monthYearFmt.format(new Date(year, month, 1));
}

export function longDayLabel(isoDate: string): string {
  return longDayFmt.format(fromISO(isoDate));
}

export function mediumDayLabel(isoDate: string): string {
  return mediumDayFmt.format(fromISO(isoDate));
}

/** Groups events by their date, each day's list sorted by time (all-day last). */
export function groupByDate(events: readonly AgendaEvent[]): Map<string, AgendaEvent[]> {
  const map = new Map<string, AgendaEvent[]>();
  for (const event of events) {
    const list = map.get(event.date);
    if (list) list.push(event);
    else map.set(event.date, [event]);
  }
  for (const list of map.values()) {
    list.sort((a, b) => (a.time || '99:99').localeCompare(b.time || '99:99'));
  }
  return map;
}

export const REMIND_OPTIONS = [
  { value: 0, label: 'Zamanında' },
  { value: 5, label: '5 dk önce' },
  { value: 10, label: '10 dk önce' },
  { value: 30, label: '30 dk önce' },
  { value: 60, label: '1 saat önce' },
  { value: 1440, label: '1 gün önce' }
] as const;

/** Epoch ms of an event's start; all-day events anchor at 09:00 local. */
export function eventStartMs(event: AgendaEvent): number {
  return new Date(`${event.date}T${event.time || '09:00'}`).getTime();
}

export function isDone(event: AgendaEvent): boolean {
  return Boolean(event.completed_at);
}

/**
 * A "pending" event is one the user still needs to act on: not marked done and
 * dated today or earlier (overdue). These stay highlighted in the calendar
 * until completed or deleted.
 */
export function isPending(event: AgendaEvent, today: string = todayISO()): boolean {
  return !event.completed_at && event.date <= today;
}
