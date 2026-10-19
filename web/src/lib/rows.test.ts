import { describe, expect, it } from 'vitest'
import retrySource from '../../../contract/retry-source.json'
import { headerRowsValid, headersRecord, subtitleRowsValid, subtitlesList, validHttpUrl } from './rows'
import type { RetryBody } from './types'

const h = (name: string, value = 'v') => ({ id: 0, name, value })
const s = (url: string, lang = '') => ({ id: 0, url, lang })

describe('rows', () => {
  it('validates URLs', () => {
    expect(validHttpUrl('https://cdn.example/a.m3u8')).toBe(true)
    expect(validHttpUrl(' http://x.example ')).toBe(true)
    expect(validHttpUrl('ftp://x.example')).toBe(false)
    expect(validHttpUrl('https://a b')).toBe(false)
    expect(validHttpUrl('')).toBe(false)
  })

  it('validates headers', () => {
    expect(headerRowsValid([h('Referer'), h('', '')])).toBe(true)
    expect(headerRowsValid([h('Bad Name')])).toBe(false)
    expect(headerRowsValid([h('X:Y')])).toBe(false)
    expect(headerRowsValid([h('Cookie'), h('cookie')])).toBe(false)
    expect(headerRowsValid([h('A', 'line\nbreak')])).toBe(false)
    expect(headerRowsValid([h('', 'value only')])).toBe(false)
    expect(headersRecord([h(' Referer ', 'https://e.example/'), h('', '')])).toEqual({ Referer: 'https://e.example/' })
    expect(headersRecord([])).toBeUndefined()
  })

  it('builds subtitle lists', () => {
    expect(subtitleRowsValid([s(''), s('https://x.example/a.vtt')])).toBe(true)
    expect(subtitleRowsValid([s('nope')])).toBe(false)
    expect(subtitlesList([s(''), s(' https://x.example/a.vtt ', ' en ')])).toEqual([{ url: 'https://x.example/a.vtt', lang: 'en' }])
  })

  it('matches the retry-source contract shape', () => {
    const body: RetryBody = { overwrite: false, source: { url: 'https://media.example.com/refreshed/video.m3u8', headers_mode: 'clear', subtitles: subtitlesList([]) } }
    expect(body).toEqual(retrySource)
  })
})
