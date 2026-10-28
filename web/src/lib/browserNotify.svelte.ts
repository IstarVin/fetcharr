// Opt-in browser notifications for finished downloads while this tab is in
// the background. Like Android, a season is announced once when its last
// episode finishes; cancelled jobs are never announced.
import { BatchTally } from './announce'
import { live, readPref, writePref } from './store.svelte'
import type { Job } from './types'

const PREF = 'browserNotifications'

class BrowserNotifications {
  readonly supported = typeof window !== 'undefined' && 'Notification' in window
  enabled = $state(this.supported && readPref(PREF) === 'on' && Notification.permission === 'granted')
  denied = $state(this.supported && Notification.permission === 'denied')
  #tally = new BatchTally()

  async enable(): Promise<void> {
    if (!this.supported) return
    const permission = await Notification.requestPermission()
    this.denied = permission === 'denied'
    this.enabled = permission === 'granted'
    writePref(PREF, this.enabled ? 'on' : null)
  }

  disable(): void {
    this.enabled = false
    writePref(PREF, null)
  }

  /** Called when a job leaves the live queue. */
  finished(job: Job, batchActive: boolean): void {
    const a = this.#tally.finished(job, batchActive)
    if (!a || !this.enabled || !document.hidden) return
    try {
      const n = new Notification(a.title, { body: a.body, tag: a.tag, icon: 'favicon.svg' })
      n.onclick = () => {
        window.focus()
        location.hash = '#/history'
        n.close()
      }
    } catch {
      // Some browsers only allow notifications from a service worker.
    }
  }
}

export const browserNotifications = new BrowserNotifications()
live.onFinished = (job, batchActive) => browserNotifications.finished(job, batchActive)
