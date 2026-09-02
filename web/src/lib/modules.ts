// Feature module catalog - mirrors internal/modules/modules.go. Keep the codes
// and order in sync with the backend.

export type ModuleCode = 'preaccounting' | 'hr' | 'fixed_asset';

export type ModuleDefinition = {
  code: ModuleCode;
  name: string;
  description: string;
};

export const MODULE_CATALOG: readonly ModuleDefinition[] = [
  {
    code: 'preaccounting',
    name: 'Ön Muhasebe',
    description: 'Cari, stok, satış, alış, banka & kasa ve raporlar.'
  },
  {
    code: 'hr',
    name: 'İnsan Kaynakları',
    description: 'Çalışanlar, izin, çalışma planı, puantaj, avanslar ve bordro.'
  },
  {
    code: 'fixed_asset',
    name: 'Sabit Kıymetler',
    description: 'Sabit kıymet kartları, kategoriler ve zimmet takibi.'
  }
];

/** Route-prefix → module map for client-side route guarding. */
export const MODULE_PATHS: readonly { prefix: string; module: ModuleCode }[] = [
  { prefix: '/personel', module: 'hr' },
  { prefix: '/sabit-kiymetler', module: 'fixed_asset' },
  { prefix: '/cari', module: 'preaccounting' },
  { prefix: '/stok', module: 'preaccounting' },
  { prefix: '/satis', module: 'preaccounting' },
  { prefix: '/alis', module: 'preaccounting' },
  { prefix: '/finans', module: 'preaccounting' },
  { prefix: '/banka', module: 'preaccounting' },
  { prefix: '/kasa', module: 'preaccounting' },
  { prefix: '/aktarimlar', module: 'preaccounting' },
  { prefix: '/raporlar', module: 'preaccounting' },
  { prefix: '/belgeler', module: 'preaccounting' }
];

/**
 * Returns the module owning pathname, or undefined when the path is core.
 * `modules === undefined` (session not loaded yet) never resolves a module.
 */
export function moduleForPath(pathname: string): ModuleCode | undefined {
  for (const entry of MODULE_PATHS) {
    if (pathname === entry.prefix || pathname.startsWith(`${entry.prefix}/`)) return entry.module;
  }
  return undefined;
}

export function isModuleEnabled(
  module: ModuleCode | undefined,
  modules?: readonly string[]
): boolean {
  if (!module) return true;
  if (modules === undefined) return true;
  return modules.includes(module);
}
