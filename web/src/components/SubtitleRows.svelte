<script lang="ts">
  import { newSubtitle, validHttpUrl, type SubtitleRow } from '../lib/rows'
  import Icon from './Icon.svelte'

  let { rows = $bindable(), disabled = false }: { rows: SubtitleRow[]; disabled?: boolean } = $props()
</script>

<div class="rows">
  {#each rows as row (row.id)}
    {@const bad = !!row.url.trim() && !validHttpUrl(row.url)}
    <div class="row">
      <label class="field">
        <span>Subtitle URL</span>
        <input type="url" bind:value={row.url} {disabled} aria-invalid={bad ? 'true' : undefined} spellcheck="false" placeholder="https://…/episode.en.vtt" />
      </label>
      <label class="field lang"><span>Language</span><input bind:value={row.lang} {disabled} placeholder="en" spellcheck="false" /></label>
      <button type="button" class="icon-btn" {disabled} aria-label="Remove subtitle" onclick={() => (rows = rows.filter((r) => r.id !== row.id))}><Icon name="close" size={18} /></button>
    </div>
    {#if bad}<p class="hint error-text">Subtitle URLs must start with http:// or https://</p>{/if}
  {/each}
  <div><button type="button" class="btn small outlined" {disabled} onclick={() => (rows = [...rows, newSubtitle()])}><Icon name="add" size={16} />Add subtitle</button></div>
</div>

<style>
  .rows {
    display: grid;
    gap: 10px;
  }

  .row {
    display: grid;
    grid-template-columns: minmax(0, 1fr) 96px auto;
    align-items: end;
    gap: 8px;
  }

  .hint {
    font-size: 0.82rem;
    margin-top: -4px;
  }
</style>
