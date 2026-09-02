<script lang="ts">
  import { page } from '$app/state';
  import FinanceAccountForm from '$lib/features/finance/FinanceAccountForm.svelte';
</script>

<FinanceAccountForm mode="edit" accountId={page.params.id ?? ''} />
