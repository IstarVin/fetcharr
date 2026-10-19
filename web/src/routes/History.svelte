<script lang="ts">
  import ConnectionBanner from '../components/ConnectionBanner.svelte'
  import HistoryRow from '../components/HistoryRow.svelte'
  import Icon from '../components/Icon.svelte'
  import PlayerDialog from '../components/PlayerDialog.svelte'
  import SourceReplacement from '../components/SourceReplacement.svelte'
  import { clearHistory } from '../lib/actions'
  import { groupByDay } from '../lib/format'
  import { history } from '../lib/store.svelte'
  import type { Job } from '../lib/types'

  type Filter = 'all' | 'completed' | 'failed'
  let filter = $state<Filter>('all')
  let playing = $state<Job | null>(null)
  let replacing = $state<Job | null>(null)
  let sentinel: HTMLElement | undefined = $state()

  if (!history.loaded && !history.loading) void history.load()

  const visible = $derived(
    filter === 'all' ? history.jobs : history.jobs.filter((j) => (filter === 'completed' ? j.status === 'completed' : j.status !== 'completed')),
  )
  const groups = $derived(groupByDay(visible))

  // Load the next page as the end of the list scrolls into view.
  $effect(() => {
    if (!sentinel) return
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) void history.loadMore()
    }, { rootMargin: '400px' })
    io.observe(sentinel)
    return () => io.disconnect()
  })
</script>

<svelte:head><title>History · Fetcharr</title></svelte:head>

<div class="page-head">
  <h1>History</h1>
  <div class="row-actions">
    <button class="icon-btn" onclick={() => history.load()} aria-label="Refresh" title="Refresh"><Icon name="retry" /></button>
    <button class="btn small danger" disabled={history.jobs.length === 0} onclick={clearHistory}><Icon name="delete" size={16} />Clear history</button>
  </div>
</div>

<ConnectionBanner />

{#if history.jobs.length > 0}
  <div class="chips filters" role="radiogroup" aria-label="Filter">
    {#each [['all', 'All'], ['completed', 'Completed'], ['failed', 'Failed']] as [f, label] (f)}
      <button class="chip" role="radio" aria-checked={filter === f} onclick={() => (filter = f as Filter)}>{label}</button>
    {/each}
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
{:else if history.jobs.length === 0}
  <div class="empty">
    <div class="glyph"><Icon name="history" size={32} /></div>
    <h2>No finished downloads</h2>
    <p>Completed, failed and cancelled downloads will show up here.</p>
  </div>
{:else if visible.length === 0}
  <div class="empty"><p>Nothing here. Switch the filter to see other downloads.</p></div>
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
    {#if history.loading}<span class="muted">Loading more…</span>{:else}<button class="btn" onclick={() => history.loadMore()}>Load more</button>{/if}
  </div>
{/if}

<PlayerDialog job={playing} onclose={() => (playing = null)} />
<SourceReplacement job={replacing} onclose={() => (replacing = null)} />

<style>
  .filters {
    margin-bottom: 4px;
  }

  .list {
    display: grid;
    gap: 8px;
  }

  .more {
    display: grid;
    place-items: center;
    padding: 24px;
  }
</style>
