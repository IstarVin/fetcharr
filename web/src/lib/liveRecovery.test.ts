import { describe, expect, it } from 'vitest'
import { activeInSnapshot, backoffDelay, STABLE_MS } from './liveRecovery'
import type { Job } from './types'

const job = (id: string, status: Job['status']) => ({ id, status }) as Job

describe('liveRecovery', () => {
  it('backs off exponentially up to 30 s', () => {
    expect([0, 1, 2, 3, 4, 5, 6, 20].map(backoffDelay)).toEqual([1, 2, 4, 8, 16, 30, 30, 30])
    expect(backoffDelay(-1)).toBe(1)
  })

  it('waits for a stable connection before resetting', () => {
    expect(STABLE_MS).toBeGreaterThan(1000)
  })

  it('finds history rows that are active in a snapshot', () => {
    const history = [job('a', 'failed'), job('b', 'completed'), job('c', 'cancelled')]
    const snapshot = [job('a', 'pending'), job('c', 'downloading'), job('d', 'paused')]
    expect(activeInSnapshot(history, snapshot)).toEqual(['a', 'c'])
    expect(activeInSnapshot(history, [])).toEqual([])
    expect(activeInSnapshot([], snapshot)).toEqual([])
    expect(activeInSnapshot(history, [job('b', 'completed')])).toEqual([])
  })
})
