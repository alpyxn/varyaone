// The test runner resolves `svelte` to its server entrypoint; use the client entrypoint for jsdom.
// @ts-expect-error Svelte does not publish declarations for this client entrypoint path.
import { mount, tick, unmount } from '../../../../node_modules/svelte/src/index-client.js';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@lucide/svelte', () => ({
  PackageOpen: () => {},
  Search: () => {},
  SlidersHorizontal: () => {}
}));

import VariantStockMovementTable, {
  type VariantStockMovementRow
} from './VariantStockMovementTable.svelte';

let component: Record<string, unknown> | undefined;
let target: HTMLDivElement | undefined;

function render(
  rows: VariantStockMovementRow[],
  props: Partial<{
    direction: 'IN' | 'OUT';
    onQuantityChange: (row: VariantStockMovementRow, value: string) => void;
    onValidationChange: (errors: Record<string, string>, valid: boolean) => void;
  }> = {}
) {
  target = document.createElement('div');
  document.body.append(target);
  component = mount(VariantStockMovementTable, {
    target,
    props: { rows, ...props }
  }) as unknown as Record<string, unknown>;
  return target;
}

afterEach(async () => {
  if (component) await unmount(component);
  target?.remove();
  component = undefined;
  target = undefined;
});

describe('VariantStockMovementTable', () => {
  it('shows active variants with attributes and inbound cost fields', async () => {
    const root = render([
      {
        id: 'variant-red',
        variant_code: 'TSHIRT-RED-M',
        variant_name: 'Kırmızı tişört',
        attributes: { Renk: 'Kırmızı', Beden: 'M' },
        physical_quantity: '10',
        reserved_quantity: '2',
        available_quantity: '8'
      },
      { id: 'variant-inactive', variant_code: 'TSHIRT-BLACK', is_active: false }
    ]);
    await tick();

    expect(root.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(root.textContent).toContain('Renk: Kırmızı · Beden: M');
    expect(root.textContent).toContain('TSHIRT-RED-M');
    expect(root.textContent).toContain('8 ADET');
    expect(root.querySelector('th.cost-column')?.textContent).toContain('Birim maliyet');
    expect(root.textContent).not.toContain('TSHIRT-BLACK');
  });

  it('filters variants by search and by entered quantity', async () => {
    const root = render([
      { id: 'variant-red', variant_code: 'TSHIRT-RED-M', variant_name: 'Kırmızı' },
      { id: 'variant-blue', variant_code: 'TSHIRT-BLUE-L', variant_name: 'Mavi', quantity: '2' }
    ]);
    await tick();

    const search = root.querySelector<HTMLInputElement>('input[type="search"]');
    expect(search).not.toBeNull();
    search!.value = 'mavi';
    search!.dispatchEvent(new Event('input', { bubbles: true }));
    await tick();
    expect(root.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(root.textContent).toContain('TSHIRT-BLUE-L');

    search!.value = '';
    search!.dispatchEvent(new Event('input', { bubbles: true }));
    root.querySelector<HTMLInputElement>('input[type="checkbox"]')!.click();
    await tick();
    expect(root.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(root.textContent).toContain('1 satır girildi');
  });

  it('rejects outbound quantity above available balance and reports recovery', async () => {
    const onQuantityChange = vi.fn();
    const onValidationChange = vi.fn();
    const root = render(
      [
        {
          id: 'variant-blue',
          variant_code: 'TSHIRT-BLUE-L',
          variant_name: 'Mavi',
          available_quantity: '5'
        }
      ],
      { direction: 'OUT', onQuantityChange, onValidationChange }
    );
    await tick();

    const quantity = root.querySelector<HTMLInputElement>('[aria-label="Mavi hareket miktarı"]');
    expect(quantity).not.toBeNull();

    quantity!.value = '6';
    quantity!.dispatchEvent(new Event('input', { bubbles: true }));
    await tick();
    expect(root.textContent).toContain('Kullanılabilir bakiyeyi aşamaz.');
    expect(quantity?.getAttribute('aria-invalid')).toBe('true');
    expect(onQuantityChange).toHaveBeenLastCalledWith(
      expect.objectContaining({ id: 'variant-blue', quantity: '6' }),
      '6'
    );
    expect(onValidationChange).toHaveBeenLastCalledWith(
      { 'variant-blue': 'Kullanılabilir bakiyeyi aşamaz.' },
      false
    );

    quantity!.value = '5';
    quantity!.dispatchEvent(new Event('input', { bubbles: true }));
    await tick();
    expect(root.textContent).not.toContain('Kullanılabilir bakiyeyi aşamaz.');
    expect(quantity?.getAttribute('aria-invalid')).toBe('false');
    expect(onValidationChange).toHaveBeenLastCalledWith({}, true);
  });

  it('keeps the matrix safe while balances load or fail', async () => {
    const loadingRoot = render(
      [{ id: 'variant-red', variant_code: 'TSHIRT-RED-M', available_quantity: '8' }],
      { onValidationChange: vi.fn() }
    );
    await tick();
    expect(loadingRoot.querySelector('table')).not.toBeNull();

    await unmount(component!);
    component = undefined;
    loadingRoot.remove();

    target = document.createElement('div');
    document.body.append(target);
    component = mount(VariantStockMovementTable, {
      target,
      props: {
        rows: [{ id: 'variant-red', variant_code: 'TSHIRT-RED-M' }],
        loading: true,
        error: 'Varyant bakiyeleri okunamadı.'
      }
    }) as unknown as Record<string, unknown>;
    await tick();
    expect(target.querySelector('table')).toBeNull();
    expect(target.querySelector('[role="status"]')?.textContent).toContain(
      'Varyant stokları yükleniyor'
    );
    expect(target.querySelector('[role="alert"]')?.textContent).toContain(
      'Varyant bakiyeleri okunamadı.'
    );
  });

  it('shows a clear empty state without creating a fake variant row', async () => {
    const root = render([]);
    await tick();

    expect(root.querySelector('table')).toBeNull();
    expect(root.textContent).toContain('Aktif varyant bulunamadı');
    expect(root.textContent).not.toContain('Ana stok');
  });
});
