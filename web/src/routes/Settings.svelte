<script lang="ts">
  import Icon from '../components/Icon.svelte'
  import { api, apiBase, ApiError, errorMessage } from '../lib/api'
  import { bytes } from '../lib/format'
  import { href } from '../lib/router.svelte'
  import { readPref, session, toasts, writePref } from '../lib/store.svelte'
  import type { YtdlpCheck } from '../lib/types'

  const info = $derived(session.info)
  const serverURL = $derived(apiBase().toString().replace(/api\/$/, ''))

  let test = $state<string | null>(null)
  let testing = $state(false)
  async function testConnection() {
    testing = true
    const start = performance.now()
    try {
      const i = await api.info()
      session.info = i
      test = `Connected · v${i.version} · ${Math.round(performance.now() - start)} ms`
    } catch (e) {
      test = e instanceof ApiError && e.status === 401 ? 'Wrong API token' : errorMessage(e)
    } finally {
      testing = false
    }
  }

  // Per-browser defaults for category preselection in Add.
  let defaultMovie = $state(readPref('defaultMovieCategory') ?? '')
  let defaultTv = $state(readPref('defaultTvCategory') ?? '')
  const movieChoices = $derived(session.categories.filter((c) => c.layout !== 'tv'))
  const tvChoices = $derived(session.categories.filter((c) => c.layout === 'tv'))
  $effect(() => writePref('defaultMovieCategory', defaultMovie || null))
  $effect(() => writePref('defaultTvCategory', defaultTv || null))

  let check = $state<YtdlpCheck | null>(null)
  let checking = $state(false)
  let updating = $state(false)
  let ytdlpError = $state<string | null>(null)

  async function checkYtdlp() {
    checking = true
    ytdlpError = null
    try {
      check = await api.ytdlpCheck()
    } catch (e) {
      ytdlpError = errorMessage(e)
    } finally {
      checking = false
    }
  }

  async function updateYtdlp() {
    updating = true
    ytdlpError = null
    try {
      const res = await api.ytdlpUpdate()
      toasts.show(res.current ? `Updated to ${res.current}` : 'yt-dlp updated')
      check = null
      await session.refreshInfo()
    } catch (e) {
      ytdlpError = e instanceof ApiError && e.code === 'ytdlp_busy' ? 'Downloads are running — try again when they finish' : errorMessage(e)
    } finally {
      updating = false
    }
  }
</script>

<svelte:head><title>Settings · Fetcharr</title></svelte:head>

<div class="page-head"><h1>Settings</h1></div>

<div class="stack">
  <section class="card panel">
    <h2>Server</h2>
    <dl>
      <dt>Address</dt>
      <dd class="mono">{serverURL}</dd>
      <dt>Server version</dt>
      <dd>{info?.version ?? '—'}</dd>
      <dt>Access</dt>
      <dd>{info?.auth_required ? 'Protected by API token' : 'Open (no API token configured)'}</dd>
      <dt>Simultaneous downloads</dt>
      <dd>{info?.concurrency ?? '—'}</dd>
    </dl>
    <div class="actions">
      <button class="btn outlined" onclick={testConnection} disabled={testing}>{testing ? 'Testing…' : 'Test connection'}</button>
      {#if test}<span class="muted" role="status">{test}</span>{/if}
      {#if info?.auth_required}
        <button class="btn danger push" onclick={() => session.logout()}><Icon name="logout" size={18} />Sign out</button>
      {/if}
    </div>
  </section>

  <a class="card panel link" href={href('server-settings')}>
    <span class="glyph"><Icon name="settings" /></span>
    <span class="grow">
      <strong>Server settings</strong>
      <span class="muted">Downloads, categories and media servers</span>
    </span>
    <Icon name="expand" />
  </a>

  {#if session.categories.length}
    <section class="card panel">
      <h2>Library folders</h2>
      <ul class="cats">
        {#each session.categories as c (c.id)}
          <li>
            <Icon name={c.layout === 'tv' ? 'tv' : c.layout === 'movie' ? 'movie' : 'folder'} size={18} />
            <span class="grow"><strong>{c.label}</strong> <code class="muted">{c.path}</code></span>
            <span class="muted">{bytes(c.free_bytes) ?? '—'} free</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  <section class="card panel">
    <h2>Defaults</h2>
    <p class="muted">Preselected in Add download, for this browser only.</p>
    <div class="two">
      <label class="field">
        <span>Default movie category</span>
        <select bind:value={defaultMovie}>
          <option value="">None</option>
          {#each movieChoices as c (c.id)}<option value={c.id}>{c.label}</option>{/each}
        </select>
      </label>
      <label class="field">
        <span>Default TV category</span>
        <select bind:value={defaultTv}>
          <option value="">None</option>
          {#each tvChoices as c (c.id)}<option value={c.id}>{c.label}</option>{/each}
        </select>
      </label>
    </div>
  </section>

  <section class="card panel">
    <h2>yt-dlp</h2>
    <dl>
      <dt>Version</dt>
      <dd>{info?.ytdlp_version || 'Unknown'}</dd>
      <dt>Binary</dt>
      <dd>{info?.ytdlp_managed ? 'Managed by Fetcharr' : 'Custom binary (updated externally)'}</dd>
      {#if check}
        <dt>Latest version</dt>
        <dd>{check.latest}{#if check.update_available} <span class="pill primary">Update available</span>{:else} <span class="pill done">Up to date</span>{/if}</dd>
      {/if}
    </dl>
    {#if info?.ytdlp_managed}
      <div class="actions">
        <button class="btn outlined" onclick={checkYtdlp} disabled={checking || updating}>{checking ? 'Checking…' : 'Check for update'}</button>
        <button class="btn" class:filled={check?.update_available} onclick={updateYtdlp} disabled={updating || checking}>{updating ? 'Updating…' : 'Update to latest'}</button>
      </div>
    {/if}
    {#if ytdlpError}<p class="error-text" role="alert">{ytdlpError}</p>{/if}
  </section>

  <section class="card panel">
    <h2>About</h2>
    <p class="muted">
      Fetcharr web · server {info?.version ?? '—'} ·
      <a href="https://github.com/IstarVin/fetcharr" target="_blank" rel="noreferrer noopener">Source code</a>
    </p>
  </section>
</div>

<style>
  .stack {
    display: grid;
    gap: 14px;
  }

  dl {
    display: grid;
    grid-template-columns: minmax(140px, max-content) 1fr;
    gap: 8px 20px;
    margin: 0;
  }

  dt {
    color: var(--fg-muted);
  }

  dd {
    margin: 0;
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 12px;
  }

  .push {
    margin-left: auto;
  }

  .link {
    display: flex;
    grid-auto-flow: column;
    align-items: center;
    gap: 14px;
    color: inherit;
    text-decoration: none;
  }

  .link:hover {
    background: var(--surface);
  }

  .link > :global(svg:last-child) {
    transform: rotate(-90deg);
    color: var(--fg-muted);
  }

  .glyph {
    display: grid;
    place-items: center;
    width: 40px;
    height: 40px;
    border-radius: 12px;
    background: var(--primary-container);
    color: var(--on-primary-container);
  }

  .grow {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .cats {
    display: grid;
    gap: 10px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .cats li {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .cats .grow {
    display: block;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .two {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }

  @media (max-width: 640px) {
    dl,
    .two {
      grid-template-columns: 1fr;
    }

    dt {
      margin-top: 4px;
    }

    dl dd {
      margin-top: -6px;
    }
  }
</style>
