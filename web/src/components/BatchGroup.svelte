<script lang="ts">
  import { batchAction } from '../lib/actions'
  import { categoryLabel, percentLabel, type SectionKind } from '../lib/format'
  import { session } from '../lib/store.svelte'
  import type { Job } from '../lib/types'
  import Icon from './Icon.svelte'
  import JobCard from './JobCard.svelte'
  import ProgressBar from './ProgressBar.svelte'

  let { batchId, title, jobs, progress, section }: { batchId: string; title: string; jobs: Job[]; progress: number | null; section: SectionKind } = $props()

  let expanded = $state(false)
  let busy = $state(false)
  const uid = $props.id()

  async function run(action: 'pause' | 'resume' | 'cancel') {
    busy = true
    try {
      await batchAction(batchId, title, action)
    } finally {
      busy = false
    }
  }
</script>

<article class="card group">
  <div class="head">
    <span class="kind" aria-hidden="true"><Icon name="tv" size={18} /></span>
    <button class="toggle" aria-expanded={expanded} aria-controls="{uid}-episodes" onclick={() => (expanded = !expanded)}>
      <span class="text">
        <span class="title">{title}</span>
        <span class="meta">{jobs.length} {jobs.length === 1 ? 'episode' : 'episodes'} · {categoryLabel(session.categories, jobs[0]?.category ?? '')}</span>
      </span>
      <span class="chev" class:open={expanded}><Icon name="expand" /></span>
      <span class="visually-hidden">{expanded ? 'Hide' : 'Show'} episodes of {title}</span>
    </button>
  </div>
  {#if section !== 'queued' && progress != null}
    <div class="progress">
      <ProgressBar value={progress} label="{title} combined progress" />
      <span>{percentLabel(progress)}</span>
    </div>
  {/if}
  <div class="row-actions">
    {#if section === 'paused'}
      <button class="btn small" disabled={busy} onclick={() => run('resume')}><Icon name="play" size={16} />Resume all</button>
    {:else}
      <button class="btn small" disabled={busy} onclick={() => run('pause')}><Icon name="pause" size={16} />Pause all</button>
    {/if}
    <button class="btn small danger" disabled={busy} onclick={() => run('cancel')}><Icon name="close" size={16} />Cancel all</button>
  </div>
  {#if expanded}
    <div id="{uid}-episodes" class="episodes">
      {#each jobs as j (j.id)}
        <JobCard job={j} nested />
      {/each}
    </div>
  {/if}
</article>

<style>
  .group {
    display: grid;
    gap: 10px;
    padding: 14px 14px 10px 16px;
  }

  .head {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .kind {
    display: grid;
    place-items: center;
    flex: none;
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: var(--tertiary-container);
    color: var(--on-tertiary-container);
  }

  .toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
    padding: 4px;
    margin: -4px;
    border: 0;
    border-radius: var(--radius-s);
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  .toggle:hover {
    background: color-mix(in srgb, var(--fg) 5%, transparent);
  }

  .text {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .title {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
  }

  .meta {
    color: var(--fg-muted);
    font-size: 0.82rem;
  }

  .chev {
    color: var(--fg-muted);
    transition: transform 160ms ease;
  }

  .chev.open {
    transform: rotate(180deg);
  }

  .progress {
    display: flex;
    align-items: center;
    gap: 12px;
    font-size: 0.85rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }

  .progress :global(.track) {
    flex: 1;
  }

  .episodes {
    display: grid;
    padding-left: 48px;
  }

  @media (max-width: 640px) {
    .episodes {
      padding-left: 0;
    }
  }
</style>
