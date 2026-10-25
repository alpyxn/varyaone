import { api } from '$lib/api';
import type {
  AddLeadsResult,
  Activity,
  ActivityInput,
  CallInput,
  CallList,
  CallListInput,
  CallListItem,
  CallResult,
  CallScript,
  ImportInput,
  ImportPreview,
  ImportResult,
  Lead,
  LeadFilterInput,
  LeadInput,
  LeadList,
  LeadSummary,
  Member,
  NextCall,
  Opportunity,
  OpportunityInput,
  Report,
  Stage,
  StageInput
} from './types';

export type LeadListOptions = {
  q?: string;
  hideClosed?: boolean;
  sort?: string;
  /** Only leads a call flow may offer: callable and never called, or with a due callback. */
  callable?: boolean;
  /** Only leads nobody has called yet. */
  neverCalled?: boolean;
  priority?: string;
  status?: string;
  owner?: string;
  source?: string;
  city?: string;
  due?: boolean;
  doNotCall?: '' | 'only' | 'exclude';
  cursor?: string;
  limit?: number;
  signal?: AbortSignal;
};

export const listLeads = (options: LeadListOptions = {}) => {
  const params = new URLSearchParams();
  if (options.q) params.set('q', options.q);
  if (options.priority) params.set('priority', options.priority);
  if (options.hideClosed) params.set('hide_closed', 'true');
  if (options.sort) params.set('sort', options.sort);
  if (options.callable) params.set('callable', 'true');
  if (options.neverCalled) params.set('never_called', 'true');
  if (options.status) params.set('status', options.status);
  if (options.owner) params.set('owner', options.owner);
  if (options.source) params.set('source', options.source);
  if (options.city) params.set('city', options.city);
  if (options.due) params.set('due', 'true');
  if (options.doNotCall) params.set('do_not_call', options.doNotCall);
  if (options.cursor) params.set('cursor', options.cursor);
  if (options.limit) params.set('limit', String(options.limit));
  const query = params.toString();
  return api<LeadList>(`/crm/leads${query ? `?${query}` : ''}`, { signal: options.signal });
};

export const getLead = (id: string, signal?: AbortSignal) =>
  api<Lead>(`/crm/leads/${encodeURIComponent(id)}`, { signal });

export const findDuplicateLeads = (phone: string, exclude = '', signal?: AbortSignal) => {
  const params = new URLSearchParams({ phone });
  if (exclude) params.set('exclude', exclude);
  return api<{ items: LeadSummary[] }>(`/crm/leads/duplicates?${params}`, { signal });
};

export const createLead = (input: LeadInput) =>
  api<Lead>('/crm/leads', { method: 'POST', body: JSON.stringify(input) });

