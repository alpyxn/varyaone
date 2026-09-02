import { api } from '$lib/api';

export type AgendaEvent = {
  id: string;
  /** YYYY-MM-DD */
  date: string;
  /** HH:MM, or '' for an all-day entry */
  time: string;
  title: string;
  remind_minutes: number;
  notified_at?: string | null;
  completed_at?: string | null;
  version: number;
  created_at: string;
};

export type AgendaEventInput = {
  date: string;
  time: string;
  title: string;
  remind_minutes: number;
};

export async function listEvents(signal?: AbortSignal): Promise<AgendaEvent[]> {
  const response = await api<{ events?: AgendaEvent[] }>('/agenda/events', { signal });
  return response.events ?? [];
}

export function createEvent(input: AgendaEventInput): Promise<AgendaEvent> {
  return api<AgendaEvent>('/agenda/events', {
    method: 'POST',
    body: JSON.stringify(input)
  });
}

export function setCompleted(id: string, completed: boolean): Promise<AgendaEvent> {
  return api<AgendaEvent>(`/agenda/events/${encodeURIComponent(id)}/complete`, {
    method: 'POST',
    body: JSON.stringify({ completed })
  });
}

export function deleteEvent(id: string): Promise<void> {
  return api<void>(`/agenda/events/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

export function markNotified(ids: string[]): Promise<void> {
  return api<void>('/agenda/events/notified', {
    method: 'POST',
    body: JSON.stringify({ ids })
  });
}
