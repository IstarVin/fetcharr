// Pure helpers for WebSocket recovery and snapshot reconciliation.
import { ACTIVE_STATUSES, type Job } from './types'

/** A socket must stay open this long before the reconnect backoff resets. */
export const STABLE_MS = 5000

/** Seconds to wait before reconnect attempt number `attempt` (0-based): 1, 2, 4 … capped at 30. */
export function backoffDelay(attempt: number): number {
  return Math.min(30, 2 ** Math.min(Math.max(0, attempt), 5))
}

/** Ids of History rows that the snapshot lists as active (retried or restarted elsewhere). */
export function activeInSnapshot(historyJobs: readonly Job[], snapshot: readonly Job[]): string[] {
  const active = new Set(snapshot.filter((j) => ACTIVE_STATUSES.includes(j.status)).map((j) => j.id))
  return historyJobs.filter((j) => active.has(j.id)).map((j) => j.id)
}
