import { api } from '$lib/api';
import type { TaxDefinition, TaxExemption, TaxRate, WithholdingRule } from './types';

type ListResponse<T> = { items: T[] };

export const listTaxDefinitions = (includeInactive = true) =>
  api<ListResponse<TaxDefinition>>(
    `/taxes/definitions?include_inactive=${includeInactive ? 'true' : 'false'}`
  );

export const createTaxDefinition = (input: Partial<TaxDefinition>) =>
  api<TaxDefinition>('/taxes/definitions', {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const listTaxRates = (definitionID: string, on?: string) =>
  api<ListResponse<TaxRate>>(
    `/taxes/definitions/${encodeURIComponent(definitionID)}/rates${on ? `?on=${encodeURIComponent(on)}` : ''}`
  );

export const listTaxExemptions = (includeInactive = false) =>
  api<ListResponse<TaxExemption>>(
    `/taxes/exemptions?include_inactive=${includeInactive ? 'true' : 'false'}`
  );

export const listWithholdingRules = (includeInactive = false) =>
  api<ListResponse<WithholdingRule>>(
    `/taxes/withholding-rules?include_inactive=${includeInactive ? 'true' : 'false'}`
  );

export const createTaxRate = (definitionID: string, input: Partial<TaxRate>) =>
  api<TaxRate>(`/taxes/definitions/${encodeURIComponent(definitionID)}/rates`, {
    method: 'POST',
    body: JSON.stringify(input)
  });
