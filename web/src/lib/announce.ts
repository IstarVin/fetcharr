// What to announce when downloads leave the queue, shared by the browser
// notifications. Mirrors the server's and Android's result notifications.
import { errorText, heading } from './format'
import type { Job } from './types'

export interface Announcement {
  title: string
  body: string
  tag: string
}

/** Per-batch counts of episodes that finished while this page watched them. */
export class BatchTally {
  #batches = new Map<string, { name: string; completed: number; failed: number }>()

  /**
   * Records a job that left the queue. batchActive says whether other
   * episodes of its batch are still in the queue. Returns what to announce.
   */
  finished(job: Job, batchActive: boolean): Announcement | null {
    if (job.status === 'cancelled' || (job.status !== 'completed' && job.status !== 'failed')) {
      return job.batch_id && !batchActive ? this.#flush(job.batch_id) : null
    }
    if (!job.batch_id) return single(job)
    const name = job.show_name?.trim() ? `${job.show_name.trim()}${job.season != null ? ` S${String(job.season).padStart(2, '0')}` : ''}` : 'Season'
    const t = this.#batches.get(job.batch_id) ?? { name, completed: 0, failed: 0 }
    if (job.status === 'completed') t.completed++
    else t.failed++
    this.#batches.set(job.batch_id, t)
    return batchActive ? null : this.#flush(job.batch_id)
  }

  #flush(batchId: string): Announcement | null {
    const t = this.#batches.get(batchId)
    this.#batches.delete(batchId)
    if (!t || t.completed + t.failed === 0) return null
    const parts = [t.completed && `${t.completed} downloaded`, t.failed && `${t.failed} failed`].filter(Boolean)
    return { title: 'Season finished', body: `${t.name} — ${parts.join(', ')}`, tag: batchId }
  }
}

function single(job: Job): Announcement {
  return job.status === 'completed'
    ? { title: 'Downloaded', body: heading(job), tag: job.id }
    : { title: 'Failed', body: `${heading(job)} — ${errorText(job.error_code)}`, tag: job.id }
}
