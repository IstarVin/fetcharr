<script lang="ts">
  import AddCategoryDialog from '../components/AddCategoryDialog.svelte'
  import HeaderRows from '../components/HeaderRows.svelte'
  import Icon from '../components/Icon.svelte'
  import SubtitleRows from '../components/SubtitleRows.svelte'
  import { api, ApiError, errorMessage } from '../lib/api'
  import { batchAddedMessage, LAYOUT_LABEL } from '../lib/format'
  import { href, router } from '../lib/router.svelte'
  import { headerRowsValid, headersRecord, newHeader, newSubtitle, rowId, subtitleRowsValid, subtitlesList, validHttpUrl, type HeaderRow, type SubtitleRow } from '../lib/rows'
  import { confirm, live, readPref, session, toasts } from '../lib/store.svelte'
  import type { Category } from '../lib/types'

  type Mode = 'single' | 'season'
  interface EpisodeRow {
    id: number
    episode: string
    title: string
    url: string
  }

  let mode = $state<Mode>('single')
  // Prefilled by `#/add?url=…` (bookmark, share target or another page).
  let url = $state(router.params.get('url') ?? '')
  let categoryId = $state<string | null>(null)
  let title = $state('')
  let showName = $state('')
  let season = $state('')
  let episode = $state('')
  let year = $state('')
  let headers = $state<HeaderRow[]>([])
  let subtitles = $state<SubtitleRow[]>([])
  let episodes = $state<EpisodeRow[]>([{ id: rowId(), episode: '1', title: '', url: '' }])
  let bulk = $state('')
  let submitting = $state(false)
  let error = $state<string | null>(null)
  let touched = $state(false)
  let addingCategory = $state(false)

  const categories = $derived(session.categories)
  const choices = $derived(mode === 'season' ? categories.filter((c) => c.layout === 'tv') : categories)
  const category = $derived<Category | undefined>(choices.find((c) => c.id === categoryId))
  const layout = $derived(category?.layout ?? null)
  const canAddCategory = $derived(!!session.info?.settings_supported)

  // Category preselection (Android §4.2): the per-browser default for the
  // kind of request, else the first category that fits.
  $effect(() => {
    if (category || choices.length === 0) return
    const pref = readPref(mode === 'season' ? 'defaultTvCategory' : 'defaultMovieCategory')
    categoryId = (choices.find((c) => c.id === pref) ?? choices[0])!.id
  })

  const intOk = (v: string, min = 0) => /^\d+$/.test(v.trim()) && Number(v) >= min
  const yearOk = $derived(!year.trim() || /^\d{4}$/.test(year.trim()))

  const singleErrors = $derived.by(() => {
    const e: Record<string, string> = {}
    if (!validHttpUrl(url)) e.url = 'Enter an http:// or https:// address'
    if (layout === 'tv') {
      if (!showName.trim()) e.show = 'Required'
      if (!intOk(season)) e.season = 'Required'
      if (!intOk(episode)) e.episode = 'Required'
    } else if (!title.trim()) {
      e.title = 'Required'
    }
    if (!yearOk) e.year = 'Use a 4-digit year'
    return e
  })

  const seasonErrors = $derived.by(() => {
    const e: Record<string, string> = {}
    if (!showName.trim()) e.show = 'Required'
    if (!intOk(season)) e.season = 'Required'
    if (!yearOk) e.year = 'Use a 4-digit year'
    const used = episodes.filter((r) => r.url.trim() || r.title.trim())
    if (used.length === 0) e.episodes = 'Add at least one episode URL'
    const nums = used.map((r) => r.episode.trim())
    if (used.some((r) => !intOk(r.episode) || !validHttpUrl(r.url))) e.episodes = 'Each episode needs a number and an http(s) URL'
    else if (new Set(nums.map(Number)).size !== nums.length) e.episodes = 'Episode numbers must be unique'
    return e
  })

  const errors = $derived(mode === 'single' ? singleErrors : seasonErrors)
  const valid = $derived(!!category && Object.keys(errors).length === 0 && headerRowsValid(headers) && (mode === 'season' || subtitleRowsValid(subtitles)))
  const show = (k: string) => (touched ? errors[k] : undefined)

  function setMode(m: Mode) {
    mode = m
    categoryId = null
    error = null
  }

  /** One URL per line becomes one episode row, numbered after the last one. */
  function addBulk() {
    const urls = bulk.split(/\s+/).filter((u) => u.trim())
    if (urls.length === 0) return
    const kept = episodes.filter((r) => r.url.trim() || r.title.trim())
    let n = kept.reduce((max, r) => Math.max(max, Number(r.episode) || 0), 0)
    episodes = [...kept, ...urls.map((u) => ({ id: rowId(), episode: String(++n), title: '', url: u.trim() }))]
    bulk = ''
  }

  function addEpisode() {
    const n = episodes.reduce((max, r) => Math.max(max, Number(r.episode) || 0), 0) + 1
    episodes = [...episodes, { id: rowId(), episode: String(n), title: '', url: '' }]
  }

  const num = (v: string) => (v.trim() ? Number(v.trim()) : undefined)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    touched = true
    if (!valid || submitting || !category) return
    submitting = true
    error = null
    try {
      await send(category, false)
    } finally {
      submitting = false
    }
  }

  /** `already_exists` asks before sending again with `overwrite`; keeping the file stays on the form. */
  async function send(category: Category, overwrite: boolean): Promise<void> {
    try {
      if (mode === 'single') {
        const tv = category.layout === 'tv'
        const job = await api.createJob({
          url: url.trim(),
          category: category.id,
          ...(tv
            ? { show_name: showName.trim(), season: num(season), episode: num(episode), ...(title.trim() ? { title: title.trim() } : {}) }
            : { title: title.trim() }),
          ...(category.layout !== 'flat' && year.trim() ? { year: num(year) } : {}),
          headers: headersRecord(headers),
          subtitles: subtitlesList(subtitles),
          ...(overwrite ? { overwrite } : {}),
        })
        live.upsert(job)
        toasts.show('Added 1 download')
      } else {
        const used = episodes.filter((r) => r.url.trim())
        const res = await api.createBatch({
          category: category.id,
          show_name: showName.trim(),
          season: Number(season.trim()),
          ...(year.trim() ? { year: num(year) } : {}),
          headers: headersRecord(headers),
          episodes: used.map((r) => ({ episode: Number(r.episode.trim()), url: r.url.trim(), ...(r.title.trim() ? { title: r.title.trim() } : {}) })),
          ...(overwrite ? { overwrite } : {}),
        })
        for (const j of res.jobs) live.upsert(j)
        toasts.show(batchAddedMessage(res.jobs.length, res.skipped))
      }
      router.go('queue')
    } catch (err) {
      if (err instanceof ApiError && err.code === 'duplicate') {
        toasts.show('Already in the queue')
        router.go('queue')
      } else if (err instanceof ApiError && err.code === 'already_exists' && !overwrite) {
        const replace = await confirm({
          title: 'Already in library',
          body: mode === 'single'
            ? 'A file with this name is already in the library. Replace it with this download?'
            : 'The episodes are already in the library or in the queue. Replace the library files?',
          confirm: 'Replace',
          cancel: 'Keep existing',
        })
        if (replace) await send(category, true)
        else error = 'Already in library'
      } else {
        error = errorMessage(err)
      }
    }
  }
