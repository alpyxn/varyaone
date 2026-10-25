// @vitest-environment node
import { describe, expect, it } from 'vitest';
import {
  callbackPreset,
  callOutcomeLabel,
  callOutcomes,
  defaultLeadListContext,
  editableLeadStatuses,
  leadContextFromParams,
  leadContextQuery,
  leadContextToParams,
  withLeadContext,
  formatDuration,
  fromLocalInput,
  leadStatusLabel,
  leadStatuses,
  telHref,
  toLocalInput
} from './types';

describe('crm types', () => {
  it('labels every status and outcome in Turkish', () => {
    for (const status of leadStatuses) expect(leadStatusLabel(status)).not.toBe(status);
    for (const outcome of callOutcomes) expect(callOutcomeLabel(outcome)).not.toBe(outcome);
  });

  it('never offers CONVERTED as a directly editable status', () => {
    expect(editableLeadStatuses).not.toContain('CONVERTED');
  });

  it('keeps the outcome list within the 1–9 keyboard shortcuts', () => {
    expect(callOutcomes.length).toBeLessThanOrEqual(9);
  });

  it('builds tel: links from any written form', () => {
    expect(telHref('0532 111 22 33')).toBe('tel:05321112233');
    expect(telHref(' +90 (532) 111-2233')).toBe('tel:+905321112233');
  });

  it('round-trips datetime-local values', () => {
    const local = '2026-11-02T14:30';
    expect(toLocalInput(fromLocalInput(local))).toBe(local);
    expect(fromLocalInput('')).toBe('');
  });

  it('formats call durations', () => {
    expect(formatDuration(null)).toBe('');
    expect(formatDuration(65)).toBe('1:05');
    expect(formatDuration(3725)).toBe('1:02:05');
  });

  it('offers callback presets in the future', () => {
    const now = new Date(2026, 9, 25, 16, 20);
    expect(callbackPreset('hour', now)).toEqual(new Date(2026, 9, 25, 18, 0));
    expect(callbackPreset('tomorrow', now)).toEqual(new Date(2026, 9, 26, 10, 0));
    expect(callbackPreset('week', now)).toEqual(new Date(2026, 10, 1, 10, 0));
  });
});

describe('lead list context', () => {
  it('round-trips through the URL and omits defaults', () => {
    expect(leadContextToParams(defaultLeadListContext()).toString()).toBe('');
    const context = {
      ...defaultLeadListContext(),
      priority: 'B',
      city: 'Antalya',
      view: 'pool' as const,
      sort: 'newest' as const,
      hideClosed: false
    };
    const params = leadContextToParams(context);
    expect(leadContextFromParams(params)).toEqual(context);
    expect(withLeadContext('/crm/adaylar', context)).toContain('oncelik=B');
  });

  it('ignores unknown values', () => {
    const context = leadContextFromParams(new URLSearchParams('gorunum=x&sira=y'));
    expect(context.view).toBe('all');
    expect(context.sort).toBe('priority');
  });

  it('maps the view to the list query', () => {
    const query = leadContextQuery({ ...defaultLeadListContext(), view: 'mine', priority: 'A' });
    expect(query.owner).toBe('me');
    expect(query.priority).toBe('A');
    expect(query.hideClosed).toBe(true);
    expect(query.sort).toBe('priority');
  });
});
