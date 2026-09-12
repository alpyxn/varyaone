import type {
  EntitySearchHandler,
  EntityOption
} from '$lib/components/varya/entity-picker-dialog/types';
export type ListFilter = {
  field: string;
  label: string;
  kind: 'date' | 'select' | 'entity' | 'text' | 'currency';
  inputMode?: 'text' | 'decimal';
  visibleWhen?: { field: string; value: string };
  options?: { value: string; label: string }[];
  placeholder?: string;
  entity?: {
    title: string;
    description: string;
    triggerPlaceholder: string;
    searchPlaceholder?: string;
    search: EntitySearchHandler<EntityOption>;
  };
};
