<script lang="ts">
  import { DateInput } from '$lib/components/varya/date-input';
  let {
    start = $bindable(''),
    end = $bindable(''),
    label = 'Tarih aralığı'
  }: { start?: string; end?: string; label?: string } = $props();
</script>

<fieldset>
  <legend>{label}</legend><DateInput bind:value={start} ariaLabel="Başlangıç tarihi" /><span>—</span
  ><DateInput bind:value={end} ariaLabel="Bitiş tarihi" />
</fieldset>

<style>
  fieldset {
    display: flex;
    align-items: center;
    gap: 5px;
    margin: 0;
    padding: 0;
    border: 0;
  }
  legend {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
  }
  span {
    color: var(--text-muted);
  }
</style>