</script>


<div class="page-head">
  <h1>Add download</h1>
  <div class="chips" role="radiogroup" aria-label="Request type">
    <button type="button" class="chip" role="radio" aria-checked={mode === 'single'} onclick={() => setMode('single')}>Single</button>
    <button type="button" class="chip" role="radio" aria-checked={mode === 'season'} onclick={() => setMode('season')}>Season</button>
  </div>
</div>

{#if categories.length === 0}
  <div class="empty">
    <div class="glyph"><Icon name="folder" size={32} /></div>
    <h2>No categories yet</h2>
    {#if canAddCategory}
      <p>This server has no categories yet. Add a destination folder to start downloading.</p>
      <button type="button" class="btn filled" onclick={() => (addingCategory = true)}><Icon name="add" size={18} />Add category</button>
    {:else}
      <p>This server has no categories yet. Add a destination folder in Server settings to start downloading.</p>
      <a class="btn filled" href={href('server-settings')}>Set up categories</a>
    {/if}
  </div>
{:else if mode === 'season' && choices.length === 0}
  <div class="empty">
    <h2>No TV categories</h2>
    {#if canAddCategory}
      <p>Seasons go into a category with the TV show layout.</p>
      <button type="button" class="btn filled" onclick={() => (addingCategory = true)}><Icon name="add" size={18} />Add TV category</button>
    {:else}
      <p>Seasons go into a category with the TV show layout. Add one in Server settings.</p>
      <a class="btn filled" href={href('server-settings')}>Server settings</a>
    {/if}
  </div>
{:else}
  <form class="layout" onsubmit={submit} novalidate>
    <div class="card panel">
      {#if mode === 'single'}
        <label class="field">
          <span>URL</span>
          <!-- svelte-ignore a11y_autofocus -->
          <input type="url" bind:value={url} placeholder="https://…" autofocus spellcheck="false" aria-invalid={show('url') ? 'true' : undefined} disabled={submitting} />
          {#if show('url')}<span class="hint error-text">{show('url')}</span>{/if}
        </label>
      {/if}

      <div class="field">
        <span>Category</span>
        <div class="chips" role="radiogroup" aria-label="Category">
          {#each choices as c (c.id)}
            <button type="button" class="chip" role="radio" aria-checked={categoryId === c.id} disabled={submitting} onclick={() => (categoryId = c.id)}>
              <Icon name={c.layout === 'tv' ? 'tv' : c.layout === 'movie' ? 'movie' : 'folder'} size={16} />{c.label}
            </button>
          {/each}
          {#if canAddCategory}
            <button type="button" class="chip" disabled={submitting} onclick={() => (addingCategory = true)}><Icon name="add" size={16} />Add category</button>
          {/if}
        </div>
        {#if category}<span class="hint">{LAYOUT_LABEL[category.layout]} · <code>{category.path}</code></span>{/if}
      </div>

      {#if mode === 'season' || layout === 'tv'}
        <div class="grid tv">
          <label class="field show">
            <span>Show</span>
            <input bind:value={showName} aria-invalid={show('show') ? 'true' : undefined} disabled={submitting} />
          </label>
          <label class="field">
            <span>Season</span>
            <input inputmode="numeric" bind:value={season} aria-invalid={show('season') ? 'true' : undefined} disabled={submitting} />
          </label>
          {#if mode === 'single'}
            <label class="field">
              <span>Episode</span>
              <input inputmode="numeric" bind:value={episode} aria-invalid={show('episode') ? 'true' : undefined} disabled={submitting} />
            </label>
          {/if}
          <label class="field">
            <span>Year</span>
            <input inputmode="numeric" bind:value={year} placeholder="Optional" aria-invalid={show('year') ? 'true' : undefined} disabled={submitting} />
          </label>
        </div>
        {#if mode === 'single'}
          <label class="field"><span>Episode title</span><input bind:value={title} placeholder="Optional" disabled={submitting} /></label>
        {/if}
      {:else}
        <div class="grid" class:movie={layout === 'movie'}>
          <label class="field">
            <span>Title</span>
            <input bind:value={title} aria-invalid={show('title') ? 'true' : undefined} disabled={submitting} />
          </label>
          {#if layout === 'movie'}
            <label class="field">
              <span>Year</span>
              <input inputmode="numeric" bind:value={year} placeholder="Optional" aria-invalid={show('year') ? 'true' : undefined} disabled={submitting} />
            </label>
          {/if}
        </div>
      {/if}
      {#if touched && (show('show') || show('season') || show('episode') || show('title') || show('year'))}
        <p class="hint error-text">Fill in the highlighted fields.</p>
      {/if}
    </div>

    {#if mode === 'season'}
      <div class="card panel">
        <div class="head">
          <h2>Episodes</h2>
          <span class="muted">{episodes.filter((r) => r.url.trim()).length} with a URL</span>
        </div>
        <div class="episodes">
          {#each episodes as row (row.id)}
            <div class="ep">
              <label class="field num"><span>Ep.</span><input inputmode="numeric" bind:value={row.episode} disabled={submitting} /></label>
              <label class="field"><span>Title</span><input bind:value={row.title} placeholder="Optional" disabled={submitting} /></label>
              <label class="field url"><span>URL</span><input type="url" bind:value={row.url} spellcheck="false" placeholder="https://…" aria-invalid={touched && row.url.trim() && !validHttpUrl(row.url) ? 'true' : undefined} disabled={submitting} /></label>
              <button type="button" class="icon-btn" aria-label="Remove episode {row.episode}" disabled={submitting} onclick={() => (episodes = episodes.filter((r) => r.id !== row.id))}><Icon name="close" size={18} /></button>
            </div>
          {/each}
        </div>
        {#if show('episodes')}<p class="hint error-text">{show('episodes')}</p>{/if}
        <div><button type="button" class="btn small outlined" onclick={addEpisode} disabled={submitting}><Icon name="add" size={16} />Add episode</button></div>
        <details>
          <summary>Paste several URLs</summary>
          <div class="bulk">
            <label class="field">
              <span>One URL per line, in episode order</span>
              <textarea rows="4" bind:value={bulk} spellcheck="false"></textarea>
            </label>
            <div><button type="button" class="btn small tonal" onclick={addBulk} disabled={!bulk.trim()}>Add as episodes</button></div>
          </div>
        </details>
      </div>
    {/if}

    <details class="card panel advanced">
      <summary>
        <span><strong>Advanced</strong> <span class="muted">· Request headers{mode === 'single' ? ' and subtitle files' : ' for every episode'}</span></span>
      </summary>
      <div class="adv">
        <div class="field"><span>Headers</span><HeaderRows bind:rows={headers} disabled={submitting} /></div>
        {#if mode === 'single'}
          <div class="field"><span>Subtitles</span><SubtitleRows bind:rows={subtitles} disabled={submitting} /></div>
        {/if}
      </div>
    </details>

    {#if error}<div class="alert" role="alert"><Icon name="error" /><span>{error}</span></div>{/if}

    <div class="submit">
      <a class="btn" href={href('queue')}>Cancel</a>
      <button class="btn filled" type="submit" disabled={submitting || (touched && !valid)}>
        <Icon name="download" size={18} />
        {#if submitting}Sending…{:else if mode === 'season'}Download ({episodes.filter((r) => r.url.trim()).length}){:else}Add download{/if}
      </button>
    </div>
  </form>
{/if}

<AddCategoryDialog
  open={addingCategory}
  layout={mode === 'season' ? 'tv' : 'movie'}
  onclose={() => (addingCategory = false)}
  oncreated={(id) => {
    addingCategory = false
    categoryId = id
    toasts.show('Category added')
  }} />

<style>
  .layout {
    display: grid;
    gap: 14px;
  }

  .grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 12px;
  }

  .grid.movie {
    grid-template-columns: minmax(0, 1fr) 110px;
  }

  .grid.tv {
    grid-template-columns: minmax(0, 1fr) 90px 90px 100px;
  }

  .head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
  }

  .episodes {
    display: grid;
    gap: 10px;
  }

  .ep {
    display: grid;
    grid-template-columns: 64px minmax(0, 1fr) minmax(0, 1.6fr) auto;
    align-items: end;
    gap: 8px;
  }

  details summary {
    cursor: pointer;
    font-weight: 600;
    color: var(--primary);
  }

  .advanced summary {
    color: inherit;
    font-weight: 400;
  }

  .advanced:not([open]) {
    gap: 0;
  }

  .bulk,
  .adv {
    display: grid;
    gap: 16px;
    margin-top: 12px;
  }

  .submit {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
  }

  @media (max-width: 640px) {
    .grid.tv {
      grid-template-columns: 1fr 1fr 1fr;
    }

    .grid.tv .show {
      grid-column: 1 / -1;
    }

    .ep {
      grid-template-columns: 56px minmax(0, 1fr) auto;
    }

    .ep .url {
      grid-column: 1 / 3;
      grid-row: 2;
    }

    .submit .btn {
      flex: 1;
    }
  }
</style>
