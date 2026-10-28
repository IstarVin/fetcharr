<script lang="ts">
  import BatchGroup from '../components/BatchGroup.svelte'
  import ConnectionBanner from '../components/ConnectionBanner.svelte'
  import Icon from '../components/Icon.svelte'
  import JobCard from '../components/JobCard.svelte'
  import { buildQueueSections, SECTION_LABEL, windowHint, windowOpensAt } from '../lib/format'
  import { href } from '../lib/router.svelte'
  import { live, session } from '../lib/store.svelte'

  const sections = $derived(buildQueueSections([...live.jobs.values()]))
  const opensAt = $derived(windowOpensAt(session.info?.download_window))

  function refresh() {
    live.reconnect()
    void session.refreshInfo()
  }
</script>


<div class="page-head">
  <h1>Queue</h1>
  <button class="icon-btn" onclick={refresh} aria-label="Refresh" title="Refresh"><Icon name="retry" /></button>
</div>

<ConnectionBanner />

{#if sections.length === 0}
  {#if live.connection === 'connected'}
    <div class="empty">
      <div class="glyph"><Icon name="download" size={32} /></div>
      <h2>Nothing downloading</h2>
      <p>Add a link here or share one from the Android app. Progress shows up here.</p>
      <a class="btn filled" href={href('add')}><Icon name="add" size={18} />Add download</a>
    </div>
  {:else}
    <div class="empty">
      <h2>Waiting for the server</h2>
      <p>The queue will show up once Fetcharr is reachable.</p>
    </div>
  {/if}
{:else}
  {#each sections as s (s.kind)}
    <section aria-labelledby="sec-{s.kind}">
      <h2 class="section-title" id="sec-{s.kind}">
        <span>{SECTION_LABEL[s.kind]}</span>
        {#if s.speedLabel}<span class="speed">{s.speedLabel}</span>{/if}
        {#if s.kind === 'queued' && opensAt}<span class="window"><Icon name="schedule" size={16} />{windowHint(opensAt)}</span>{/if}
      </h2>
      <div class="list">
        {#each s.items as item, i (item.key)}
          {#if item.kind === 'job'}
            <JobCard job={item.job} first={s.kind === 'queued' && i === 0} />
          {:else}
            <BatchGroup batchId={item.batchId} title={item.title} jobs={item.jobs} progress={item.progress} section={s.kind} first={s.kind === 'queued' && i === 0} />
          {/if}
        {/each}
      </div>
    </section>
  {/each}
{/if}

<style>
  .list {
    display: grid;
    gap: 10px;
  }

  .window {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    text-transform: none;
    letter-spacing: 0;
    color: var(--fg-muted);
  }

  .speed {
    text-transform: none;
    letter-spacing: 0;
    font-variant-numeric: tabular-nums;
    color: var(--primary);
  }
</style>
