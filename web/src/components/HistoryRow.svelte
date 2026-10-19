<script lang="ts">
  import { copyText, deleteJob, retryJob } from '../lib/actions'
  import { categoryLabel, completedSize, heading, historyStatusLine, timeOfDay } from '../lib/format'
  import { session, toasts } from '../lib/store.svelte'
  import type { Job } from '../lib/types'
  import Icon from './Icon.svelte'

  let { job, onplay, onreplace }: { job: Job; onplay: (j: Job) => void; onreplace: (j: Job) => void } = $props()

  let open = $state(false)
  let busy = $state(false)
  const uid = $props.id()

  const title = $derived(heading(job))
  const line = $derived(historyStatusLine(job))
  const size = $derived(completedSize(job))
  const path = $derived(job.status === 'completed' ? job.file_path?.trim() || null : null)
  const warned = $derived(job.status === 'completed' && job.warnings.length > 0)
  const canReplace = $derived(job.status !== 'completed' && !!session.info?.source_replacement_supported)
  const host = $derived.by(() => {
    try {
      return new URL(job.url).host
    } catch {
      return job.url
    }
  })

  async function retry() {
    busy = true
    try {
      await retryJob(job)
    } finally {
      busy = false
    }
  }

  async function copy() {
    if (path && (await copyText(path))) toasts.show('Path copied')
  }
</script>

<article class="row card" class:open>
  <div class="main">
    <span class="status {job.status}" aria-hidden="true">
      <Icon name={job.status === 'completed' ? 'check' : job.status === 'failed' ? 'error' : 'close'} size={20} />
    </span>
    <button class="summary" aria-expanded={open} aria-controls="{uid}-details" onclick={() => (open = !open)}>
      <span class="title">
        {title}
        {#if warned}<span class="warn" title="Completed with warnings"><Icon name="warning" size={16} label="Completed with warnings" /></span>{/if}
      </span>
      {#if path}
        <!-- Right-to-left truncation keeps the file name visible; bdi keeps the path itself left-to-right. -->
        <span class="line mono" title={path}><bdi dir="ltr">{path}</bdi></span>
      {:else}
        <span class="line" class:error-text={job.status === 'failed'}>{line}</span>
      {/if}
      <span class="meta">{[categoryLabel(session.categories, job.category), size, timeOfDay(job.finished_at ?? job.created_at)].filter(Boolean).join(' · ')}</span>
    </button>
    <div class="row-actions">
      {#if path}
        <button class="btn small tonal primary-action" onclick={() => onplay(job)} aria-label="Play {title}"><Icon name="play" size={16} /><span>Play</span></button>
      {:else if job.status !== 'completed'}
        <button class="btn small tonal primary-action" disabled={busy} onclick={retry} aria-label="Retry {title}"><Icon name="retry" size={16} /><span>Retry</span></button>
      {/if}
      <button class="icon-btn row-delete" aria-label="Delete {title}" title="Delete" onclick={() => deleteJob(job)}><Icon name="delete" size={20} /></button>
      <button class="icon-btn chev" aria-label="{open ? 'Hide' : 'Show'} details" aria-expanded={open} onclick={() => (open = !open)}><Icon name="expand" /></button>
    </div>
  </div>

  {#if open}
    <div class="details" id="{uid}-details">
      {#if path}
        <div class="kv">
          <span>Saved to</span>
          <div class="pathline"><code>{path}</code><button class="btn small" onclick={copy}><Icon name="copy" size={16} />Copy path</button></div>
        </div>
      {/if}
      {#if job.status === 'failed' && job.error_message?.trim()}
        <div class="kv"><span>What went wrong</span><pre>{job.error_message}</pre></div>
      {/if}
      {#if warned}
        <div class="kv"><span>Warnings</span><pre>{job.warnings.join('\n')}</pre></div>
      {/if}
      <div class="kv"><span>Source</span><p class="mono">{host}{#if job.subtitles.length} · {job.subtitles.length} {job.subtitles.length === 1 ? 'subtitle' : 'subtitles'}: {job.subtitles.map((s) => s.lang).join(', ')}{/if}</p></div>
      <div class="detail-actions">
        {#if canReplace}
          <button class="btn small outlined" onclick={() => onreplace(job)}><Icon name="link" size={16} />Replace source &amp; retry</button>
        {/if}
        <button class="btn small danger details-delete" onclick={() => deleteJob(job)}><Icon name="delete" size={16} />Delete</button>
      </div>
    </div>
  {/if}
</article>

<style>
  .row {
    padding: 10px 8px 10px 14px;
  }

  .main {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .status {
    display: grid;
    place-items: center;
    flex: none;
    width: 36px;
    height: 36px;
    border-radius: 10px;
  }

  .status.completed {
    background: var(--tertiary-container);
    color: var(--on-tertiary-container);
  }

  .status.failed {
    background: var(--error-container);
    color: var(--on-error-container);
  }

  .status.cancelled {
    background: var(--surface-highest);
    color: var(--fg-muted);
  }

  .summary {
    display: grid;
    flex: 1;
    min-width: 0;
    padding: 2px 0;
    border: 0;
    background: none;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }

  .title {
    display: flex;
    align-items: center;
    gap: 6px;
    overflow: hidden;
    font-weight: 600;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  .warn {
    color: var(--warning);
  }

  .line,
  .meta {
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
    font-size: 0.85rem;
    color: var(--fg-muted);
  }

  .line.mono {
    font-size: 0.8rem;
    direction: rtl;
    text-align: left;
  }

  .line.error-text {
    color: var(--error);
  }

  .meta {
    font-size: 0.78rem;
  }

  .chev :global(svg) {
    transition: transform 160ms ease;
  }

  .open .chev :global(svg) {
    transform: rotate(180deg);
  }

  .row-actions {
    flex-wrap: nowrap;
    align-items: center;
  }

  .details {
    display: grid;
    gap: 12px;
    margin: 10px 0 4px 48px;
    padding-top: 12px;
    border-top: 1px solid color-mix(in srgb, var(--outline-variant) 60%, transparent);
  }

  .kv {
    display: grid;
    gap: 4px;
  }

  .kv > span {
    font-size: 0.75rem;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--fg-muted);
  }

  .pathline {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px 8px;
  }

  code {
    word-break: break-all;
  }

  pre {
    margin: 0;
    padding: 10px 12px;
    overflow-x: auto;
    border-radius: var(--radius-s);
    background: var(--surface-highest);
    font: 0.8rem/1.5 var(--mono);
    white-space: pre-wrap;
    word-break: break-word;
  }

  .detail-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  .details-delete {
    display: none;
  }

  @media (max-width: 640px) {
    .row {
      padding-left: 12px;
    }

    .main {
      gap: 10px;
    }

    .details {
      margin-left: 0;
    }

    /* Narrow rows keep one round primary action; Delete moves into the details. */
    .primary-action {
      width: 40px;
      min-height: 40px;
      padding: 0;
    }

    .primary-action span,
    .row-delete {
      display: none;
    }

    .details-delete {
      display: inline-flex;
    }
  }
</style>
