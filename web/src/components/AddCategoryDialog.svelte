<script lang="ts">
  import { api, ApiError, errorMessage } from '../lib/api'
  import { LAYOUT_LABEL } from '../lib/format'
  import { newCategoryId } from '../lib/serverSettings'
  import { session } from '../lib/store.svelte'
  import type { Layout, ServerSettings } from '../lib/types'
  import Dialog from './Dialog.svelte'
  import Icon from './Icon.svelte'

  let {
    open,
    layout: initialLayout = 'movie',
    onclose,
    oncreated,
  }: {
    open: boolean
    /** The layout preselected when the dialog opens. */
    layout?: Layout
    onclose: () => void
    /** Called with the new category's id once the server saved it and session info is reloaded. */
    oncreated: (id: string) => void
  } = $props()

  const LAYOUTS: Layout[] = ['movie', 'tv', 'flat']

  let label = $state('')
  let path = $state('')
  let layout = $state<Layout>('movie')
  let saving = $state(false)
  let attempted = $state(false)
  let error = $state<string | null>(null)

  // Each opening starts from a blank form.
  $effect(() => {
    if (!open) return
    label = ''
    path = ''
    layout = initialLayout
    attempted = false
    error = null
  })

  const labelError = $derived(attempted && !label.trim() ? 'This field is required.' : undefined)
  const pathError = $derived(attempted && (!path.trim().startsWith('/') || path.includes('\u0000')) ? 'Enter an absolute server path beginning with /.' : undefined)

  /** Reads the latest document so other settings are kept; retries once if it changed in between. */
  async function append(category: ServerSettings['categories'][number]): Promise<void> {
    for (let attempt = 0; ; attempt++) {
      const doc = await api.getSettings()
      try {
        await api.saveSettings({ ...doc, categories: [...doc.categories, category] })
        return
      } catch (e) {
        if (attempt > 0 || !(e instanceof ApiError && e.code === 'settings_conflict')) throw e
      }
    }
  }

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (saving) return
    attempted = true
    error = null
    if (labelError || pathError) return
    saving = true
    const id = newCategoryId()
    try {
      await append({ id, label: label.trim(), path: path.trim(), layout })
      await session.refreshInfo()
      oncreated(id)
    } catch (err) {
      error = errorMessage(err)
    } finally {
      saving = false
    }
  }
</script>

<Dialog {open} title="New category" onclose={() => !saving && onclose()}>
  <form id="add-category" class="form" onsubmit={save} novalidate>
    <label class="field">
      <span>Label</span>
      <!-- svelte-ignore a11y_autofocus -->
      <input bind:value={label} autofocus aria-invalid={labelError ? 'true' : undefined} disabled={saving} />
      {#if labelError}<span class="hint error-text">{labelError}</span>{/if}
    </label>
    <label class="field">
      <span>Destination folder</span>
      <input bind:value={path} placeholder="/media/…" spellcheck="false" aria-invalid={pathError ? 'true' : undefined} disabled={saving} />
      <span class="hint" class:error-text={!!pathError}>{pathError ?? 'A folder on the server. It is created if missing.'}</span>
    </label>
    <div class="field">
      <span>Folder layout</span>
      <div class="chips" role="radiogroup" aria-label="Folder layout">
        {#each LAYOUTS as l (l)}
          <button type="button" class="chip" role="radio" aria-checked={layout === l} disabled={saving} onclick={() => (layout = l)}>
            <Icon name={l === 'tv' ? 'tv' : l === 'movie' ? 'movie' : 'folder'} size={16} />{LAYOUT_LABEL[l]}
          </button>
        {/each}
      </div>
    </div>
    {#if error}<div class="alert" role="alert"><Icon name="error" /><span>{error}</span></div>{/if}
  </form>
  {#snippet actions()}
    <button type="button" class="btn" onclick={onclose} disabled={saving}>Cancel</button>
    <button type="submit" form="add-category" class="btn filled" disabled={saving}>{saving ? 'Saving…' : 'Add'}</button>
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: 14px;
    color: var(--fg);
  }
</style>
