<script lang="ts">
  import { onDestroy } from 'svelte'
  import Icon from '../components/Icon.svelte'
  import { api, ApiError, errorMessage } from '../lib/api'
  import { LAYOUT_LABEL } from '../lib/format'
  import { href, router } from '../lib/router.svelte'
  import { fromDocument, newCategory, newIntegration, newMapping, newNotification, toDocument, validate, type NotificationDraft, type SettingsDraft } from '../lib/serverSettings'
  import { confirm, readPref, session, toasts, writePref } from '../lib/store.svelte'
  import type { Layout } from '../lib/types'

  let baseline = $state<SettingsDraft | null>(null)
  let draft = $state<SettingsDraft | null>(null)
  let loading = $state(false)
  let saving = $state(false)
  let error = $state<string | null>(null)
  let conflict = $state(false)
  let attempted = $state(false)

  const supported = $derived(!!session.info?.settings_supported)
  const managed = $derived(!!session.info?.ytdlp_managed)
  const dirty = $derived(!!draft && !!baseline && JSON.stringify(draft) !== JSON.stringify(baseline))
  const errors = $derived(draft ? validate(draft) : {})
  const err = (k: string) => (attempted ? errors[k] : undefined)
  const invalid = $derived(attempted && Object.keys(errors).length > 0)

  async function load() {
    loading = true
    error = null
    conflict = false
    attempted = false
    try {
      const d = fromDocument(await api.getSettings())
      baseline = structuredClone(d)
      draft = d
    } catch (e) {
      error = errorMessage(e)
    } finally {
      loading = false
    }
  }

  if (session.info?.settings_supported) void load()

  async function reload() {
    if (dirty && !(await confirm({ title: 'Discard unsaved changes?', body: 'Your changes have not been saved to the server.', confirm: 'Discard changes', cancel: 'Keep editing', danger: true }))) return
    await load()
  }

  async function save() {
    if (!draft || saving) return
    attempted = true
    error = null
    if (Object.keys(errors).length) return
    saving = true
    try {
      const saved = fromDocument(await api.saveSettings(toDocument($state.snapshot(draft) as SettingsDraft)))
      baseline = structuredClone(saved)
      draft = saved
      attempted = false
      await session.refreshInfo()
      // Defaults that no longer match a category of the right layout are cleared.
      const layoutOf = (id: string | null) => session.categories.find((c) => c.id === id)?.layout
      if (layoutOf(readPref('defaultMovieCategory')) === undefined || layoutOf(readPref('defaultMovieCategory')) === 'tv') writePref('defaultMovieCategory', null)
      if (layoutOf(readPref('defaultTvCategory')) !== 'tv') writePref('defaultTvCategory', null)
      toasts.show('Server settings saved and applied.')
    } catch (e) {
      if (e instanceof ApiError && e.code === 'settings_conflict') {
        conflict = true
        error = null
      } else {
        error = errorMessage(e)
      }
    } finally {
      saving = false
    }
  }

  // Unsaved edits: ask before leaving through the app or closing the tab.
  $effect(() => {
    router.setGuard(() => !(dirty || saving) || window.confirm(saving ? 'Leave while saving? The server may still apply this save.' : 'Discard unsaved changes?'))
  })
  const beforeUnload = (e: BeforeUnloadEvent) => {
    if (dirty || saving) e.preventDefault()
  }
  window.addEventListener('beforeunload', beforeUnload)
  onDestroy(() => {
    router.setGuard(null)
    window.removeEventListener('beforeunload', beforeUnload)
  })

  const LAYOUTS: Layout[] = ['movie', 'tv', 'flat']

  // Test messages go to the saved target, so edits must be saved first.
  let testing = $state<string | null>(null)
  let testResult = $state<Record<string, { ok: boolean; text: string }>>({})
  const savedUnchanged = (n: NotificationDraft) =>
    n.existing && JSON.stringify(n) === JSON.stringify(baseline?.notifications?.find((b) => b.id === n.id))

  async function sendTest(n: NotificationDraft) {
    testing = n.id
    try {
      await api.testNotification(n.id)
      testResult[n.id] = { ok: true, text: 'Test notification sent.' }
    } catch (e) {
      testResult[n.id] = { ok: false, text: errorMessage(e) }
    } finally {
      testing = null
    }
  }
