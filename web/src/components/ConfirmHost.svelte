<script lang="ts">
  import { confirmer } from '../lib/store.svelte'
  import Dialog from './Dialog.svelte'

  let checked = $state(false)
  const req = $derived(confirmer.current)

  $effect(() => {
    if (req) checked = false
  })
</script>

<Dialog open={!!req} title={req?.title ?? ''} onclose={() => req?.resolve({ ok: false, checked: false })}>
  {#if req?.body}<p>{req.body}</p>{/if}
  {#if req?.checkbox}
    <label class="check"><input type="checkbox" bind:checked /> {req.checkbox}</label>
  {/if}
  {#snippet actions()}
    <button class="btn" onclick={() => req?.resolve({ ok: false, checked: false })}>{req?.cancel ?? 'Cancel'}</button>
    <button class="btn filled" class:danger={req?.danger} onclick={() => req?.resolve({ ok: true, checked })}>{req?.confirm}</button>
  {/snippet}
</Dialog>
