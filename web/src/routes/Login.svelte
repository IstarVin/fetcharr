<script lang="ts">
  import Icon from '../components/Icon.svelte'
  import { ApiError, errorMessage } from '../lib/api'
  import { session } from '../lib/store.svelte'

  let token = $state('')
  let show = $state(false)
  let busy = $state(false)
  let error = $state<string | null>(null)

  async function submit(e: SubmitEvent) {
    e.preventDefault()
    if (!token.trim() || busy) return
    busy = true
    error = null
    try {
      await session.login(token.trim())
      token = ''
    } catch (err) {
      error = err instanceof ApiError && err.status === 401 ? 'Wrong API token' : errorMessage(err)
    } finally {
      busy = false
    }
  }
</script>

<div class="wrap">
  <form class="card panel" onsubmit={submit}>
    <div class="head">
      <span class="logo"><Icon name="download" size={26} /></span>
      <h1>Fetcharr</h1>
      <p class="muted">Enter the server's API token to continue.</p>
    </div>
    <label class="field">
      <span>API token</span>
      <div class="secret">
        <input
          type={show ? 'text' : 'password'}
          bind:value={token}
          autocomplete="current-password"
          aria-invalid={error ? 'true' : undefined}
          disabled={busy}
          required />
        <button type="button" class="icon-btn" onclick={() => (show = !show)} aria-label={show ? 'Hide token' : 'Show token'} aria-pressed={show}>
          <Icon name={show ? 'visibility_off' : 'visibility'} size={18} />
        </button>
      </div>
      {#if error}<span class="hint error-text" role="alert">{error}</span>{/if}
    </label>
    <button class="btn filled" type="submit" disabled={busy || !token.trim()}>{busy ? 'Signing in…' : 'Sign in'}</button>
    <p class="muted small">The token is set as <code>server.auth_token</code> in the server's config file. This browser keeps a sign-in cookie for 30 days.</p>
  </form>
</div>

<style>
  .wrap {
    display: grid;
    place-items: center;
    min-height: 100dvh;
    padding: var(--gutter);
  }

  form {
    width: min(400px, 100%);
    padding: 32px 28px;
    background: var(--surface-lowest);
    box-shadow: var(--shadow);
  }

  .head {
    display: grid;
    justify-items: center;
    gap: 6px;
    margin-bottom: 8px;
    text-align: center;
  }

  .logo {
    display: grid;
    place-items: center;
    width: 52px;
    height: 52px;
    margin-bottom: 6px;
    border-radius: 16px;
    background: #1e6fd9;
    color: #fff;
  }

  .secret {
    display: flex;
    gap: 4px;
    align-items: center;
  }

  .small {
    font-size: 0.8rem;
  }
</style>