</script>


<div class="page-head">
  <div class="title">
    <a class="icon-btn" href={href('settings')} aria-label="Back"><Icon name="back" /></a>
    <h1>Server settings</h1>
  </div>
  {#if draft}
    <button class="btn" onclick={reload} disabled={saving || loading}><Icon name="retry" size={18} />Reload from server</button>
  {/if}
</div>

{#if !supported}
  <div class="empty">
    <h2>Not available on this server</h2>
    <p>This server does not support in-app configuration. Update the server to manage its settings here.</p>
  </div>
{:else if !draft}
  {#if error}
    <div class="empty">
      <h2>Couldn't load settings</h2>
      <p>{error}</p>
      <button class="btn filled" onclick={load}>Try again</button>
    </div>
  {:else}
    <div class="empty"><p>Loading…</p></div>
  {/if}
{:else}
  <form
    class="stack"
    onsubmit={(e) => {
      e.preventDefault()
      void save()
    }}
    novalidate>
    {#if conflict}
      <div class="alert" role="alert">
        <Icon name="warning" />
        <div class="grow">
          <strong>Settings changed on the server</strong>
          <p>Someone saved newer settings. Reload to get them; your edits here will be discarded.</p>
        </div>
        <button type="button" class="btn small" onclick={load}>Reload</button>
      </div>
    {/if}

    <fieldset class="card panel" disabled={saving}>
      <legend><h2>Downloads</h2></legend>
      <div class="three">
        <label class="field">
          <span>Simultaneous downloads</span>
          <input inputmode="numeric" bind:value={draft.concurrency} aria-invalid={err('concurrency') ? 'true' : undefined} />
          {#if err('concurrency')}<span class="hint error-text">{err('concurrency')}</span>{/if}
        </label>
        <label class="field">
          <span>Merge format</span>
          <input bind:value={draft.mergeFormat} aria-invalid={err('mergeFormat') ? 'true' : undefined} />
          {#if err('mergeFormat')}<span class="hint error-text">{err('mergeFormat')}</span>{/if}
        </label>
        <label class="field">
          <span>Minimum free space (GB)</span>
          <input inputmode="decimal" bind:value={draft.minFreeGb} aria-invalid={err('minFreeGb') ? 'true' : undefined} />
          {#if err('minFreeGb')}<span class="hint error-text">{err('minFreeGb')}</span>{/if}
        </label>
      </div>
      <p class="hint">0 disables the free-space check. New download attempts use the saved settings; running downloads continue.</p>
      {#if draft.downloadControls}
        <div class="three">
          <label class="field">
            <span>Speed limit per download (MB/s)</span>
            <input inputmode="decimal" bind:value={draft.rateMb} placeholder="Unlimited" aria-invalid={err('rateMb') ? 'true' : undefined} />
            {#if err('rateMb')}<span class="hint error-text">{err('rateMb')}</span>{/if}
          </label>
          <div class="field">
            <span id="window-label">Start downloads only between</span>
            <div class="window" role="group" aria-labelledby="window-label">
              <input type="time" bind:value={draft.windowStart} aria-label="Window start" aria-invalid={err('window') ? 'true' : undefined} />
              <span aria-hidden="true">–</span>
              <input type="time" bind:value={draft.windowEnd} aria-label="Window end" aria-invalid={err('window') ? 'true' : undefined} />
            </div>
            {#if err('window')}<span class="hint error-text">{err('window')}</span>{/if}
          </div>
          <label class="field">
            <span>Automatic retries</span>
            <input inputmode="numeric" bind:value={draft.autoRetries} aria-invalid={err('autoRetries') ? 'true' : undefined} />
            {#if err('autoRetries')}<span class="hint error-text">{err('autoRetries')}</span>{/if}
          </label>
        </div>
        <p class="hint">
          Leave the window blank to start downloads any time. It uses the server's clock; downloads that are running when it ends finish normally. A failed download
          is retried after a short pause, resuming where it stopped.
        </p>
      {/if}
    </fieldset>

    <fieldset class="card panel" disabled={saving}>
      <legend><h2>Maintenance</h2></legend>
      <label class="field narrow">
        <span>Keep history for (days)</span>
        <input inputmode="numeric" bind:value={draft.retentionDays} aria-invalid={err('retentionDays') ? 'true' : undefined} />
        {#if err('retentionDays')}<span class="hint error-text">{err('retentionDays')}</span>{/if}
      </label>
      <p class="hint">0 keeps history forever. Expired history records are removed after saving; media files are kept.</p>
      <label class="check"><input type="checkbox" bind:checked={draft.autoUpdate} disabled={!managed} /> Update yt-dlp automatically</label>
      {#if !managed}<p class="hint">Automatic updates are controlled externally when the server uses a custom yt-dlp binary.</p>{/if}
    </fieldset>

    <fieldset class="card panel" disabled={saving}>
      <legend><h2>Categories</h2></legend>
      <p class="hint">Enter paths on the server, such as <code>/media/movies</code>. Missing folders are created for new categories and must be writable. Configure Docker mounts on the host.</p>
      {#if draft.categories.length === 0}<p class="muted">Add a category to start downloading.</p>{/if}
      {#each draft.categories as c, i (c.key)}
        <div class="item">
          <div class="item-grid">
            <label class="field">
              <span>Label</span>
              <input bind:value={c.label} placeholder={c.existing ? '' : 'New category'} aria-invalid={err(`category.${i}.label`) ? 'true' : undefined} />
              {#if err(`category.${i}.label`)}<span class="hint error-text">{err(`category.${i}.label`)}</span>{/if}
            </label>
            <label class="field">
              <span>Destination folder</span>
              <input bind:value={c.path} placeholder="/media/…" spellcheck="false" aria-invalid={err(`category.${i}.path`) ? 'true' : undefined} />
              {#if err(`category.${i}.path`)}<span class="hint error-text">{err(`category.${i}.path`)}</span>{/if}
            </label>
            <div class="field">
              <span>Folder layout</span>
              <div class="chips" role="radiogroup" aria-label="Folder layout">
                {#each LAYOUTS as l (l)}
                  <button type="button" class="chip" role="radio" aria-checked={c.layout === l} onclick={() => (c.layout = l)}>{LAYOUT_LABEL[l]}</button>
                {/each}
              </div>
            </div>
          </div>
          {#if err(`category.${i}.id`)}<p class="hint error-text">{err(`category.${i}.id`)}</p>{/if}
          <button type="button" class="icon-btn" aria-label="Remove category {c.label}" title="Remove category" onclick={() => (draft!.categories = draft!.categories.filter((x) => x.key !== c.key))}>
            <Icon name="delete" size={20} />
          </button>
        </div>
      {/each}
      <div><button type="button" class="btn small outlined" onclick={() => draft!.categories.push(newCategory())}><Icon name="add" size={16} />Add category</button></div>
    </fieldset>

    <fieldset class="card panel" disabled={saving}>
      <legend><h2>Media servers</h2></legend>
      {#if draft.integrations.length === 0}<p class="muted">Connect Jellyfin or Plex to refresh its library after downloads finish.</p>{/if}
      {#each draft.integrations as s, i (s.id)}
        <div class="item">
          <div class="item-grid">
            <div class="field">
              <span>Media server type</span>
              <div class="chips" role="radiogroup" aria-label="Media server type">
                {#each [['jellyfin', 'Jellyfin'], ['plex', 'Plex']] as [t, label] (t)}
                  <button type="button" class="chip" role="radio" aria-checked={s.type === t} disabled={s.existing} onclick={() => (s.type = t as 'jellyfin' | 'plex')}>{label}</button>
                {/each}
              </div>
            </div>
            <label class="field">
              <span>Media server URL</span>
              <input bind:value={s.url} placeholder={s.type === 'plex' ? 'http://plex:32400' : 'http://jellyfin:8096'} spellcheck="false" aria-invalid={err(`integration.${i}.url`) ? 'true' : undefined} />
              {#if err(`integration.${i}.url`)}<span class="hint error-text">{err(`integration.${i}.url`)}</span>{/if}
            </label>
            <label class="field">
              <span>{s.type === 'plex' ? 'Plex token' : 'API key'}</span>
              <input type="password" bind:value={s.credential} autocomplete="off" placeholder={s.hasCredential ? '•••••••• (saved)' : ''} aria-invalid={err(`integration.${i}.credential`) ? 'true' : undefined} />
              {#if err(`integration.${i}.credential`)}<span class="hint error-text">{err(`integration.${i}.credential`)}</span>
              {:else if s.hasCredential}<span class="hint">A credential is saved on the server. Leave blank to keep it, or enter a replacement.</span>{/if}
            </label>
          </div>
          <div class="maps">
            <span class="sub">Path mappings</span>
            <p class="hint">Optional: rewrite a Fetcharr folder prefix to the path your media server sees.</p>
            {#each s.paths as p, j (p.key)}
              <div class="map">
                <input bind:value={p.from} placeholder="/media" aria-label="Fetcharr path prefix" spellcheck="false" aria-invalid={err(`integration.${i}.path.${j}`) ? 'true' : undefined} />
                <span aria-hidden="true">→</span>
                <input bind:value={p.to} placeholder="/data/media" aria-label="Media server path prefix" spellcheck="false" aria-invalid={err(`integration.${i}.path.${j}`) ? 'true' : undefined} />
                <button type="button" class="icon-btn" aria-label="Remove mapping" onclick={() => (s.paths = s.paths.filter((x) => x.key !== p.key))}><Icon name="close" size={18} /></button>
              </div>
              {#if err(`integration.${i}.path.${j}`)}<p class="hint error-text">{err(`integration.${i}.path.${j}`)}</p>{/if}
            {/each}
            <div><button type="button" class="btn small" onclick={() => s.paths.push(newMapping())}><Icon name="add" size={16} />Add path mapping</button></div>
          </div>
          <button type="button" class="icon-btn" aria-label="Remove media server" title="Remove media server" onclick={() => (draft!.integrations = draft!.integrations.filter((x) => x.id !== s.id))}>
            <Icon name="delete" size={20} />
          </button>
        </div>
      {/each}
      <div><button type="button" class="btn small outlined" onclick={() => draft!.integrations.push(newIntegration())}><Icon name="add" size={16} />Add media server</button></div>
    </fieldset>

    {#if draft.notifications}
      <fieldset class="card panel" disabled={saving}>
        <legend><h2>Notifications</h2></legend>
        <p class="hint">
          The server tells these targets when downloads finish, even when no app is open. A season is announced once, when all its episodes are done. Webhooks
          receive JSON with <code>text</code> and <code>content</code> fields.
        </p>
        {#if draft.notifications.length === 0}<p class="muted">Add an ntfy topic or a webhook to get notified on your devices.</p>{/if}
        {#each draft.notifications as n, i (n.id)}
          <div class="item">
            <div class="item-grid">
              <div class="field">
                <span>Type</span>
                <div class="chips" role="radiogroup" aria-label="Notification type">
                  {#each [['ntfy', 'ntfy'], ['webhook', 'Webhook']] as [t, label] (t)}
                    <button type="button" class="chip" role="radio" aria-checked={n.type === t} disabled={n.existing} onclick={() => (n.type = t as 'ntfy' | 'webhook')}>{label}</button>
                  {/each}
                </div>
              </div>
              <label class="field">
                <span>{n.type === 'ntfy' ? 'Topic URL' : 'Webhook URL'}</span>
                <input bind:value={n.url} placeholder={n.type === 'ntfy' ? 'https://ntfy.sh/my-fetcharr' : 'https://…'} spellcheck="false" aria-invalid={err(`notification.${i}.url`) ? 'true' : undefined} />
                {#if err(`notification.${i}.url`)}<span class="hint error-text">{err(`notification.${i}.url`)}</span>{/if}
              </label>
              <label class="field">
                <span>Access token (optional)</span>
                <input type="password" bind:value={n.credential} autocomplete="off" disabled={n.removeCredential} placeholder={n.hasCredential && !n.removeCredential ? '•••••••• (saved)' : ''} />
                <span class="hint">Sent as a Bearer token.{n.hasCredential ? ' Leave blank to keep the saved one.' : ''}</span>
              </label>
            </div>
            <div class="toggles">
              <label class="check"><input type="checkbox" bind:checked={n.onCompleted} /> Completed downloads</label>
              <label class="check"><input type="checkbox" bind:checked={n.onFailed} /> Failed downloads</label>
              {#if n.hasCredential}<label class="check"><input type="checkbox" bind:checked={n.removeCredential} /> Remove saved token</label>{/if}
              <span class="grow"></span>
              <button type="button" class="btn small" disabled={!savedUnchanged(n) || testing === n.id} onclick={() => sendTest(n)}>
                <Icon name="bell" size={16} />{testing === n.id ? 'Sending…' : 'Send test'}
              </button>
            </div>
            {#if !savedUnchanged(n)}
              <p class="hint">Save to send a test to this target.</p>
            {:else if testResult[n.id]}
              <p class="hint" class:error-text={!testResult[n.id]!.ok} role="status">{testResult[n.id]!.text}</p>
            {/if}
            <button type="button" class="icon-btn" aria-label="Remove notification target" title="Remove notification target" onclick={() => (draft!.notifications = draft!.notifications!.filter((x) => x.id !== n.id))}>
              <Icon name="delete" size={20} />
            </button>
          </div>
        {/each}
        <div><button type="button" class="btn small outlined" onclick={() => draft!.notifications!.push(newNotification())}><Icon name="add" size={16} />Add notification target</button></div>
      </fieldset>
    {/if}

    <div class="savebar">
      {#if error}
        <p class="error-text grow" role="alert">{error}</p>
      {:else if invalid}
        <p class="error-text grow" role="alert">Check the highlighted fields before saving.</p>
      {:else}
        <p class="muted grow">{dirty ? 'Unsaved changes' : 'All changes saved'}</p>
      {/if}
      <button class="btn filled" type="submit" disabled={saving || !dirty || conflict}>{saving ? 'Saving…' : 'Save'}</button>
    </div>
  </form>
{/if}

<style>
  .title {
    display: flex;
    align-items: center;
    gap: 4px;
    margin-left: -8px;
  }

  .stack {
    display: grid;
    gap: 14px;
  }

  fieldset {
    margin: 0;
    min-width: 0;
  }

  legend {
    float: left;
    width: 100%;
    padding: 0;
  }

  legend + * {
    clear: both;
  }

  .hint {
    font-size: 0.82rem;
    color: var(--fg-muted);
  }

  .three {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 12px;
  }

  .narrow {
    max-width: 240px;
  }

  .item {
    position: relative;
    display: grid;
    gap: 12px;
    padding: 16px 52px 16px 16px;
    border-radius: var(--radius-m);
    background: var(--surface-lowest);
    border: 1px solid color-mix(in srgb, var(--outline-variant) 60%, transparent);
  }

  .item > .icon-btn {
    position: absolute;
    top: 8px;
    right: 8px;
  }

  .item-grid {
    display: grid;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr);
    gap: 12px;
  }

  .item-grid > .field:last-child:nth-child(odd) {
    grid-column: 1 / -1;
  }

  .maps {
    display: grid;
    gap: 8px;
  }

  .sub {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--fg-muted);
  }

  .map {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr) auto;
    align-items: center;
    gap: 8px;
  }

  .window {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  .window input {
    flex: 1;
    min-width: 0;
  }

  .toggles {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 8px 20px;
  }

  .savebar {
    position: sticky;
    bottom: calc(12px + var(--bottom-nav-h));
    z-index: 5;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 10px 10px 18px;
    border-radius: var(--radius-l);
    background: var(--surface-high);
    box-shadow: var(--shadow);
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  .alert .btn {
    color: inherit;
  }

  @media (max-width: 640px) {
    .three,
    .item-grid {
      grid-template-columns: 1fr;
    }

    .map {
      grid-template-columns: minmax(0, 1fr) minmax(0, 1fr) auto;
    }

    .map span {
      display: none;
    }
  }
</style>
