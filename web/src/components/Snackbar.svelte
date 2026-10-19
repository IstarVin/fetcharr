<script lang="ts">
  import { toasts } from '../lib/store.svelte'
</script>

<div class="host" role="status" aria-live="polite">
  {#each toasts.items as t (t.id)}
    <div class="toast">
      <span>{t.text}</span>
      {#if t.action}
        <button
          class="btn small"
          onclick={() => {
            t.action?.run()
            toasts.dismiss(t.id)
          }}>{t.action.label}</button>
      {/if}
    </div>
  {/each}
</div>

<style>
  .host {
    position: fixed;
    left: 50%;
    bottom: calc(16px + var(--bottom-nav-h) + env(safe-area-inset-bottom));
    z-index: 50;
    display: grid;
    gap: 8px;
    width: min(560px, calc(100vw - 32px));
    transform: translateX(-50%);
    pointer-events: none;
  }

  .toast {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    min-height: 48px;
    padding: 8px 8px 8px 16px;
    border-radius: var(--radius-s);
    background: var(--fg);
    color: var(--bg);
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.25);
    pointer-events: auto;
    animation: rise 160ms ease-out;
  }

  .toast .btn {
    color: color-mix(in srgb, var(--primary) 60%, var(--bg));
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
  }
</style>
