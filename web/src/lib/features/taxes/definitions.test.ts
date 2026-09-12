import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Page from '../../../routes/ayarlar/vergi-tanimlari/+page.svelte';
import { api } from '$lib/api';
import type { TaxDefinition } from './types';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));
vi.mock('$lib/api', () => ({ api: vi.fn() }));
const seeded: TaxDefinition = {
  id: 'seeded-tax',
  company_id: 'company',
  code: 'KDV_20',
  name: 'KDV %20',
  description: 'Türkiye KDV tanımı',
  source: 'TR_TAX_LOCALIZATION',
  source_reference: 'Varya temel vergi kataloğu',
  source_version: '2026-01',
  rate: '20',
  calculation_type: 'PERCENTAGE',
  metadata: { preserved: true },
  is_active: true,
  version: 3
};
let component: ReturnType<typeof mount>;
beforeEach(() => vi.resetAllMocks());
afterEach(async () => {
  if (component) await unmount(component);
  document.body.innerHTML = '';
});
async function setup(manage = true) {
  vi.mocked(api).mockResolvedValueOnce({ permissions: manage ? ['tax.manage'] : ['tax.read'] });
  vi.mocked(api).mockResolvedValueOnce({ items: [seeded] });
  const target = document.createElement('div');
  document.body.append(target);
  component = mount(Page, { target });
  await vi.waitFor(() => {
    flushSync();
    expect(target.textContent).toContain('Hazır tanım');
  });
  return target;
}
it('edits seeded definitions with the version and preserves source metadata and the displayed rate', async () => {
  const target = await setup();
  target.querySelector<HTMLButtonElement>('[aria-label="KDV %20 düzenle"]')!.click();
  flushSync();
  const input = target.querySelector<HTMLInputElement>('#tax-definition-name')!;
  input.value = 'Standart KDV';
  input.dispatchEvent(new Event('input', { bubbles: true }));
  const value = target.querySelector<HTMLInputElement>('input[inputmode="decimal"]')!;
  value.value = '18,5';
  value.dispatchEvent(new Event('input', { bubbles: true }));
  const response = { ...seeded, rate: '18.5' };
  vi.mocked(api).mockResolvedValueOnce({ ...response, name: 'Standart KDV', version: 4 });
  target
    .querySelector('form')!
    .dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
  await vi.waitFor(() => {
    flushSync();
    expect(target.textContent).toContain('vergi tanımı güncellendi.');
  });
  const [url, options] = vi.mocked(api).mock.calls[2];
  expect(url).toBe('/taxes/definitions/seeded-tax');
  expect(options).toMatchObject({ method: 'PUT', headers: { 'If-Match': '"3"' } });
  expect(JSON.parse(options!.body as string)).toMatchObject({
    name: 'Standart KDV',
    rate: '18,5',
    calculation_type: 'PERCENTAGE',
    metadata: seeded.metadata,
    source_reference: seeded.source_reference,
    source_version: seeded.source_version
  });
  expect(target.textContent).toContain('18,5');
  target.querySelector<HTMLButtonElement>('[aria-label="Standart KDV pasife al"]')!.click();
  flushSync();
  vi.mocked(api).mockResolvedValueOnce({
    ...response,
    name: 'Standart KDV',
    version: 5,
    is_active: false
  });
  const confirm = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(
    (b) => b.textContent?.trim() === 'Pasife al'
  )!;
  expect(confirm).toBeDefined();
  confirm.click();
  await vi.waitFor(() => {
    flushSync();
    expect(target.textContent).toContain('vergi tanımı pasife alındı.');
  });
  expect(vi.mocked(api).mock.calls[3]).toEqual([
    '/taxes/definitions/seeded-tax/deactivate',
    { method: 'POST', headers: { 'If-Match': '"4"' }, body: '{}' }
  ]);
  expect(target.querySelector('[aria-label="Standart KDV pasife al"]')).toBeNull();
  expect(target.textContent).toContain('Pasif');
  target.querySelector<HTMLButtonElement>('[aria-label="Standart KDV aktife al"]')!.click();
  flushSync();
  vi.mocked(api).mockResolvedValueOnce({
    ...response,
    name: 'Standart KDV',
    version: 6,
    is_active: true
  });
  const activate = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].find(
    (b) => b.textContent?.trim() === 'Aktife al'
  )!;
  activate.click();
  await vi.waitFor(() => {
    flushSync();
    expect(target.textContent).toContain('vergi tanımı aktife alındı.');
  });
  expect(vi.mocked(api).mock.calls[4]).toEqual([
    '/taxes/definitions/seeded-tax/activate',
    { method: 'POST', headers: { 'If-Match': '"5"' }, body: '{}' }
  ]);
  expect(target.querySelector('[aria-label="Standart KDV aktife al"]')).toBeNull();
  expect(target.querySelector('[aria-label="Standart KDV pasife al"]')).not.toBeNull();
});
it('does not offer mutations to readers', async () => {
  const target = await setup(false);
  expect(target.querySelector('[aria-label="KDV %20 düzenle"]')).toBeNull();
  expect(target.querySelector('[aria-label="KDV %20 pasife al"]')).toBeNull();
});
