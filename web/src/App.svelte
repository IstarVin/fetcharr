<script lang="ts">
  import ConfirmHost from './components/ConfirmHost.svelte'
  import Icon, { type IconName } from './components/Icon.svelte'
  import Snackbar from './components/Snackbar.svelte'
  import './lib/browserNotify.svelte'
  import { titleProgress } from './lib/format'
  import { href, router, type Route } from './lib/router.svelte'
  import { live, session } from './lib/store.svelte'
  import Add from './routes/Add.svelte'
  import History from './routes/History.svelte'
  import Login from './routes/Login.svelte'
  import Queue from './routes/Queue.svelte'
  import ServerSettings from './routes/ServerSettings.svelte'
  import Settings from './routes/Settings.svelte'

  const tabs: { route: Route; label: string; icon: IconName }[] = [
    { route: 'queue', label: 'Queue', icon: 'download' },
    { route: 'history', label: 'History', icon: 'history' },
    { route: 'settings', label: 'Settings', icon: 'settings' },
  ]

  // Server settings belongs to the Settings tab; Add to Queue.
  const tabOf = (r: Route): Route => (r === 'server-settings' ? 'settings' : r === 'add' ? 'queue' : r)
  const activeCount = $derived(live.jobs.size)

  const PAGE_TITLE: Record<Route, string> = {
    queue: 'Queue',
    history: 'History',
    add: 'Add download',
    settings: 'Settings',
    'server-settings': 'Server settings',
  }
  // Running downloads show in the tab title, e.g. "(2) 43% · Queue · Fetcharr".
  const title = $derived(session.status === 'ready' ? `${titleProgress([...live.jobs.values()])}${PAGE_TITLE[router.route]} · Fetcharr` : 'Fetcharr')

  void session.boot()
</script>

<svelte:head><title>{title}</title></svelte:head>

