<script lang="ts">
  import { Badge } from '$lib/components/ui/badge';
  let { value }: { value: unknown; row?: unknown } = $props();
  const labels: Record<string, string> = {
    customer: 'Müşteri',
    supplier: 'Tedarikçi',
    both: 'Müşteri + Tedarikçi'
  };
</script>

<Badge tone="info">{labels[String(value)] ?? String(value)}</Badge>