export const updateLead = (id: string, version: number, input: LeadInput) =>
  api<Lead>(`/crm/leads/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const setLeadDoNotCall = (id: string, doNotCall: boolean, reason = '') =>
  api<Lead>(`/crm/leads/${encodeURIComponent(id)}/do-not-call`, {
    method: 'POST',
    body: JSON.stringify({ do_not_call: doNotCall, reason })
  });

export const logCall = (leadID: string, input: CallInput) =>
  api<CallResult>(`/crm/leads/${encodeURIComponent(leadID)}/calls`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const listLeadActivities = (leadID: string, signal?: AbortSignal) =>
  api<{ items: Activity[] }>(`/crm/leads/${encodeURIComponent(leadID)}/activities`, { signal });

export const createLeadActivity = (leadID: string, input: ActivityInput) =>
  api<Activity>(`/crm/leads/${encodeURIComponent(leadID)}/activities`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const listPartyActivities = (partyID: string, signal?: AbortSignal) =>
  api<{ items: Activity[] }>(`/crm/parties/${encodeURIComponent(partyID)}/activities`, { signal });

export const createPartyActivity = (partyID: string, input: ActivityInput) =>
  api<Activity>(`/crm/parties/${encodeURIComponent(partyID)}/activities`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const listTasks = (
  options: { scope?: 'mine' | 'all'; state?: 'open' | 'overdue' | 'today' | 'done' } = {},
  signal?: AbortSignal
) => {
  const params = new URLSearchParams();
  if (options.scope) params.set('scope', options.scope);
  if (options.state) params.set('state', options.state);
  const query = params.toString();
  return api<{ items: Activity[] }>(`/crm/tasks${query ? `?${query}` : ''}`, { signal });
};

export const setTaskDone = (id: string, done: boolean) =>
  api<Activity>(`/crm/tasks/${encodeURIComponent(id)}/done`, {
    method: 'POST',
    body: JSON.stringify({ done })
  });

export const listCRMMembers = (signal?: AbortSignal) =>
  api<{ items: Member[] }>('/crm/members', { signal });

// --- Cariye dönüştürme -------------------------------------------------------

export const convertLead = (leadID: string, partyID: string) =>
  api<Lead>(`/crm/leads/${encodeURIComponent(leadID)}/convert`, {
    method: 'POST',
    body: JSON.stringify({ party_id: partyID })
  });

// --- İçe aktarma -------------------------------------------------------------

export const previewLeadImport = (file: File, sheet = '') => {
  const form = new FormData();
  form.append('file', file);
  if (sheet) form.append('sheet', sheet);
  return api<ImportPreview>('/crm/leads/import/preview', { method: 'POST', body: form });
};

export const importLeads = (input: ImportInput) =>
  api<ImportResult>('/crm/leads/import', { method: 'POST', body: JSON.stringify(input) });

// --- Arama listeleri ---------------------------------------------------------

export const listCallLists = (includeInactive = false, signal?: AbortSignal) =>
  api<{ items: CallList[] }>(`/crm/call-lists${includeInactive ? '?include_inactive=true' : ''}`, {
    signal
  });

export const getCallList = (id: string, signal?: AbortSignal) =>
  api<CallList>(`/crm/call-lists/${encodeURIComponent(id)}`, { signal });

export const createCallList = (input: CallListInput) =>
  api<CallList>('/crm/call-lists', { method: 'POST', body: JSON.stringify(input) });

export const updateCallList = (id: string, version: number, input: CallListInput) =>
  api<CallList>(`/crm/call-lists/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const listCallListItems = (id: string, status = '', signal?: AbortSignal) =>
  api<{ items: CallListItem[] }>(
    `/crm/call-lists/${encodeURIComponent(id)}/items${status ? `?status=${status}` : ''}`,
    { signal }
  );

export type LeadSelection = ({ lead_ids: string[] } | { filter: LeadFilterInput }) & {
  /** Take leads that were already called on purpose; off by default. */
  include_called?: boolean;
  dry_run?: boolean;
};

export const addLeadsToList = (id: string, selection: LeadSelection) =>
  api<AddLeadsResult>(`/crm/call-lists/${encodeURIComponent(id)}/items`, {
    method: 'POST',
    body: JSON.stringify(selection)
  });

/** Counts what a list that is not created yet would receive. */
export const previewListSelection = (selection: LeadSelection) =>
  api<AddLeadsResult>('/crm/call-lists/preview', {
    method: 'POST',
    body: JSON.stringify(selection)
  });

export const archiveCallList = (id: string, archived: boolean) =>
  api<CallList>(`/crm/call-lists/${encodeURIComponent(id)}/archive`, {
    method: 'POST',
    body: JSON.stringify({ archived })
  });

export const deleteCallList = (id: string) =>
  api<void>(`/crm/call-lists/${encodeURIComponent(id)}`, { method: 'DELETE' });

export const removeListItems = (id: string, itemIDs: string[]) =>
  api<{ removed: number }>(`/crm/call-lists/${encodeURIComponent(id)}/items/remove`, {
    method: 'POST',
    body: JSON.stringify({ item_ids: itemIDs })
  });

export const claimNextCall = (listID: string) =>
  api<NextCall | { empty: true }>(`/crm/call-lists/${encodeURIComponent(listID)}/next`, {
    method: 'POST'
  });

export const releaseCallItem = (listID: string, itemID: string, skip: boolean) =>
  api<void>(
    `/crm/call-lists/${encodeURIComponent(listID)}/items/${encodeURIComponent(itemID)}/release`,
    { method: 'POST', body: JSON.stringify({ skip }) }
  );

// --- Konuşma metinleri -------------------------------------------------------

export const listScripts = (signal?: AbortSignal) =>
  api<{ items: CallScript[] }>('/crm/scripts', { signal });

export const createScript = (input: { name: string; body: string }) =>
  api<CallScript>('/crm/scripts', { method: 'POST', body: JSON.stringify(input) });

export const updateScript = (
  id: string,
  version: number,
  input: { name: string; body: string; is_active: boolean }
) =>
  api<CallScript>(`/crm/scripts/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

// --- Fırsatlar ---------------------------------------------------------------

export const listStages = (signal?: AbortSignal) =>
  api<{ items: Stage[] }>('/crm/stages', { signal });

export const createStage = (input: StageInput) =>
  api<Stage>('/crm/stages', { method: 'POST', body: JSON.stringify(input) });

export const updateStage = (id: string, version: number, input: StageInput) =>
  api<Stage>(`/crm/stages/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const listOpportunities = (
  options: {
    state?: 'open' | 'won' | 'lost' | 'all';
    owner?: string;
    party_id?: string;
    lead_id?: string;
    q?: string;
  } = {},
  signal?: AbortSignal
) => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(options)) if (value) params.set(key, value);
  const query = params.toString();
  return api<{ items: Opportunity[] }>(`/crm/opportunities${query ? `?${query}` : ''}`, {
    signal
  });
};

