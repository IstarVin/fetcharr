<script lang="ts">
  import { api, ApiError, errorMessage } from '../lib/api'
  import { heading } from '../lib/format'
  import { headerRowsValid, headersRecord, newHeader, newSubtitle, subtitleRowsValid, subtitlesList, validHttpUrl, type HeaderRow, type SubtitleRow } from '../lib/rows'
  import { confirm, live, toasts } from '../lib/store.svelte'
  import type { HeadersMode, Job } from '../lib/types'
  import Dialog from './Dialog.svelte'
  import HeaderRows from './HeaderRows.svelte'
  import SubtitleRows from './SubtitleRows.svelte'

  let { job, onclose }: { job: Job | null; onclose: () => void } = $props()

  // The draft lives only in this component; saved headers are never fetched.
  let url = $state('')
  let mode = $state<HeadersMode>('clear')
  let headers = $state<HeaderRow[]>([])
  let subtitles = $state<SubtitleRow[]>([])
  let submitting = $state(false)
  let error = $state<string | null>(null)

  $effect(() => {
    const j = job
    if (!j) return
    url = j.url
    mode = 'clear'
    headers = [newHeader()]
    subtitles = j.subtitles.map((s) => newSubtitle(s.url, s.lang))
    error = null
    submitting = false
  })

  const urlValid = $derived(validHttpUrl(url))
  const canSubmit = $derived(!submitting && urlValid && (mode !== 'replace' || headerRowsValid(headers)) && subtitleRowsValid(subtitles))

  const MODES: { mode: HeadersMode; label: string }[] = [
    { mode: 'clear', label: 'Clear saved headers' },
    { mode: 'keep', label: 'Keep saved headers' },
    { mode: 'replace', label: 'Replace headers' },
  ]

  async function requestClose() {
    if (submitting) {
      const leave = await confirm({
        title: 'Leave while submitting?',
        body: 'The server may still replace the source and start the download. Check the queue before trying again.',
        confirm: 'Leave',
        cancel: 'Stay',
      })
      if (!leave) return
    }
    onclose()
  }

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    const j = job
    if (!j || !canSubmit) return
    let overwrite = false
    if (j.error_code === 'already_exists') {
      overwrite = await confirm({ title: 'File already exists', body: 'A file with this name already exists. Replace it?', confirm: 'Replace', cancel: 'Stay' })
      if (!overwrite) return
    }
    submitting = true
    error = null
    try {
      const updated = await api.retry(j.id, {
        overwrite,
        source: {
          url: url.trim(),
          headers_mode: mode,
          ...(mode === 'replace' ? { headers: headersRecord(headers) ?? {} } : {}),
          subtitles: subtitlesList(subtitles),
        },
      })
      live.upsert(updated)
      toasts.show('Added to queue')
      onclose()
    } catch (err) {
      error = err instanceof ApiError && err.code === 'duplicate' ? 'This URL is already in the queue.' : errorMessage(err)
    } finally {
      submitting = false
    }
  }
</script>

<Dialog open={!!job} title="Replace source" wide onclose={requestClose}>
  <form id="source-form" class="form" onsubmit={submit}>
    <p class="name">{job ? heading(job) : ''}</p>
    <label class="field">
      <span>Source URL</span>
      <input type="url" bind:value={url} disabled={submitting} aria-invalid={!urlValid ? 'true' : undefined} spellcheck="false" />
      {#if !urlValid}<span class="hint error-text">Enter a valid HTTP or HTTPS URL</span>{/if}
    </label>
    <fieldset>
      <legend>Request headers</legend>
      <div class="chips" role="radiogroup" aria-label="Request headers">
        {#each MODES as m (m.mode)}
          <button type="button" class="chip" role="radio" aria-checked={mode === m.mode} disabled={submitting} onclick={() => (mode = m.mode)}>{m.label}</button>
        {/each}
      </div>
      {#if mode === 'keep'}<p class="hint">The headers saved with this download are reused. They are never shown here.</p>{/if}
      {#if mode === 'replace'}<HeaderRows bind:rows={headers} disabled={submitting} />{/if}
    </fieldset>
    <fieldset>
      <legend>Subtitles</legend>
      <SubtitleRows bind:rows={subtitles} disabled={submitting} />
    </fieldset>
    <p class="hint">Starts a new download. Previous partial files will be discarded.</p>
    {#if error}<p class="error-text" role="alert">{error}</p>{/if}
  </form>
  {#snippet actions()}
    <button class="btn" type="button" onclick={requestClose}>Cancel</button>
    <button class="btn filled" type="submit" form="source-form" disabled={!canSubmit}>{submitting ? 'Adding to queue…' : 'Replace source & retry'}</button>
  {/snippet}
</Dialog>

<style>
  .form {
    display: grid;
    gap: 18px;
    color: var(--fg);
  }

  .name {
    font-weight: 600;
  }

  fieldset {
    display: grid;
    gap: 10px;
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }

  legend {
    padding: 0;
    margin-bottom: 8px;
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--fg-muted);
  }

  .hint {
    font-size: 0.82rem;
    color: var(--fg-muted);
  }
</style>
