export type PricingTaxMode = 'INCLUSIVE' | 'EXCLUSIVE';
export type PricingRoundPolicy = 'HALF_UP' | 'HALF_EVEN' | 'DOWN' | 'UP';

export type PricingCurrency = {
  company_id: string;
  code: string;
  name: string;
  symbol: string;
  minor_unit: number;
  is_custom: boolean;
  source: string;
  is_active: boolean;
  version: number;
};

export type PriceList = {
  id: string;
  company_id: string;
  code: string;
  name: string;
  description: string;
  applies_to_all_categories: boolean;
  scope_category_id?: string;
  currency_code: string;
  tax_mode: PricingTaxMode;
  round_policy: PricingRoundPolicy;
  round_scale: number;
  is_active: boolean;
  version: number;
};

export type PriceListEntry = {
  id: string;
  company_id: string;
  price_list_id: string;
  item_id: string;
  valid_from: string;
  valid_to?: string;
  unit_price: string;
  version: number;
};

export type ExchangeRateSettings = {
  company_id: string;
  source_preference: 'AUTO' | 'TCMB' | 'ECB';
  refresh_interval_hours: number;
  last_attempt_at?: string;
  last_success_at?: string;
  last_rate_date?: string;
  last_source?: string;
  last_error?: string;
  version: number;
};

export type ExchangeRate = {
  company_id: string;
  currency_code: string;
  base_currency: string;
  rate_to_base: string;
  rate_date: string;
  source: string;
  source_url: string;
  fetched_at: string;
};

export type ExchangeRateDashboard = {
  base_currency: string;
  settings: ExchangeRateSettings;
  items: ExchangeRate[];
};
