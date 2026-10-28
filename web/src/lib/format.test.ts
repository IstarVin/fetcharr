import { describe, expect, it } from 'vitest'
import jobsList from '../../../contract/jobs-list.json'
import downloading from '../../../contract/job-downloading.json'
import paused from '../../../contract/job-paused.json'
import failed from '../../../contract/job-failed.json'
import completed from '../../../contract/job-completed.json'
import type { Job } from './types'
import {
  actionsFor,
  batchAddedMessage,
  batchTitle,
  buildQueueSections,
  bytes,
  bytesToGb,
  combinedProgress,
  errorText,
  eta,
  gbToBytes,
  groupByDay,
  heading,
  historyStatusLine,
  speed,
  statsLine,
  tidyReleaseName,
} from './format'

const job = (j: unknown) => j as Job
const all = (jobsList as { jobs: unknown[] }).jobs.map(job)

describe('formatters', () => {
  it('formats bytes like Android', () => {
    expect(bytes(null)).toBeNull()
    expect(bytes(-1)).toBeNull()
    expect(bytes(512)).toBe('512 B')
    expect(bytes(1024)).toBe('1.0 KiB')
    expect(bytes(3250585)).toBe('3.1 MiB')
    expect(bytes(1048575)).toBe('1.0 MiB')
    expect(speed(3250585)).toBe('3.1 MiB/s')
  })

  it('formats eta', () => {
    expect(eta(null)).toBeNull()
    expect(eta(132)).toBe('2:12')
    expect(eta(3725)).toBe('1:02:05')
  })

  it('converts decimal GB', () => {
    expect(bytesToGb(5_000_000_000)).toBe('5')
    expect(bytesToGb(1_500_000_000)).toBe('1.5')
    expect(bytesToGb(0)).toBe('0')
    expect(bytesToGb(1)).toBe('0.000000001')
    expect(gbToBytes('5')).toBe(5_000_000_000)
    expect(gbToBytes('0.5')).toBe(500_000_000)
    expect(gbToBytes(' 2. ')).toBe(2_000_000_000)
    expect(gbToBytes('-1')).toBeNull()
    expect(gbToBytes('1.0000000001')).toBeNull()
    expect(gbToBytes('abc')).toBeNull()
  })
})

describe('job text', () => {
  it('builds headings', () => {
    const d = job(downloading)
    expect(heading(d)).toMatch(/^Severance — S02E0\d$/)
    expect(heading({ ...d, show_name: null, title: 'Dune Part Two', year: 2024, season: null, episode: null })).toBe('Dune Part Two (2024)')
    expect(heading({ ...d, show_name: null, title: null, year: null, season: null, episode: null })).toBe(d.url)
  })

  it('tidies scene names', () => {
    expect(tidyReleaseName('Spider-Man.Brand.New.Day.2026.2160p.MA.WEB-DL')).toBe('Spider-Man Brand New Day')
    expect(tidyReleaseName('Already Spaced 2026')).toBe('Already Spaced 2026')
  })

  it('shows live stats only while downloading', () => {
    const d = job(downloading)
    expect(statsLine(d)).toContain(' · ')
    expect(statsLine(d)).toContain('left')
    const p = job(paused)
    expect(statsLine(p) ?? '').not.toContain('left')
  })

  it('maps actions', () => {
    expect(actionsFor('pending')).toEqual(['pause', 'cancel'])
    expect(actionsFor('processing')).toEqual(['cancel'])
    expect(actionsFor('paused')).toEqual(['resume', 'cancel'])
    expect(actionsFor('completed')).toEqual([])
  })

  it('maps history status lines', () => {
    expect(historyStatusLine(job(completed))).toBe(job(completed).file_path)
    expect(historyStatusLine(job(failed))).toBe(errorText(job(failed).error_code))
    expect(errorText('mystery')).toBe('Download failed')
  })
})

describe('queue', () => {
  it('builds sections from the contract job list', () => {
    const sections = buildQueueSections(all)
    expect(sections.map((s) => s.kind)).toEqual(['active', 'queued', 'paused'])
    const count = sections.flatMap((s) => s.items).reduce((n, i) => n + (i.kind === 'batch' ? i.jobs.length : 1), 0)
    expect(count).toBe(all.filter((j) => ['pending', 'downloading', 'processing', 'paused'].includes(j.status)).length)
  })

  it('groups batches and titles them', () => {
    const d = job(downloading)
    const members = [d, { ...d, id: 'x', episode: 2 }]
    expect(batchTitle(members)).toBe('Severance — Season 2')
    const sections = buildQueueSections(members)
    expect(sections[0]!.items).toHaveLength(1)
    expect(sections[0]!.items[0]!.kind).toBe('batch')
  })

  it('combines progress by bytes when known', () => {
    const base = job(downloading)
    const a = { ...base, progress: { percent: 50, downloaded_bytes: 50, total_bytes: 100, speed_bps: 0, eta_seconds: 0 } }
    const b = { ...base, progress: { percent: 10, downloaded_bytes: 30, total_bytes: 300, speed_bps: 0, eta_seconds: 0 } }
    expect(combinedProgress([a, b])).toBeCloseTo(80 / 400)
    expect(combinedProgress([{ ...a, progress: { ...a.progress, total_bytes: null } }, b])).toBeCloseTo(0.3)
  })
})

describe('history groups', () => {
  it('labels today and yesterday', () => {
    const now = new Date(2026, 9, 14, 18, 0)
    const at = (d: Date) => ({ ...job(completed), id: String(d.getTime()), finished_at: d.toISOString() })
    const groups = groupByDay([at(new Date(2026, 9, 14, 9)), at(new Date(2026, 9, 13, 9)), at(new Date(2025, 0, 2, 9))], now, 'en-GB')
    expect(groups.map((g) => g.label)).toEqual(['Today', 'Yesterday', 'Thu, 2 Jan 2025'])
  })
})

describe('batchAddedMessage', () => {
  it('counts skip reasons', () => {
    expect(batchAddedMessage(2, [])).toBe('Added 2 downloads')
    expect(batchAddedMessage(1, [])).toBe('Added 1 download')
    expect(batchAddedMessage(2, [{ reason: 'duplicate' }])).toBe('Added 2 · 1 already queued')
    expect(batchAddedMessage(4, [{ reason: 'already_exists' }, { reason: 'already_exists' }, { reason: 'duplicate' }])).toBe('Added 4 · 2 already in library · 1 already queued')
  })
})
