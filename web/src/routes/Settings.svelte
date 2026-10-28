<script lang="ts">
  import Icon from '../components/Icon.svelte'
  import QrCode from '../components/QrCode.svelte'
  import { copyText } from '../lib/actions'
  import { api, apiBase, ApiError, errorMessage } from '../lib/api'
  import { browserNotifications } from '../lib/browserNotify.svelte'
  import { bytes } from '../lib/format'
  import { href } from '../lib/router.svelte'
  import { bookmarklet, pairingLink, serverAddress } from '../lib/share'
  import { readPref, session, toasts, writePref } from '../lib/store.svelte'
  import type { YtdlpCheck } from '../lib/types'

  const info = $derived(session.info)
  const serverURL = $derived(apiBase().toString().replace(/api\/$/, ''))

  // Pairing: the browser never learns the API token (it signs in with an
  // HttpOnly cookie), so it is typed here and only drawn into the code.
  let pairToken = $state('')
  let showPairing = $state(false)
  const pairing = $derived(pairingLink(serverAddress(document.baseURI), pairToken))
  const sendTo = $derived(bookmarklet(serverAddress(document.baseURI) + '/'))

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
      <span class="muted">Downloads, categories, media servers and notifications</span>
    </span>
    <Icon name="expand" />
  </a>

  <section class="card panel">
    <h2>Android app</h2>
    <p class="muted">Scan this code with your phone's camera to fill in the server address{info?.auth_required ? ' and token' : ''} in the Fetcharr app.</p>
    {#if info?.auth_required}
      <label class="field token">
        <span>API token</span>
        <input type="password" bind:value={pairToken} autocomplete="off" placeholder="Paste the server's API token" />
        <span class="hint">Used only to draw the code; it is not saved or sent anywhere.</span>
      </label>
    {/if}
    {#if showPairing}
      <div class="pair">
        <QrCode text={pairing} label="Fetcharr connection code for {serverAddress(document.baseURI)}" />
        <div class="pair-text">
          <p class="muted">The app asks before it saves anything.{pairToken.trim() ? ' This code contains your token: do not share screenshots of it.' : ''}</p>
          <a class="btn small" href={pairing}><Icon name="open" size={16} />Open in the app</a>
          <button class="btn small" onclick={() => (showPairing = false)}>Hide code</button>
        </div>
      </div>
    {:else}
      <div class="actions">
        <button class="btn outlined" disabled={info?.auth_required && !pairToken.trim()} onclick={() => (showPairing = true)}>
          <Icon name="qr" size={18} />Show connection code
        </button>
      </div>
    {/if}
  </section>

  <section class="card panel">
    <h2>Send links from your browser</h2>
    <p class="muted">Drag this button to your bookmarks bar. Click it on any page to open Add download with that page's address.</p>
    <div class="actions">
      <!-- svelte-ignore a11y_invalid_attribute -->
      <a class="btn outlined" href={sendTo} draggable="true" onclick={(e) => e.preventDefault()} title="Drag to your bookmarks bar">
        <Icon name="download" size={18} />Send to Fetcharr
      </a>
      <button class="btn small" onclick={async () => toasts.show((await copyText(sendTo)) ? 'Bookmark code copied' : "Couldn't copy")}>
        <Icon name="copy" size={16} />Copy bookmark code
      </button>
    </div>
    <p class="hint">On a phone, install this page as an app from the browser menu (HTTPS required) and share links to Fetcharr.</p>
  </section>

  {#if browserNotifications.supported}
    <section class="card panel">
      <h2>Notifications in this browser</h2>
      <p class="muted">Get a notification when downloads finish while this tab is in the background.</p>
      <div class="actions">
        {#if browserNotifications.enabled}
          <span class="pill done"><Icon name="bell" size={14} />On</span>
          <button class="btn small" onclick={() => browserNotifications.disable()}>Turn off</button>
        {:else}
          <button class="btn outlined" disabled={browserNotifications.denied} onclick={() => browserNotifications.enable()}>
            <Icon name="bell" size={18} />Turn on
          </button>
          {#if browserNotifications.denied}<span class="hint">Notifications are blocked for this site in your browser settings.</span>{/if}
        {/if}
      </div>
      {#if info?.notifications_supported}
        <p class="hint">For notifications without an open tab, add an ntfy topic or webhook in <a href={href('server-settings')}>Server settings</a>.</p>
      {/if}
    </section>
  {/if}

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
    grid-template-columns: minmax(0, 1fr);
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
    grid-template-columns: minmax(0, 1fr);
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

  .token {
    max-width: 420px;
  }

  .hint {
    font-size: 0.82rem;
    color: var(--fg-muted);
  }

  .pair {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: 16px 24px;
  }

  .pair-text {
    display: grid;
    gap: 10px;
    justify-items: start;
    flex: 1 1 220px;
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
