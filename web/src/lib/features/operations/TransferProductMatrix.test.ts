// The test runner resolves `svelte` to its server entrypoint; use the client entrypoint for jsdom.
// @ts-expect-error Svelte does not publish declarations for this client entrypoint path.
import { mount, tick, unmount } from '../../../../node_modules/svelte/src/index-client.js';
import { afterEach, describe, expect, it, vi } from 'vitest';

import TransferProductMatrix from './TransferProductMatrix.svelte';

let component: Record<string, unknown> | undefined;
let target: HTMLDivElement | undefined;

afterEach(async () => {
  if (component) await unmount(component);
  target?.remove();
  component = undefined;
  target = undefined;
});

describe('TransferProductMatrix', () => {
  it('keeps a non-variant product as a simple quantity row', async () => {
    target = document.createElement('div');
    document.body.append(target);
    component = mount(TransferProductMatrix, {
      target,
      props: {
        productName: 'Koli',
        variantsEnabled: false,
        quantity: '',
        availableQuantity: '8'
      }
    }) as unknown as Record<string, unknown>;
    await tick();

    expect(target.textContent).toContain('8 ADET kullanılabilir');
    expect(target.textContent).toContain('Varyantsız stok');
    expect(target.querySelector('table')).toBeNull();
    expect(target.querySelector('input')).not.toBeNull();
  });

  it('renders one editable quantity cell per active variant', async () => {
    const onVariantQuantityChange = vi.fn();
    target = document.createElement('div');
    document.body.append(target);
    component = mount(TransferProductMatrix, {
      target,
      props: {
        productName: 'Tişört',
        variantsEnabled: true,
        variants: [
          {
            id: 'red-m',
            code: 'TSHIRT-RED-M',
            name: 'TSHIRT-RED-M',
            attributes: { Renk: 'Kırmızı', Beden: 'M' },
            physicalQuantity: '5',
            reservedQuantity: '1',
            availableQuantity: '4'
          },
          {
            id: 'blue-l',
            code: 'TSHIRT-BLUE-L',
            attributes: { Renk: 'Mavi', Beden: 'L' },
            availableQuantity: '0'
          }
        ],
        variantQuantities: {},
        onVariantQuantityChange
      }
    }) as unknown as Record<string, unknown>;
    await tick();

    expect(target.querySelectorAll('tbody tr')).toHaveLength(2);
    expect(target.textContent).toContain('Renk: Kırmızı · Beden: M');
    expect(target.textContent?.match(/TSHIRT-RED-M/g)).toHaveLength(1);
    expect(target.textContent).not.toContain('Fiziksel');
    expect(target.textContent).not.toContain('Rezerve');
    expect(target.textContent).not.toContain('Kullanılabilir');
    expect(target.textContent).toContain('4 ADET kullanılabilir');
    expect(target.textContent).toContain('Stok yok');
    expect(
      target.querySelector<HTMLInputElement>('[aria-label="Renk: Mavi · Beden: L miktarı"]')
        ?.disabled
    ).toBe(true);
    const quantity = target.querySelector<HTMLInputElement>(
      '[aria-label="Renk: Kırmızı · Beden: M miktarı"]'
    );
    expect(quantity).not.toBeNull();
    quantity!.value = '2';
    quantity!.dispatchEvent(new Event('input', { bubbles: true }));
    expect(onVariantQuantityChange).toHaveBeenCalledWith('red-m', '2');
  });
});
