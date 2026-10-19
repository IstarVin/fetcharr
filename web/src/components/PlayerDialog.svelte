<script lang="ts">
  import { copyText } from '../lib/actions'
  import { api, errorMessage, playbackURL } from '../lib/api'
  import { heading } from '../lib/format'
  import { toasts } from '../lib/store.svelte'
  import type { Job } from '../lib/types'
  import Dialog from './Dialog.svelte'
  import Icon from './Icon.svelte'

  let { job, onclose }: { job: Job | null; onclose: () => void } = $props()

  let url = $state<string | null>(null)
  let mime = $state('')
  let error = $state<string | null>(null)
  let unplayable = $state(false)

  $effect(() => {
    const j = job
    url = null
    error = null
    unplayable = false
    if (!j) return
    let alive = true
    api
      .playback(j.id)
      .then((p) => {
        if (!alive) return
        url = playbackURL(p)
        mime = p.mime_type
      })
      .catch((e) => {
        if (alive) error = errorMessage(e)
      })
    return () => {
      alive = false
    }
  })

  async function copy() {
    if (url && (await copyText(url))) toasts.show('Link copied · valid for 24 hours')
  }
</script>

<Dialog open={!!job} title={job ? heading(job) : ''} wide {onclose}>
  {#if error}
    <div class="alert" role="alert"><Icon name="error" /><span>{error}</span></div>
  {:else if !url}
    <div class="stage loading" aria-busy="true"><span class="muted">Preparing stream…</span></div>
  {:else}
    {#if !unplayable}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video class="stage" src={url} controls autoplay playsinline preload="metadata" onerror={() => (unplayable = true)}></video>
    {:else}
      <div class="alert info"><Icon name="info" /><span>This browser can't play this file ({mime || 'unknown type'}). Open the link in a player such as VLC or mpv.</span></div>
    {/if}
    <p class="muted small">The link works for 24 hours or until the server restarts, and never contains your API token.</p>
  {/if}
  {#snippet actions()}
    {#if url}
      <button class="btn" onclick={copy}><Icon name="link" size={18} />Copy link</button>
      <a class="btn" href={url} target="_blank" rel="noreferrer noopener"><Icon name="open" size={18} />Open</a>
    {/if}
    <button class="btn filled" onclick={onclose}>Close</button>
  {/snippet}
</Dialog>

<style>
  .stage {
    display: block;
    width: 100%;
    aspect-ratio: 16 / 9;
    border-radius: var(--radius-m);
    background: #000;
  }

  .loading {
    display: grid;
    place-items: center;
    background: var(--surface-highest);
  }

  .small {
    font-size: 0.8rem;
  }
</style>
