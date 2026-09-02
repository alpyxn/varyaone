/**
 * The currencies the UI offers wherever a currency must be chosen. Kept as a
 * small static list so every picker is consistent and no free-text currency
 * codes leak into the API. Extend here if the business adds a currency.
 */
export type CurrencyOption = { code: string; name: string; symbol: string };

export const SUPPORTED_CURRENCIES: CurrencyOption[] = [
  { code: 'TRY', name: 'Türk lirası', symbol: '₺' },
  { code: 'USD', name: 'ABD doları', symbol: '$' },
  { code: 'EUR', name: 'Euro', symbol: '€' },
  { code: 'GBP', name: 'İngiliz sterlini', symbol: '£' }
];

export const SUPPORTED_CURRENCY_CODES = SUPPORTED_CURRENCIES.map((c) => c.code);

export function currencyLabel(code: string): string {
  const match = SUPPORTED_CURRENCIES.find((c) => c.code === code);
  return match ? `${match.code} · ${match.name}` : code;
}
