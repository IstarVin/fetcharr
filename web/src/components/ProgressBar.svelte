<script lang="ts">
  let { value, indeterminate = false, label }: { value: number | null; indeterminate?: boolean; label: string } = $props()
</script>

<div
  class="track"
  role="progressbar"
  aria-label={label}
  aria-valuemin={indeterminate ? undefined : 0}
  aria-valuemax={indeterminate ? undefined : 100}
  aria-valuenow={indeterminate || value == null ? undefined : Math.round(value * 100)}>
  {#if indeterminate}
    <div class="bar indeterminate"></div>
  {:else if value != null}
    <div class="bar" style:width="{Math.max(0, Math.min(1, value)) * 100}%"></div>
  {/if}
</div>

<style>
  .track {
    position: relative;
    height: 6px;
    overflow: hidden;
    border-radius: 999px;
    background: color-mix(in srgb, var(--primary) 16%, transparent);
  }

  .bar {
    height: 100%;
    border-radius: inherit;
    background: var(--primary);
    transition: width 400ms ease;
  }

  .indeterminate {
    position: absolute;
    width: 40%;
    animation: slide 1.4s ease-in-out infinite;
  }

  @keyframes slide {
    from {
      left: -40%;
    }
    to {
      left: 100%;
    }
  }
</style>
