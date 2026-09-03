import { api, type Session } from '$lib/api';
import {
  TURKEY_PROVINCES,
  type Address,
  type LocationOption,
  type LocationSelection,
  type Party,
  type PartyOpenItem,
  type PartyBalanceList,
  type PartyStatementList,
  type PartyStatementReport,
  type PartyOpenItemList,
  type PartyAgingReport,
  type PartyInput,
  type PartyList,
  type PartyLocationDefaults,
  type TaxOfficeReference
} from './types';

type ReferenceResponse<T> = { items: T[] };
type ProvinceReference = { id: number; plate_code: string; name: string };
type DistrictReference = { id: number; province_id: number; name: string };
type NeighborhoodReference = { id: number; district_id: number; name: string };
type AddressPreferenceResponse = {
  province_id?: number;
  province_name?: string;
  district_id?: number;
  district_name?: string;
  neighborhood_id?: number;
  neighborhood_name?: string;
};

export type TaxOfficeReferenceList = { items: TaxOfficeReference[] };
export type TaxOfficeReferenceQuery = {
  province_id?: string | number;
  district_name?: string;
  q?: string;
  limit?: number;
};

export const listProvinces = async (signal?: AbortSignal): Promise<LocationOption[]> => {
  try {
    const result = await api<ReferenceResponse<ProvinceReference>>(
      '/address-references/provinces',
      {
        signal
      }
    );
    if (result.items.length) {
      return result.items.map((item) => ({ id: String(item.id), name: item.name }));
    }
  } catch {
    // Fall through to the bundled 81-province list.
  }
  return TURKEY_PROVINCES;
};

export const listDistricts = async (provinceID: string, signal?: AbortSignal) => {
  const result = await api<ReferenceResponse<DistrictReference>>(
    `/address-references/provinces/${encodeURIComponent(provinceID)}/districts?limit=2000`,
    { signal }
  );
  return result.items.map((item) => ({ id: String(item.id), name: item.name }));
};

export const listNeighborhoods = async (districtID: string, signal?: AbortSignal) => {
  const result = await api<ReferenceResponse<NeighborhoodReference>>(
    `/address-references/districts/${encodeURIComponent(districtID)}/neighborhoods?limit=2000`,
    { signal }
  );
  return result.items.map((item) => ({ id: String(item.id), name: item.name }));
};

export const listTaxOfficeReferences = (
  query: TaxOfficeReferenceQuery = {},
  signal?: AbortSignal
) => {
  const params = new URLSearchParams();
  if (query.province_id !== undefined && String(query.province_id).trim()) {
    params.set('province_id', String(query.province_id).trim());
  }
  if (query.district_name?.trim()) params.set('district_name', query.district_name.trim());
  if (query.q?.trim()) params.set('q', query.q.trim());
  if (query.limit !== undefined) params.set('limit', String(query.limit));
  const suffix = params.toString();
  return api<TaxOfficeReferenceList>(`/tax-office-references${suffix ? `?${suffix}` : ''}`, {
    signal
  });
};

const emptyLocationDefaults = (): PartyLocationDefaults => ({});

function locationDefaultsKey(session: Session): string {
  return `varyaone:party-location-defaults:${session.user.id}:${session.current_company_id || 'none'}`;
}

function readLocalLocationDefaults(session: Session): PartyLocationDefaults {
  if (typeof localStorage === 'undefined') return emptyLocationDefaults();
  try {
    const raw = localStorage.getItem(locationDefaultsKey(session));
    if (!raw) return emptyLocationDefaults();
    const value = JSON.parse(raw) as PartyLocationDefaults;
    return value && typeof value === 'object' ? value : emptyLocationDefaults();
  } catch {
    return emptyLocationDefaults();
  }
}

