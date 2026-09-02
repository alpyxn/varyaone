import { api } from '$lib/api';
import type {
  ExchangeRateDashboard,
  ExchangeRateSettings,
  PriceList,
  PriceListEntry,
  PricingCurrency
} from './types';

type ListResponse<T> = { items: T[] };

export const listPricingCurrencies = (includeInactive = true) =>
  api<ListResponse<PricingCurrency>>(
    `/pricing/currencies?include_inactive=${includeInactive ? 'true' : 'false'}`
  );

export const createPricingCurrency = (input: Partial<PricingCurrency>) =>
  api<PricingCurrency>('/pricing/currencies', {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const listPriceLists = (includeInactive = true) =>
  api<ListResponse<PriceList>>(
    `/price-lists?include_inactive=${includeInactive ? 'true' : 'false'}`
  );

export const createPriceList = (input: Partial<PriceList>) =>
  api<PriceList>('/price-lists', { method: 'POST', body: JSON.stringify(input) });

export const setPriceListActive = (id: string, version: number, active: boolean) =>
  api<PriceList>(`/price-lists/${encodeURIComponent(id)}/${active ? 'activate' : 'deactivate'}`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

export const listPriceEntries = (priceListID: string, itemID = '', on = '') => {
  const params = new URLSearchParams();
  if (itemID) params.set('item_id', itemID);
  if (on) params.set('on', on);
  const query = params.toString() ? `?${params.toString()}` : '';
  return api<ListResponse<PriceListEntry>>(
    `/price-lists/${encodeURIComponent(priceListID)}/entries${query}`
  );
};

export const createPriceEntry = (priceListID: string, input: Partial<PriceListEntry>) =>
  api<PriceListEntry>(`/price-lists/${encodeURIComponent(priceListID)}/entries`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const updatePriceEntry = (
  priceListID: string,
  entryID: string,
  version: number,
  input: Partial<PriceListEntry>
) =>
  api<PriceListEntry>(
    `/price-lists/${encodeURIComponent(priceListID)}/entries/${encodeURIComponent(entryID)}`,
    {
      method: 'PUT',
      headers: { 'If-Match': `"${version}"` },
      body: JSON.stringify(input)
    }
  );

export const getExchangeRateDashboard = () => api<ExchangeRateDashboard>('/exchange-rates');

export const updateExchangeRateSettings = (input: {
  source_preference: 'AUTO' | 'TCMB' | 'ECB';
  refresh_interval_hours: number;
}) =>
  api<ExchangeRateSettings>('/exchange-rates/settings', {
    method: 'PUT',
    body: JSON.stringify(input)
  });

export const refreshExchangeRates = () =>
  api<ExchangeRateDashboard>('/exchange-rates/refresh', { method: 'POST', body: '{}' });
