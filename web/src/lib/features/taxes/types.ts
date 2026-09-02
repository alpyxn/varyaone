export type TaxDefinition = {
  id: string;
  company_id: string;
  code: string;
  name: string;
  description: string;
  source: string;
  source_reference: string;
  source_version: string;
  rate?: string;
  calculation_type?: 'PERCENTAGE' | 'QUANTITY_BASED';
  metadata: Record<string, unknown>;
  is_active: boolean;
  version: number;
};

export type TaxRate = {
  id: string;
  company_id: string;
  tax_definition_id: string;
  rate: string;
  valid_from: string;
  valid_to?: string;
  source: string;
  source_reference: string;
  source_version: string;
  metadata: Record<string, unknown>;
  version: number;
};

export type TaxExemption = {
  id: string;
  company_id: string;
  code: string;
  name: string;
  legal_basis: string;
  source: string;
  source_reference: string;
  source_version: string;
  valid_from: string;
  valid_to?: string;
  metadata: Record<string, unknown>;
  is_active: boolean;
  version: number;
};

export type WithholdingRule = {
  id: string;
  company_id: string;
  code: string;
  name: string;
  rate: string;
  ratio_numerator?: number;
  ratio_denominator?: number;
  legal_basis: string;
  source: string;
  source_reference: string;
  source_version: string;
  valid_from: string;
  valid_to?: string;
  metadata: Record<string, unknown>;
  is_active: boolean;
  version: number;
};
