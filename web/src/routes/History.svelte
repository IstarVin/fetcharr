<script lang="ts">
  import ConnectionBanner from '../components/ConnectionBanner.svelte'
  import HistoryRow from '../components/HistoryRow.svelte'
  import Icon from '../components/Icon.svelte'
  import PlayerDialog from '../components/PlayerDialog.svelte'
  import SourceReplacement from '../components/SourceReplacement.svelte'
  import { clearHistory, retryFailed } from '../lib/actions'
  import { groupByDay } from '../lib/format'
  import { history, session, type HistoryFilter } from '../lib/store.svelte'
  import type { Job } from '../lib/types'

  const FILTERS: [HistoryFilter, string][] = [
    ['all', 'All'],
    ['completed', 'Completed'],
    ['failed', 'Failed'],
  ]
  let playing = $state<Job | null>(null)
  let replacing = $state<Job | null>(null)
  let sentinel: HTMLElement | undefined = $state()
  let search = $state(history.query)
  let searchTimer: ReturnType<typeof setTimeout> | undefined

  if (!history.loaded && !history.loading) void history.load()

  const canSearch = $derived(session.info?.history_search_supported === true)
  const groups = $derived(groupByDay(history.jobs))
  const failedCount = $derived(history.jobs.filter((j) => j.status === 'failed').length)

  function onSearch(value: string) {
    search = value
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => history.setQuery(value), 250)
  }

  function clearFilters() {
    clearTimeout(searchTimer)
    search = ''
    history.query = ''
    history.filter = 'all'
    void history.load()
  }

  // Load the next page as the end of the list scrolls into view.
  $effect(() => {
    if (!sentinel) return
    const io = new IntersectionObserver((entries) => {
      // After a failed page the inline Retry button takes over, so scrolling doesn't loop.
      if (entries.some((e) => e.isIntersecting) && !history.moreError) void history.loadMore()
    }, { rootMargin: '400px' })
    io.observe(sentinel)
    return () => io.disconnect()
  })
</script>


<div class="page-head">
  <h1>History</h1>
  <div class="row-actions">
    <button class="icon-btn" onclick={() => history.load()} aria-label="Refresh" title="Refresh"><Icon name="retry" /></button>
    {#if failedCount > 0}
      <button class="btn small" onclick={retryFailed}><Icon name="retry" size={16} />Retry failed ({failedCount})</button>
    {/if}
    <button class="btn small danger" disabled={history.jobs.length === 0} onclick={clearHistory}><Icon name="delete" size={16} />Clear history</button>
  </div>
</div>

<ConnectionBanner />

{#if history.jobs.length > 0 || history.filtered}
  <div class="tools">
    {#if canSearch}
      <label class="search">
        <Icon name="search" size={18} />
        <span class="visually-hidden">Search history</span>
        <input type="search" placeholder="Search titles and shows" value={search} oninput={(e) => onSearch(e.currentTarget.value)} />
      </label>
    {/if}
    <div class="chips filters" role="radiogroup" aria-label="Filter">
      {#each FILTERS as [f, label] (f)}
        <button class="chip" role="radio" aria-checked={history.filter === f} onclick={() => history.setFilter(f)}>{label}</button>
      {/each}
    </div>
  </div>
{/if}

{#if history.error && history.jobs.length === 0}
  <div class="empty">
    <h2>Couldn't load history</h2>
    <p>{history.error}</p>
    <button class="btn filled" onclick={() => history.load()}>Try again</button>
  </div>
{:else if !history.loaded}
  <div class="empty"><p>Loading…</p></div>
{:else if history.jobs.length === 0 && history.filtered}
  <div class="empty">
    <p>No downloads match{history.query.trim() ? ` “${history.query.trim()}”` : ' this filter'}.</p>
    <button class="btn" onclick={clearFilters}>Show all</button>
  </div>
{:else if history.jobs.length === 0}
  <div class="empty">
    <div class="glyph"><Icon name="history" size={32} /></div>
    <h2>No finished downloads</h2>
    <p>Completed, failed and cancelled downloads will show up here.</p>
  </div>
{:else}
  {#each groups as g (g.key)}
    <section aria-label={g.label}>
      <h2 class="section-title">{g.label}</h2>
      <div class="list">
        {#each g.jobs as j (j.id)}
          <HistoryRow job={j} onplay={(x) => (playing = x)} onreplace={(x) => (replacing = x)} />
        {/each}
      </div>
    </section>
  {/each}
{/if}

{#if history.loaded && !history.done}
  <div bind:this={sentinel} class="more">
    {#if history.loading}<span class="muted">Loading more…</span>{:else if history.moreError}
      <p class="error-text" role="alert">Couldn't load more: {history.moreError}</p>
      <button class="btn" onclick={() => history.loadMore()}>Retry</button>
    {:else}<button class="btn" onclick={() => history.loadMore()}>Load more</button>{/if}
  </div>
{/if}

<PlayerDialog job={playing} onclose={() => (playing = null)} />
<SourceReplacement job={replacing} onclose={() => (replacing = null)} />

<style>
  .tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 10px 16px;
    margin-bottom: 4px;
  }

  .search {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1 1 240px;
    max-width: 420px;
    height: 40px;
    padding: 0 12px;
    border: 1px solid var(--outline-variant);
    border-radius: 999px;
    background: var(--surface);
    color: var(--fg-muted);
  }

  .search:focus-within {
    border-color: var(--primary);
    outline: 2px solid color-mix(in srgb, var(--primary) 25%, transparent);
  }

  .search input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: none;
    color: var(--fg);
    font: inherit;
    outline: none;
  }

  .list {
    display: grid;
    gap: 8px;
  }

  .more {
    display: grid;
    place-items: center;
    gap: 8px;
    padding: 24px;
  }

  .more p {
    margin: 0;
  }
</style>
