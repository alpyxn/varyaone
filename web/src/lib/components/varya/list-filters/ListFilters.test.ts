import { afterEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ListFilters from './ListFilters.svelte';
let component: ReturnType<typeof mount>;
afterEach(async () => {
  if (component) await unmount(component);
  document.body.innerHTML = '';
});
it('keeps hidden active filters visible as removable labels and expands secondary fields', () => {
  const target = document.createElement('div');
  document.body.append(target);
  const onChange = vi.fn();
  const onClear = vi.fn();
  component = mount(ListFilters, {
    target,
    props: {
      filters: [
        {
          field: 'status',
          label: 'Durum',
          kind: 'select',
          options: [{ value: 'OPEN', label: 'Açık' }]
        },
        { field: 'currency', label: 'Para birimi', kind: 'currency' }
      ],
      primaryCount: 1,
      values: { currency: 'EUR' },
      onChange,
      onClear
    }
  });
  flushSync();
  expect(target.querySelector('input')).toBeNull();
  expect(target.textContent).toContain('Para birimi: EUR');
  target.querySelector<HTMLButtonElement>('[aria-expanded]')!.click();
  flushSync();
  expect(target.querySelector('input')).toBeNull();
  const currency = target.querySelector<HTMLSelectElement>('select[aria-label="Para birimi"]')!;
  expect(currency.value).toBe('EUR');
  expect(Array.from(currency.options, (option) => option.value)).toEqual([
    '',
    'TRY',
    'USD',
    'EUR',
    'GBP'
  ]);
  currency.value = 'USD';
  currency.dispatchEvent(new Event('change', { bubbles: true }));
  expect(onChange).toHaveBeenCalledWith('currency', 'USD');
  target.querySelector<HTMLButtonElement>('[aria-label="Para birimi filtresini temizle"]')!.click();
  expect(onChange).toHaveBeenCalledWith('currency', '');
});
