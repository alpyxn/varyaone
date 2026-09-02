import { api } from '$lib/api';
import type {
  AssetAssignment,
  AssignInput,
  FixedAsset,
  FixedAssetCategory,
  FixedAssetCategoryInput,
  FixedAssetInput,
  FixedAssetList,
  ReturnInput
} from './types';

type ListOptions = {
  q?: string;
  status?: string;
  category?: string;
  cursor?: string;
  limit?: number;
  signal?: AbortSignal;
};

export const listFixedAssets = (options: ListOptions = {}) => {
  const params = new URLSearchParams();
  if (options.q) params.set('q', options.q);
  if (options.status) params.set('status', options.status);
  if (options.category) params.set('category', options.category);
  if (options.cursor) params.set('cursor', options.cursor);
  if (options.limit) params.set('limit', String(options.limit));
  const query = params.toString();
  return api<FixedAssetList>(`/fixed-assets${query ? `?${query}` : ''}`, {
    signal: options.signal
  });
};

export const getFixedAsset = (id: string, signal?: AbortSignal) =>
  api<FixedAsset>(`/fixed-assets/${encodeURIComponent(id)}`, { signal });

export const createFixedAsset = (input: FixedAssetInput) =>
  api<FixedAsset>('/fixed-assets', { method: 'POST', body: JSON.stringify(input) });

export const updateFixedAsset = (id: string, version: number, input: FixedAssetInput) =>
  api<FixedAsset>(`/fixed-assets/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const listFixedAssetCategories = (includeArchived = false, signal?: AbortSignal) =>
  api<{ items: FixedAssetCategory[] }>(
    `/fixed-asset-categories${includeArchived ? '?include_archived=true' : ''}`,
    { signal }
  );

export const createFixedAssetCategory = (input: FixedAssetCategoryInput) =>
  api<FixedAssetCategory>('/fixed-asset-categories', {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const updateFixedAssetCategory = (
  id: string,
  version: number,
  input: FixedAssetCategoryInput
) =>
  api<FixedAssetCategory>(`/fixed-asset-categories/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'If-Match': `"${version}"` },
    body: JSON.stringify(input)
  });

export const setFixedAssetCategoryActive = (id: string, active: boolean) =>
  api<FixedAssetCategory>(`/fixed-asset-categories/${encodeURIComponent(id)}/status`, {
    method: 'POST',
    body: JSON.stringify({ active })
  });

export const listAssetAssignments = (assetID: string, signal?: AbortSignal) =>
  api<{ items: AssetAssignment[] }>(`/fixed-assets/${encodeURIComponent(assetID)}/assignments`, {
    signal
  });

export const listEmployeeAssetAssignments = (employeeID: string, signal?: AbortSignal) =>
  api<{ items: AssetAssignment[] }>(
    `/hr/employees/${encodeURIComponent(employeeID)}/asset-assignments`,
    { signal }
  );

export const assignFixedAsset = (assetID: string, input: AssignInput) =>
  api<FixedAsset>(`/fixed-assets/${encodeURIComponent(assetID)}/assignments`, {
    method: 'POST',
    body: JSON.stringify(input)
  });

export const returnFixedAsset = (assetID: string, assignmentID: string, input: ReturnInput) =>
  api<FixedAsset>(
    `/fixed-assets/${encodeURIComponent(assetID)}/assignments/${encodeURIComponent(assignmentID)}/return`,
    { method: 'POST', body: JSON.stringify(input) }
  );
