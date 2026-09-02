import { beforeEach, describe, expect, it, vi } from 'vitest';

const { apiMock } = vi.hoisted(() => ({ apiMock: vi.fn() }));
vi.mock('$lib/api', () => ({ api: apiMock }));

import { activateParty, activatePartyGroup, createParty, listTaxOfficeReferences } from './api';
import { emptyParty } from './types';

beforeEach(() => apiMock.mockReset());

describe('vergi dairesi API adaptörü', () => {
  it('sends the province, district, search and limit filters with the request signal', async () => {
    apiMock.mockResolvedValueOnce({ items: [] });
    const controller = new AbortController();

    await listTaxOfficeReferences(
      { province_id: '34', district_name: 'Kadıköy', q: 'kadık', limit: 2000 },
      controller.signal
    );

    expect(apiMock).toHaveBeenCalledWith(
      '/tax-office-references?province_id=34&district_name=Kad%C4%B1k%C3%B6y&q=kad%C4%B1k&limit=2000',
      { signal: controller.signal }
    );
  });

  it('keeps the selected identity and canonical name in the cari payload', async () => {
    apiMock.mockResolvedValueOnce({ id: 'party-1' });
    const input = emptyParty();
    input.legal_name = 'Örnek AŞ';
    input.tax_office_id = '5fa193cf-a61b-50bd-b457-f265ea5897ea';
    input.tax_office = 'Adana İhtisas Vergi Dairesi Müdürlüğü';

    await createParty(input);

    const request = apiMock.mock.calls[0][1] as { body: string };
    expect(JSON.parse(request.body)).toMatchObject({
      tax_office_id: input.tax_office_id,
      tax_office: input.tax_office
    });
  });

  it('activates a pasif cari with optimistic locking', async () => {
    apiMock.mockResolvedValueOnce({ id: 'party-1', is_active: true, version: 4 });

    await activateParty('party-1', 3);

    expect(apiMock).toHaveBeenCalledWith('/parties/party-1/activate', {
      method: 'POST',
      headers: { 'If-Match': '"3"' },
      body: '{}'
    });
  });

  it('activates a pasif cari grubunu with optimistic locking', async () => {
    apiMock.mockResolvedValueOnce({ id: 'group-1', is_active: true, version: 4 });

    await activatePartyGroup('group-1', 3);

    expect(apiMock).toHaveBeenCalledWith('/party-settings/groups/group-1/activate', {
      method: 'POST',
      headers: { 'If-Match': '"3"' },
      body: '{}'
    });
  });

  it('serializes a legacy free-text office without inventing an identity', async () => {
    apiMock.mockResolvedValueOnce({ id: 'party-2' });
    const input = emptyParty();
    input.legal_name = 'Eski Kayıt AŞ';
    input.tax_office = 'Elle girilmiş vergi dairesi';

    await createParty(input);

    expect(JSON.parse((apiMock.mock.calls[0][1] as { body: string }).body)).toMatchObject({
      tax_office: 'Elle girilmiş vergi dairesi',
      tax_office_id: null
    });
  });
});
