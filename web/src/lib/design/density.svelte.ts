// Varya One uses one predictable desktop data-entry density. Keeping this as a
// small preference object preserves the component contract without exposing a
// setting that changes row geometry from screen to screen.
export const densities = ['compact'] as const;
export type Density = (typeof densities)[number];

export const densityLabels: Record<Density, string> = {
  compact: 'Kompakt'
};

const STORAGE_KEY = 'varyaone:density';

class DensityPreference {
  value = $state<Density>('compact');

  load() {
    // Older builds stored comfortable/dense values. They intentionally no
    // longer affect the shared geometry after the fixed-density revision.
    if (typeof localStorage !== 'undefined') localStorage.setItem(STORAGE_KEY, 'compact');
    this.value = 'compact';
  }

  set(value: Density) {
    this.value = value;
    if (typeof localStorage !== 'undefined') localStorage.setItem(STORAGE_KEY, value);
  }
}

export const densityPreference = new DensityPreference();
