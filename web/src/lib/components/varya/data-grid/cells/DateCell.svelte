<script lang="ts">
  import { formatDate } from '$lib/design/formatters';
  let { value, includeTime = false }: { value: unknown; row?: unknown; includeTime?: boolean } =
    $props();
</script>

<span
  >{formatDate(
    typeof value === 'string' || value instanceof Date ? value : undefined,
    includeTime
  )}</span
>
