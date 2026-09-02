import { describe, expect, it } from 'vitest';
import { navigation } from '$lib/navigation';
import {
  CAPABILITY_DOMAINS,
  CATALOGUED_PERMISSIONS,
  ROLE_PRESETS,
  capabilityLists,
  menusForPermissions,
  permissionsFromSelection,
  selectionFromPermissions
} from './capabilities';

/** Sidebar'ın istediği her yetki, bir rol seviyesiyle verilebilmeli. */
describe('capability catalogue ↔ sidebar', () => {
  const navPermissions = new Set<string>();
  for (const group of navigation) {
    for (const child of group.children ?? []) {
      if (child.permission) navPermissions.add(child.permission);
      for (const code of child.anyPermission ?? []) navPermissions.add(code);
    }
  }

  it('every menu permission is grantable through some domain level', () => {
    const missing = [...navPermissions].filter((code) => !CATALOGUED_PERMISSIONS.has(code));
    expect(missing).toEqual([]);
  });

  it('a role with no permissions reaches no permission-gated menu item', () => {
    const reachable = new Set(menusForPermissions([]).flatMap((m) => m.items));
    for (const group of navigation) {
      for (const child of group.children ?? []) {
        if (child.permission || child.anyPermission) {
          expect(reachable.has(child.label)).toBe(false);
        }
      }
    }
    // Sensitive workspaces must never show up without an explicit permission.
    const groups = menusForPermissions([]).map((m) => m.group);
    for (const sensitive of [
      'Cari',
      'Stok',
      'Satış',
      'Alış',
      'Banka & Kasa',
      'İnsan Kaynakları',
      'Raporlar'
    ]) {
      expect(groups).not.toContain(sensitive);
    }
  });

  it('a single-area role only unlocks that area', () => {
    const financeView = permissionsFromSelection({ finance: 'view' } as Record<
      string,
      'view' | 'operate' | 'full'
    >);
    const groups = menusForPermissions(financeView).map((m) => m.group);
    expect(groups).toContain('Banka & Kasa');
    expect(groups).not.toContain('İnsan Kaynakları');
    expect(groups).not.toContain('Stok');
  });

  it('a full-access role sees every active menu group', () => {
    const everything = permissionsFromSelection(
      Object.fromEntries(
        CAPABILITY_DOMAINS.map((d) => [d.key, d.levels[d.levels.length - 1].key])
      ) as Record<string, 'view' | 'operate' | 'full'>
    );
    const groups = menusForPermissions(everything).map((m) => m.group);
    for (const group of navigation) {
      const gatedChildren = (group.children ?? []).filter((c) => c.href);
      if (!gatedChildren.length || group.href) continue;
      expect(groups).toContain(group.label);
    }
  });
});

describe('selection round-trip', () => {
  it('rebuilds the same selection from generated permissions', () => {
    for (const preset of ROLE_PRESETS) {
      const perms = permissionsFromSelection(preset.selection);
      const rebuilt = selectionFromPermissions(perms);
      for (const domain of CAPABILITY_DOMAINS) {
        expect(rebuilt[domain.key]).toBe(preset.selection[domain.key] ?? 'none');
      }
    }
  });
});

describe('capabilityLists', () => {
  it('splits view-only and operating areas', () => {
    const perms = permissionsFromSelection({ finance: 'operate', party: 'view' } as Record<
      string,
      'view' | 'operate' | 'full'
    >);
    const lists = capabilityLists(perms);
    expect(lists.operate).toContain('Ön muhasebe');
    expect(lists.view).toContain('Cari');
  });
});
