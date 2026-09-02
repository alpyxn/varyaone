export const assetStatuses = ['AVAILABLE', 'ASSIGNED', 'MAINTENANCE', 'RETIRED'] as const;
export type AssetStatus = (typeof assetStatuses)[number];

/** Statuses a user may set directly on the card. ASSIGNED is reached only by a zimmet. */
export const editableAssetStatuses = ['AVAILABLE', 'MAINTENANCE', 'RETIRED'] as const;

export type AssetAssignedTo = {
  assignment_id: string;
  employee_id: string;
  employee_code: string;
  employee_name: string;
  assigned_at: string;
};

export type FixedAsset = {
  id: string;
  asset_code: string;
  name: string;
  category: string;
  serial_number?: string | null;
  description: string;
  status: AssetStatus;
  assigned_to?: AssetAssignedTo | null;
  created_at: string;
  updated_at: string;
  version: number;
};

export type FixedAssetInput = {
  asset_code: string;
  name: string;
  category: string;
  serial_number: string;
  description: string;
  status: AssetStatus;
};

export type FixedAssetList = { items: FixedAsset[]; next_cursor?: string };

export type AssetAssignment = {
  id: string;
  asset_id: string;
  asset_code: string;
  asset_name: string;
  employee_id: string;
  employee_code: string;
  employee_name: string;
  assigned_at: string;
  returned_at?: string | null;
  assignment_note: string;
  return_note?: string | null;
};

export type FixedAssetCategory = {
  id: string;
  code: string;
  name: string;
  description: string;
  is_system: boolean;
  is_active: boolean;
  archived_at?: string | null;
  created_at: string;
  version: number;
};

export type FixedAssetCategoryInput = { code: string; name: string; description: string };

export type AssignInput = { employee_id: string; assigned_at: string; note: string };
export type ReturnInput = { returned_at: string; note: string };

export function assetStatusLabel(status: string): string {
  switch (status) {
    case 'AVAILABLE':
      return 'Uygun';
    case 'ASSIGNED':
      return 'Zimmetli';
    case 'MAINTENANCE':
      return 'Bakımda';
    case 'RETIRED':
      return 'Hurdaya ayrıldı';
    default:
      return status;
  }
}

export function toAssetInput(
  asset: Pick<
    FixedAsset,
    'asset_code' | 'name' | 'category' | 'serial_number' | 'description' | 'status'
  >
): FixedAssetInput {
  return {
    asset_code: asset.asset_code.trim(),
    name: asset.name.trim(),
    category: asset.category.trim(),
    serial_number: (asset.serial_number ?? '').trim(),
    description: (asset.description ?? '').trim(),
    status: asset.status
  };
}
