import { describe, expect, it } from 'vitest'
import settings from '../../../contract/server-settings.json'
import update from '../../../contract/update-server-settings.json'
import { fromDocument, newCategory, newCategoryId, newIntegration, normalizeUrl, toDocument, uuid, validate } from './serverSettings'
import type { ServerSettings } from './types'

const doc = settings as ServerSettings

describe('server settings draft', () => {
  it('round-trips the contract document', () => {
    const d = fromDocument(doc)
    expect(d.minFreeGb).toBe('5')
    expect(validate(d)).toEqual({})
    expect(toDocument(d)).toEqual(doc)
  })

  it('produces the update fixture from edits', () => {
    const d = fromDocument(doc)
    d.concurrency = '3'
    d.retentionDays = '30'
    d.autoUpdate = false
    d.integrations[0]!.credential = ' example-replacement-key '
    expect(toDocument(d)).toEqual(update)
  })

  it('validates fields', () => {
    const d = fromDocument(doc)
    d.concurrency = '0'
    d.minFreeGb = '-1'
    d.retentionDays = 'x'
    d.mergeFormat = ' '
    d.categories.push({ ...newCategory(), label: 'Anime', path: 'relative' })
    d.integrations.push({ ...newIntegration(), url: '' })
    d.integrations[0]!.paths.push({ key: 'k', from: '/media', to: 'C:\\media' })
    expect(Object.keys(validate(d)).sort()).toEqual(
      ['concurrency', 'integration.0.path.0', 'integration.0.path.1', 'integration.1.credential', 'integration.1.url', 'mergeFormat', 'minFreeGb', 'category.1.path', 'retentionDays'].sort(),
    )
  })

  it('accepts Windows and UNC mapping targets', () => {
    const d = fromDocument(doc)
    d.integrations[0]!.paths = [
      { key: 'a', from: '/media', to: 'D:\\Media' },
      { key: 'b', from: '/tv', to: '\\\\nas\\tv' },
    ]
    expect(validate(d)).toEqual({})
  })

  it('normalizes URLs', () => {
    expect(normalizeUrl('jellyfin:8096/')).toBe('http://jellyfin:8096')
    expect(normalizeUrl('https://plex.example:32400')).toBe('https://plex.example:32400')
    expect(normalizeUrl('ftp://x')).toBeNull()
    expect(normalizeUrl('')).toBeNull()
  })

  it('generates ids', () => {
    expect(newCategoryId()).toMatch(/^[a-f0-9]{32}$/)
    expect(uuid()).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
  })
})
