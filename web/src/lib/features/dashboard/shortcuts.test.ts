import { describe, expect, it } from 'vitest';
import { availableShortcuts, groupedShortcuts, resolvePinnedShortcuts } from './shortcuts';

// Every catalog permission, so only the module filter is under test.
const allPerms = ['party.read', 'party.create', 'hr.employee.read', 'fixed_asset.read'];

describe('dashboard shortcuts respect active modules', () => {
  it('drops tiles whose module is disabled', () => {
    const withoutHR = availableShortcuts(allPerms, ['preaccounting', 'fixed_asset']);
    expect(withoutHR.some((s) => s.href.startsWith('/personel'))).toBe(false);
    expect(withoutHR.some((s) => s.href.startsWith('/cari'))).toBe(true);

    const preAccountingOnly = availableShortcuts(allPerms, ['preaccounting']);
    expect(preAccountingOnly.some((s) => s.href.startsWith('/sabit-kiymetler'))).toBe(false);
  });

  it('hides disabled-module groups from the launcher buckets', () => {
    const groups = groupedShortcuts(allPerms, ['preaccounting']).map((b) => b.group);
    expect(groups).not.toContain('İnsan Kaynakları');
    expect(groups).not.toContain('Sabit Kıymetler');
  });

  it('filters pinned tiles by module', () => {
    const pinned = resolvePinnedShortcuts(['cari.kartlar', 'personel.calisanlar'], allPerms, [
      'preaccounting'
    ]);
    expect(pinned.map((s) => s.key)).toEqual(['cari.kartlar']);
  });

  it('shows everything when the module list is unknown', () => {
    expect(availableShortcuts(allPerms).some((s) => s.href.startsWith('/personel'))).toBe(true);
  });
});
