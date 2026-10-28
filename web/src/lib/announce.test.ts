import { describe, expect, it } from 'vitest'
import completed from '../../../contract/job-completed.json'
import failed from '../../../contract/job-failed.json'
import { BatchTally } from './announce'
import type { Job } from './types'

const job = (j: unknown, over: Partial<Job> = {}) => ({ ...(j as Job), ...over })

describe('BatchTally', () => {
  it('announces single jobs right away', () => {
    const t = new BatchTally()
    expect(t.finished(job(completed, { batch_id: null }), false)?.title).toBe('Downloaded')
    expect(t.finished(job(failed, { batch_id: null }), false)?.title).toBe('Failed')
    expect(t.finished(job(completed, { batch_id: null, status: 'cancelled' }), false)).toBeNull()
  })

  it('summarizes a batch once its last episode leaves the queue', () => {
    const t = new BatchTally()
    const ep = (status: Job['status'], episode: number) => job(completed, { batch_id: 'b', show_name: 'Severance', season: 2, episode, status })
    expect(t.finished(ep('completed', 1), true)).toBeNull()
    expect(t.finished(ep('failed', 2), true)).toBeNull()
    expect(t.finished(ep('cancelled', 3), false)).toEqual({ title: 'Season finished', body: 'Severance S02 — 1 downloaded, 1 failed', tag: 'b' })
  })

  it('stays quiet when every episode was cancelled', () => {
    const t = new BatchTally()
    expect(t.finished(job(completed, { batch_id: 'c', status: 'cancelled' }), false)).toBeNull()
  })
})
