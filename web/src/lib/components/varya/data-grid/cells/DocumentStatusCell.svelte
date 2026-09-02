<script lang="ts">
  import { DocumentStatus } from '$lib/components/varya/document-status';
  let { value }: { value: unknown; row?: unknown } = $props();
</script>

<DocumentStatus status={String(value ?? '')} />
