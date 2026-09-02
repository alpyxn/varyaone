<script lang="ts">
  import { Badge } from '$lib/components/ui/badge';
  let { value }: { value: unknown; row?: unknown } = $props();
  const labels: Record<string, string> = { ALLOW: 'İzin ver', WARN: 'Uyar', BLOCK: 'Engelle' };
</script>

<Badge tone={value === 'BLOCK' ? 'danger' : value === 'WARN' ? 'warning' : 'neutral'}
  >{labels[String(value)] ?? String(value)}</Badge
>
