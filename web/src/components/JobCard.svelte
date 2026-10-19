<script lang="ts">
  import { jobAction } from '../lib/actions'
  import { actionsFor, cardFraction, categoryLabel, categoryLayout, heading, percentLabel, secondary, STATUS_LABEL, statParts } from '../lib/format'
  import { session } from '../lib/store.svelte'
  import type { Job } from '../lib/types'
  import Icon from './Icon.svelte'
  import ProgressBar from './ProgressBar.svelte'

  let { job, nested = false }: { job: Job; nested?: boolean } = $props()

  const title = $derived(heading(job))
  const sub = $derived(secondary(job))
  const fraction = $derived(cardFraction(job))
  const stats = $derived(statParts(job))
  const layout = $derived(categoryLayout(session.categories, job.category))
  let busy = $state(false)

  async function run(action: 'pause' | 'resume' | 'cancel') {
    busy = true
    try {
      await jobAction(job, action)
    } finally {
      busy = false
    }
  }

  const ACTION = {
    pause: { label: 'Pause', icon: 'pause' },
    resume: { label: 'Resume', icon: 'play' },
    cancel: { label: 'Cancel', icon: 'close' },
  } as const
</script>

<article class="job" class:nested class:card={!nested}>
  <div class="top">
    {#if !nested}
      <span class="kind" aria-hidden="true"><Icon name={layout === 'tv' ? 'tv' : layout === 'movie' ? 'movie' : 'folder'} size={18} /></span>
    {/if}
    <div class="text">
      <h3 title={title}>{title}</h3>
      {#if sub}<p class="sub">{sub}</p>{/if}
      {#if !nested}<p class="cat">{categoryLabel(session.categories, job.category)}</p>{/if}
    </div>
    <span class="pill" class:primary={job.status === 'downloading' || job.status === 'processing'}>{STATUS_LABEL[job.status]}</span>
  </div>

  {#if job.status === 'processing'}
    <ProgressBar value={null} indeterminate label="Finishing {title}" />
  {:else if fraction != null}
    <ProgressBar value={fraction} label="{title} progress" />
  {/if}

  <div class="bottom">
    <p class="stats">
      {#if fraction != null}<strong>{percentLabel(fraction)}</strong>{/if}
      {#if stats?.speed}<span>{stats.speed}</span>{/if}
      {#if stats?.eta}<span>{stats.eta}</span>{/if}
      {#if stats?.size}<span>{stats.size}</span>{/if}
      {#if job.status === 'pending'}<span class="muted">Waiting for a free slot</span>{/if}
    </p>
    <div class="row-actions">
      {#each actionsFor(job.status) as a (a)}
        <button class="btn small" class:danger={a === 'cancel'} disabled={busy} onclick={() => run(a)} aria-label="{ACTION[a].label} {title}">
          <Icon name={ACTION[a].icon} size={16} />{ACTION[a].label}
        </button>
      {/each}
    </div>
  </div>
</article>

<style>
  .job {
    display: grid;
    gap: 10px;
    padding: 14px 14px 10px 16px;
  }

  .job.nested {
    padding: 12px 0 8px;
    border-top: 1px solid color-mix(in srgb, var(--outline-variant) 60%, transparent);
  }

  .top {
    display: flex;
    align-items: flex-start;
    gap: 12px;
  }

  .kind {
    display: grid;
    place-items: center;
    flex: none;
    width: 36px;
    height: 36px;
    border-radius: 10px;
    background: var(--primary-container);
    color: var(--on-primary-container);
  }

  .text {
    flex: 1;
    min-width: 0;
  }

  h3 {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 1rem;
  }

  .sub {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg-muted);
  }

  .cat {
    color: var(--fg-muted);
    font-size: 0.82rem;
  }

  .bottom {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: 4px 12px;
  }

  .stats {
    display: flex;
    flex-wrap: wrap;
    gap: 4px 14px;
    font-size: 0.85rem;
    color: var(--fg-muted);
    font-variant-numeric: tabular-nums;
  }

  .stats strong {
    color: var(--fg);
  }
</style>
