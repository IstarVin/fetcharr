// Job actions shared by Queue and History: call the API, apply the returned
// job right away (the WebSocket event follows), and report failures.
import { api, ApiError, errorMessage } from './api'
import { heading } from './format'
import { confirm, confirmer, history, live, toasts } from './store.svelte'
import type { Job } from './types'

export async function jobAction(job: Job, action: 'pause' | 'resume' | 'cancel'): Promise<void> {
  if (action === 'cancel') {
    const ok = await confirm({
      title: 'Cancel this download?',
      body: `${heading(job)} will be stopped and removed from the queue.`,
      confirm: 'Yes, cancel',
      cancel: 'Keep',
      danger: true,
    })
    if (!ok) return
  }
  try {
    live.upsert(await api.jobAction(job.id, action))
  } catch (e) {
    toasts.show(errorMessage(e))
  }
}

export async function batchAction(batchId: string, title: string, action: 'pause' | 'resume' | 'cancel'): Promise<void> {
  if (action === 'cancel') {
    const ok = await confirm({
      title: `Cancel all episodes of ${title}?`,
      body: 'Every unfinished episode in this batch, including ones in other sections, will be stopped.',
      confirm: 'Yes, cancel',
      cancel: 'Keep',
      danger: true,
    })
    if (!ok) return
  }
  try {
    const res = await api.batchAction(batchId, action)
    for (const j of res.jobs) live.upsert(j)
  } catch (e) {
    toasts.show(errorMessage(e))
  }
}

/** Moves a queued job (or every queued episode of a batch) to the front of the queue. */
export async function downloadNext(target: { job: Job } | { batchId: string }): Promise<void> {
  try {
    if ('job' in target) {
      live.upsert(await api.prioritize(target.job.id))
    } else {
      for (const j of (await api.batchAction(target.batchId, 'prioritize')).jobs) live.upsert(j)
    }
    toasts.show('Moved to the front of the queue')
  } catch (e) {
    toasts.show(errorMessage(e))
  }
}

/** Retry; `already_exists` asks before overwriting the library file. */
export async function retryJob(job: Job): Promise<void> {
  let overwrite = false
  if (job.error_code === 'already_exists') {
    overwrite = await confirm({
      title: 'File already exists',
      body: 'A file with this name already exists. Replace it?',
      confirm: 'Replace',
    })
    if (!overwrite) return
  }
  try {
    live.upsert(await api.retry(job.id, overwrite ? { overwrite: true } : undefined))
    toasts.show('Added to queue')
  } catch (e) {
    toasts.show(e instanceof ApiError && e.code === 'duplicate' ? 'Already in the queue' : errorMessage(e))
  }
}

/** Retries failed downloads after confirming; falls back to one request per loaded job on servers without the bulk endpoint. */
export async function retryFailed(): Promise<void> {
  const failed = history.jobs.filter((j) => j.status === 'failed')
  if (failed.length === 0) return
  const ok = await confirm({
    title: 'Retry failed downloads?',
    body: 'Every failed download will be added back to the queue.',
    confirm: 'Retry',
  })
  if (!ok) return
  let retried = 0
  try {
    try {
      const res = await api.retryFailed()
      for (const j of res.jobs) live.upsert(j)
      retried = res.jobs.length
    } catch (e) {
      if (!(e instanceof ApiError && (e.status === 404 || e.status === 405))) throw e
      for (const j of failed) {
        try {
          live.upsert(await api.retry(j.id))
          retried++
        } catch (err) {
          // Already queued or unretryable: skip, and stop only when the server is unreachable.
          if (err instanceof ApiError && err.status === 0) throw err
        }
      }
    }
    toasts.show(retried === 1 ? 'Retried 1 download' : `Retried ${retried} downloads`)
  } catch (e) {
    toasts.show(retried > 0 ? `Retried ${retried}, then: ${errorMessage(e)}` : errorMessage(e))
  }
}

export async function deleteJob(job: Job): Promise<void> {
  const res = await confirmer.ask({
    title: 'Delete from history?',
    body: `“${heading(job)}” will be removed from history.`,
    confirm: 'Delete',
    danger: true,
    checkbox: job.status === 'completed' ? 'Also delete file from disk' : undefined,
  })
  if (!res.ok) return
  try {
    await api.deleteJob(job.id, job.status === 'completed' && res.checked)
    history.remove([job.id])
  } catch (e) {
    toasts.show(errorMessage(e))
  }
}

export async function clearHistory(): Promise<void> {
  const ok = await confirm({
    title: 'Clear history?',
    body: 'This removes every finished download from the list. Downloaded files are kept.',
    confirm: 'Clear',
    danger: true,
  })
  if (!ok) return
  try {
    const res = await api.clearHistory()
    await history.load()
    toasts.show(res.deleted === 1 ? 'Removed 1 download' : `Removed ${res.deleted} downloads`)
  } catch (e) {
    toasts.show(errorMessage(e))
  }
}

/** Copies text; falls back to a hidden textarea on plain-http origins where the Clipboard API is unavailable. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through
  }
  const ta = document.createElement('textarea')
  ta.value = text
  ta.setAttribute('readonly', '')
  ta.style.position = 'fixed'
  ta.style.opacity = '0'
  document.body.append(ta)
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  ta.remove()
  return ok
}
