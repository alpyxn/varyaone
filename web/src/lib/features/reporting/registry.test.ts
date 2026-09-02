import { describe, expect, it } from 'vitest';
import { buildReportQuery } from './api';
import { DEFAULT_REPORT_ID, REPORTS, canSeeReport, reportByID } from './registry';

describe('reporting registry', () => {
  it('has unique ids, report-scoped endpoints and non-empty columns', () => {
    const ids = REPORTS.map((r) => r.id);
    expect(new Set(ids).size).toBe(ids.length);
    for (const report of REPORTS) {
      expect(report.endpoint.startsWith('/reports/')).toBe(true);
      expect(report.label.length).toBeGreaterThan(0);
      expect(report.columns.length).toBeGreaterThan(0);
    }
  });

  it('ships exactly the six curated reports', () => {
    expect(REPORTS.map((r) => r.id).sort()).toEqual(
      [
        'en-cok-satanlar',
        'satis-karliligi',
        'stok-degerleme',
        'vadesi-gecen-alacaklar',
        'vadesi-gecen-borclar',
        'vergi-ozeti'
      ].sort()
    );
    expect(reportByID(DEFAULT_REPORT_ID)).toBeDefined();
  });

  it('gates reports behind reporting.read plus any extra permission', () => {
    const profitability = reportByID('satis-karliligi')!;
    expect(canSeeReport(profitability, ['reporting.read'])).toBe(false);
    expect(canSeeReport(profitability, ['reporting.read', 'sales.cost.read'])).toBe(true);
    expect(canSeeReport(reportByID('stok-degerleme')!, [])).toBe(false);
  });

  it('serializes only the filter params a report declares', () => {
    const tax = reportByID('vergi-ozeti')!;
    const query = new URLSearchParams(
      buildReportQuery(tax, { from: '2026-01-01', to: '2026-01-31', direction: 'PURCHASE' })
    );
    expect(query.get('from')).toBe('2026-01-01');
    expect(query.get('direction')).toBe('PURCHASE');

    const valuation = reportByID('stok-degerleme')!;
    expect(buildReportQuery(valuation, { from: '2026-01-01', to: '2026-01-31' })).toBe('');
  });
});
