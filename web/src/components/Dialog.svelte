<script lang="ts">
  import type { Snippet } from 'svelte'

  let {
    open,
    title,
    onclose,
    wide = false,
    children,
    actions,
  }: {
    open: boolean
    title: string
    /** Called on Escape or a backdrop click; the owner decides whether to close. */
    onclose: () => void
    wide?: boolean
    children: Snippet
    actions?: Snippet
  } = $props()

  const uid = $props.id()
  let el: HTMLDialogElement | undefined = $state()

  $effect(() => {
    if (!el) return
    if (open && !el.open) el.showModal()
    else if (!open && el.open) el.close()
  })
</script>

<dialog
  bind:this={el}
  class:wide
  aria-labelledby="{uid}-title"
  oncancel={(e) => {
    e.preventDefault()
    onclose()
  }}
  onclick={(e) => {
    if (e.target === el) onclose()
  }}>
  {#if open}
    <div class="body">
      <h2 id="{uid}-title">{title}</h2>
      <div class="content">{@render children()}</div>
      {#if actions}<div class="actions">{@render actions()}</div>{/if}
    </div>
  {/if}
</dialog>

<style>
  dialog {
    width: min(440px, calc(100vw - 32px));
    max-height: calc(100dvh - 48px);
    padding: 0;
    border: 0;
    border-radius: 24px;
    background: var(--surface-high);
    color: var(--fg);
    box-shadow: 0 12px 40px rgb(0 0 0 / 0.25);
  }

  dialog.wide {
    width: min(760px, calc(100vw - 32px));
  }

  dialog::backdrop {
    background: var(--scrim);
  }

  .body {
    display: grid;
    gap: 16px;
    padding: 24px;
  }

  h2 {
    font-size: 1.25rem;
  }

  .content {
    display: grid;
    gap: 12px;
    color: var(--fg-muted);
    min-width: 0;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: 8px;
  }
</style>