export const getOpportunity = (id: string, signal?: AbortSignal) =>
  api<Opportunity>(`/crm/opportunities/${encodeURIComponent(id)}`, { signal });

export const createOpportunity = (input: OpportunityInput) =>
  api<Opportunity>('/crm/opportunities', { method: 'POST', body: JSON.stringify(input) });

export const updateOpportunity = (id: string, version: number, input: OpportunityInput) =>
  api<Opportunity>(`/crm/opportunities/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const moveOpportunity = (id: string, version: number, stageID: string, lostReason = '') =>
  api<Opportunity>(`/crm/opportunities/${encodeURIComponent(id)}/stage`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify({ stage_id: stageID, lost_reason: lostReason })
  });

export const linkOpportunityQuote = (id: string, quoteID: string) =>
  api<Opportunity>(`/crm/opportunities/${encodeURIComponent(id)}/quote`, {
    method: 'POST',
    body: JSON.stringify({ quote_id: quoteID })
  });

export const listOpportunityActivities = (id: string, signal?: AbortSignal) =>
  api<{ items: Activity[] }>(`/crm/opportunities/${encodeURIComponent(id)}/activities`, {
    signal
  });

export const createOpportunityActivity = (id: string, input: ActivityInput) =>
  api<Activity>(`/crm/opportunities/${encodeURIComponent(id)}/activities`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

// --- Hatırlatmalar ve raporlar -----------------------------------------------

export const listReminders = (signal?: AbortSignal) =>
  api<{ items: Activity[] }>('/crm/reminders', { signal });

export const markRemindersNotified = (ids: string[]) =>
  api<void>('/crm/reminders/notified', { method: 'POST', body: JSON.stringify({ ids }) });

export const getReport = (
  options: { from?: string; to?: string; user_id?: string } = {},
  signal?: AbortSignal
) => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(options)) if (value) params.set(key, value);
  const query = params.toString();
  return api<Report>(`/crm/reports${query ? `?${query}` : ''}`, { signal });
};
