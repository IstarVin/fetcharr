<script lang="ts">
  import { live } from '../lib/store.svelte'
  import Icon from './Icon.svelte'
</script>

{#if live.connection !== 'connected'}
  <div class="banner" class:off={live.connection === 'disconnected'} role="status">
    <Icon name={live.connection === 'disconnected' ? 'cloud_off' : 'retry'} size={18} />
    <span>
      {#if live.connection === 'connecting'}
        Connecting…
      {:else if live.retryIn != null}
        Disconnected · retrying in {live.retryIn} s
      {:else}
        Disconnected
      {/if}
    </span>
    {#if live.connection === 'disconnected'}
      <button class="btn small" onclick={() => live.reconnect()}>Retry now</button>
    {/if}
  </div>
{/if}

<style>
  .banner {
    display: flex;
    align-items: center;
    gap: 10px;
    min-height: 44px;
    padding: 4px 8px 4px 14px;
    margin-bottom: 12px;
    border-radius: var(--radius-m);
    background: var(--secondary-container);
    color: var(--on-secondary-container);
    font-size: 0.9rem;
  }

  .banner.off {
    background: var(--error-container);
    color: var(--on-error-container);
  }

  span {
    flex: 1;
  }

  .btn {
    color: inherit;
  }
</style>