{#if session.status === 'loading'}
  <div class="center" aria-busy="true"><span class="visually-hidden">Loading</span><div class="spinner"></div></div>
{:else if session.status === 'login'}
  <Login />
{:else if session.status === 'unreachable'}
  <div class="center">
    <div class="empty">
      <div class="glyph"><Icon name="cloud_off" size={32} /></div>
      <h1>Can't reach Fetcharr</h1>
      <p>{session.error ?? 'The server did not respond.'} Check that the server is running, then try again.</p>
      <button class="btn filled" onclick={() => session.boot()}>Try again</button>
    </div>
  </div>
{:else}
  <header>
    <div class="bar">
      <a class="brand" href={href('queue')} aria-label="Fetcharr queue">
        <span class="logo"><Icon name="download" size={18} /></span>
        <span>Fetcharr</span>
      </a>
      <nav class="tabs" aria-label="Main">
        {#each tabs as t (t.route)}
          <a href={href(t.route)} aria-current={tabOf(router.route) === t.route ? 'page' : undefined}>
            {t.label}
            {#if t.route === 'queue' && activeCount > 0}<span class="count">{activeCount}</span>{/if}
          </a>
        {/each}
      </nav>
      <a class="btn filled add" href={href('add')} aria-current={router.route === 'add' ? 'page' : undefined}>
        <Icon name="add" size={18} /><span>Add download</span>
      </a>
    </div>
  </header>

  <main>
    {#if session.info?.setup_required && router.route !== 'server-settings'}
      <div class="alert info setup">
        <Icon name="folder" />
        <div>
          <strong>Set up a library folder</strong>
          <p>This server has no categories yet. Add a destination folder to start downloading.</p>
        </div>
        <a class="btn small" href={href('server-settings')}>Set up categories</a>
      </div>
    {/if}
    {#if router.route === 'queue'}
      <Queue />
    {:else if router.route === 'history'}
      <History />
    {:else if router.route === 'add'}
      {#key router.query}<Add />{/key}
    {:else if router.route === 'settings'}
      <Settings />
    {:else if router.route === 'server-settings'}
      <ServerSettings />
    {/if}
  </main>

  <nav class="bottom" aria-label="Main">
    {#each tabs as t (t.route)}
      <a href={href(t.route)} aria-current={tabOf(router.route) === t.route ? 'page' : undefined}>
        <span class="ind"><Icon name={t.icon} size={22} /></span>
        <span>{t.label}</span>
      </a>
    {/each}
  </nav>
{/if}

<ConfirmHost />
<Snackbar />

<style>
  .center {
    display: grid;
    place-items: center;
    min-height: 100dvh;
    padding: var(--gutter);
  }

  .spinner {
    width: 32px;
    height: 32px;
    border: 3px solid color-mix(in srgb, var(--primary) 20%, transparent);
    border-top-color: var(--primary);
    border-radius: 50%;
    animation: spin 0.8s linear infinite;
  }

  @keyframes spin {
    to {
      transform: rotate(1turn);
    }
  }

  header {
    position: sticky;
    top: 0;
    z-index: 20;
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: blur(12px);
    border-bottom: 1px solid color-mix(in srgb, var(--outline-variant) 50%, transparent);
  }

  .bar {
    display: flex;
    align-items: center;
    gap: 24px;
    max-width: calc(var(--content) + 2 * var(--gutter));
    height: var(--header-h);
    margin: 0 auto;
    padding: 0 var(--gutter);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    color: var(--fg);
    font-weight: 700;
    font-size: 1.05rem;
    text-decoration: none;
  }

  .logo {
    display: grid;
    place-items: center;
    width: 30px;
    height: 30px;
    border-radius: 9px;
    background: #1e6fd9;
    color: #fff;
  }

  .tabs {
    display: flex;
    gap: 4px;
    flex: 1;
  }

  .tabs a {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    height: 36px;
    padding: 0 14px;
    border-radius: 999px;
    color: var(--fg-muted);
    font-weight: 600;
    font-size: 0.92rem;
    text-decoration: none;
  }

  .tabs a:hover {
    background: color-mix(in srgb, var(--fg) 6%, transparent);
    color: var(--fg);
  }

  .tabs a[aria-current='page'] {
    background: var(--secondary-container);
    color: var(--on-secondary-container);
  }

  .count {
    min-width: 20px;
    padding: 0 6px;
    border-radius: 999px;
    background: var(--primary);
    color: var(--on-primary);
    font-size: 0.72rem;
    line-height: 20px;
    text-align: center;
  }

  .add {
    margin-left: auto;
  }

  main {
    max-width: calc(var(--content) + 2 * var(--gutter));
    margin: 0 auto;
    padding: 20px var(--gutter) calc(48px + var(--bottom-nav-h));
  }

  .setup {
    align-items: center;
    margin-bottom: 16px;
  }

  .setup div {
    flex: 1;
  }

  .setup .btn {
    color: inherit;
  }

  .bottom {
    display: none;
  }

  @media (max-width: 640px) {
    :global(:root) {
      --bottom-nav-h: 72px;
    }

    .tabs {
      display: none;
    }

    .bar {
      justify-content: space-between;
    }

    .add span {
      display: none;
    }

    .add {
      width: 40px;
      padding: 0;
    }

    main {
      padding-top: 12px;
    }

    .bottom {
      position: fixed;
      inset: auto 0 0;
      z-index: 20;
      display: grid;
      grid-template-columns: repeat(3, 1fr);
      height: calc(var(--bottom-nav-h) + env(safe-area-inset-bottom));
      padding-bottom: env(safe-area-inset-bottom);
      background: var(--surface);
      border-top: 1px solid color-mix(in srgb, var(--outline-variant) 50%, transparent);
    }

    .bottom a {
      display: grid;
      justify-items: center;
      align-content: center;
      gap: 4px;
      color: var(--fg-muted);
      font-size: 0.75rem;
      font-weight: 600;
      text-decoration: none;
    }

    .ind {
      display: grid;
      place-items: center;
      width: 60px;
      height: 30px;
      border-radius: 999px;
    }

    .bottom a[aria-current='page'] {
      color: var(--fg);
    }

    .bottom a[aria-current='page'] .ind {
      background: var(--secondary-container);
      color: var(--on-secondary-container);
    }
  }
</style>
