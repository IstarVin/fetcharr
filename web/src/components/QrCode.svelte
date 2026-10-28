<script lang="ts">
  import { encode } from 'uqr'

  // Always dark on white, whatever the theme: scanners expect that contrast.
  let { text, label, size = 200 }: { text: string; label: string; size?: number } = $props()

  const qr = $derived(encode(text, { ecc: 'M', border: 2 }))
  const path = $derived(
    qr.data.flatMap((row, y) => row.map((dark, x) => (dark ? `M${x} ${y}h1v1h-1z` : ''))).join(''),
  )
</script>

<svg width={size} height={size} viewBox="0 0 {qr.size} {qr.size}" shape-rendering="crispEdges" role="img" aria-label={label}>
  <rect width={qr.size} height={qr.size} fill="#fff" />
  <path d={path} fill="#000" />
</svg>

<style>
  svg {
    display: block;
    max-width: 100%;
    height: auto;
    border-radius: var(--radius-s);
  }
</style>
