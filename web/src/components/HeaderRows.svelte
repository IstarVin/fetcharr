<script lang="ts">
  import { headerRowsValid, newHeader, type HeaderRow } from '../lib/rows'
  import Icon from './Icon.svelte'

  let { rows = $bindable(), disabled = false }: { rows: HeaderRow[]; disabled?: boolean } = $props()
  const valid = $derived(headerRowsValid(rows))
</script>

<div class="rows">
  {#each rows as row (row.id)}
    <div class="row">
      <label class="field"><span>Name</span><input bind:value={row.name} {disabled} autocomplete="off" spellcheck="false" placeholder="Referer" /></label>
      <label class="field"><span>Value</span><input bind:value={row.value} {disabled} autocomplete="off" spellcheck="false" /></label>
      <button type="button" class="icon-btn" {disabled} aria-label="Remove header" onclick={() => (rows = rows.filter((r) => r.id !== row.id))}><Icon name="close" size={18} /></button>
    </div>
  {/each}
  {#if !valid}<p class="hint error-text">Use valid, unique header names and values without control characters.</p>{/if}
  <div><button type="button" class="btn small outlined" {disabled} onclick={() => (rows = [...rows, newHeader()])}><Icon name="add" size={16} />Add header</button></div>
</div>

<style>
  .rows {
    display: grid;
    gap: 10px;
  }

  .row {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 3fr) auto;
    align-items: end;
    gap: 8px;
  }

  .hint {
    font-size: 0.82rem;
  }
</style>