export async function getPartyLocationDefaults(
  session: Session,
  signal?: AbortSignal
): Promise<PartyLocationDefaults> {
  const fallback = readLocalLocationDefaults(session);
  try {
    const value = await api<AddressPreferenceResponse>('/address-preferences/default', { signal });
    const defaults: PartyLocationDefaults = {};
    if (value.province_name) {
      defaults.city = {
        id: String(value.province_id ?? value.province_name),
        name: value.province_name
      };
    }
    if (value.district_name) {
      defaults.district = {
        id: String(value.district_id ?? value.district_name),
        name: value.district_name,
        parent_id: String(value.province_id ?? value.province_name ?? '')
      };
    }
    if (value.neighborhood_name) {
      defaults.neighborhood = {
        id: String(value.neighborhood_id ?? value.neighborhood_name),
        name: value.neighborhood_name,
        parent_id: String(value.district_id ?? value.district_name ?? '')
      };
    }
    return Object.keys(defaults).length ? defaults : fallback;
  } catch {
    // Keep the local preference fallback if the preference request is unavailable.
    return fallback;
  }
}

export async function savePartyLocationDefault(
  session: Session,
  level: keyof PartyLocationDefaults,
  selection: LocationSelection
): Promise<PartyLocationDefaults> {
  const next = readLocalLocationDefaults(session);
  if (level === 'city') {
    next.city = selection;
    if (next.district?.parent_id && next.district.parent_id !== selection.id) delete next.district;
    delete next.neighborhood;
  } else if (level === 'district') {
    next.district = selection;
    if (next.neighborhood?.parent_id && next.neighborhood.parent_id !== selection.id) {
      delete next.neighborhood;
    }
  } else {
    next.neighborhood = selection;
  }
  if (typeof localStorage !== 'undefined') {
    try {
      localStorage.setItem(locationDefaultsKey(session), JSON.stringify(next));
    } catch {
      // Preferences are helpful but must never block saving a cari card.
    }
  }
  try {
    const saved = await api<AddressPreferenceResponse>('/address-preferences/default', {
      method: 'PUT',
      body: JSON.stringify({
        // Names are the durable fallback while the optional district and
        // neighbourhood reference seed is being installed. This also keeps
        // preferences valid for remote location catalogs.
        province_name: next.city?.name,
        district_name: next.district?.name,
        neighborhood_name: next.neighborhood?.name
      })
    });
    const result: PartyLocationDefaults = { ...next };
    if (saved.province_name)
      result.city = {
        id: String(saved.province_id ?? next.city?.id ?? saved.province_name),
        name: saved.province_name
      };
    if (saved.district_name)
      result.district = {
        id: String(saved.district_id ?? next.district?.id ?? saved.district_name),
        name: saved.district_name,
        parent_id: result.city?.id
      };
    if (saved.neighborhood_name)
      result.neighborhood = {
        id: String(saved.neighborhood_id ?? next.neighborhood?.id ?? saved.neighborhood_name),
        name: saved.neighborhood_name,
        parent_id: result.district?.id
      };
    return result;
  } catch {
    // Keep the local fallback. A missing endpoint must not break card entry.
  }
  return next;
}

function toAPIAddress(address: Address) {
  const {
    neighborhood,
    city_id: _cityID,
    district_id: _districtID,
    neighborhood_id: _neighborhoodID,
    province_id: _provinceID,
    province_name: _provinceName,
    district_name: _districtName,
    neighborhood_name: _neighborhoodName,
    ...payload
  } = address;
  const integerID = (value: string | undefined) =>
    value && /^\d+$/.test(value) ? Number(value) : undefined;
  return {
    ...payload,
    neighborhood,
    province_id: integerID(address.city_id ?? address.province_id),
    district_id: integerID(address.district_id),
    neighborhood_id: integerID(address.neighborhood_id)
  };
}

function toAPIPartyInput(input: PartyInput) {
  return {
    ...input,
    tax_office_id: input.tax_office_id || null,
    addresses: input.addresses.slice(0, 1).map(toAPIAddress)
  };
}

export type PartyGroup = {
  id: string;
  code: string;
  name: string;
  is_active: boolean;
  version: number;
};

export type PartyGroupList = { items: PartyGroup[] };

export const listParties = (params: URLSearchParams, signal?: AbortSignal) =>
  api<PartyList>(`/parties?${params}`, { signal });
export const getParty = (id: string, signal?: AbortSignal) =>
  api<Party>(`/parties/${id}`, { signal });
export const getPartyBalances = (id: string, signal?: AbortSignal) =>
  api<PartyBalanceList>(`/parties/${id}/balances`, { signal });
export const getPartyStatement = (
  id: string,
  params = new URLSearchParams(),
  signal?: AbortSignal
) => api<PartyStatementList>(`/parties/${id}/statement?${params}`, { signal });
export const getPartyStatementReport = (
  id: string,
  params = new URLSearchParams(),
  signal?: AbortSignal
) => api<PartyStatementReport>(`/parties/${id}/statement?${params}`, { signal });
export const getPartyOpenItems = (
  id: string,
  params = new URLSearchParams(),
  signal?: AbortSignal
) => {
  // Open-item totals must not depend on the server's first-page default. Walk
  // the deterministic cursor until the complete projection is available.
  return (async (): Promise<PartyOpenItemList> => {
    const query = new URLSearchParams(params);
    query.set('party_id', id);
    if (!query.has('limit')) query.set('limit', '500');
    const items: PartyOpenItem[] = [];
    let cursor = query.get('cursor') || '';
    let lastCursor = '';
    let remainingCursor: string | undefined;
    while (true) {
      if (cursor) query.set('cursor', cursor);
      else query.delete('cursor');
      const page = await api<PartyOpenItemList>(`/invoice-open-items?${query}`, { signal });
      if (!Array.isArray(page.items)) throw new Error('Açık kalem yanıtı geçersiz.');
      items.push(...page.items);
      remainingCursor = page.next_cursor;
      if (!remainingCursor || remainingCursor === lastCursor) break;
      lastCursor = remainingCursor;
      cursor = remainingCursor;
    }
    return { items, ...(remainingCursor ? { next_cursor: remainingCursor } : {}) };
  })();
};
export const createParty = (input: PartyInput) =>
  api<Party>('/parties', { method: 'POST', body: JSON.stringify(toAPIPartyInput(input)) });
export const updateParty = (id: string, version: number, input: PartyInput) =>
  api<Party>(`/parties/${id}`, {
    method: 'PUT',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(toAPIPartyInput(input))
  });
export const deactivateParty = (id: string, version: number) =>
  api<Party>(`/parties/${id}/deactivate`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

export const activateParty = (id: string, version: number) =>
  api<Party>(`/parties/${id}/activate`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

export const listPartyGroups = (signal?: AbortSignal) =>
  api<PartyGroupList>('/party-settings/groups', { signal });

export const createPartyGroup = (input: Pick<PartyGroup, 'code' | 'name'>) =>
  api<PartyGroup>('/party-settings/groups', {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const updatePartyGroup = (
  id: string,
  version: number,
  input: Pick<PartyGroup, 'code' | 'name'>
) =>
  api<PartyGroup>(`/party-settings/groups/${id}`, {
    method: 'PUT',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const deactivatePartyGroup = (id: string, version: number) =>
  api<PartyGroup>(`/party-settings/groups/${id}/deactivate`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

export const activatePartyGroup = (id: string, version: number) =>
  api<PartyGroup>(`/party-settings/groups/${id}/activate`, {
    method: 'POST',
    headers: { 'If-Match': `"${version}"` },
    body: '{}'
  });

/** Cari yaşlandırma: açık fatura bakiyelerinin vade kovalarına dağılımı. */
export const getPartyAging = (params = new URLSearchParams(), signal?: AbortSignal) => {
  const query = params.toString();
  return api<PartyAgingReport>(`/finance/party-aging${query ? `?${query}` : ''}`, { signal });
};
