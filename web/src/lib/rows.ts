// Editable header and subtitle rows used by Add and Replace source.
import type { Subtitle } from './types'

export interface HeaderRow {
  id: number
  name: string
  value: string
}

export interface SubtitleRow {
  id: number
  url: string
  lang: string
}

let next = 1
export const rowId = () => next++

export const newHeader = (): HeaderRow => ({ id: rowId(), name: '', value: '' })
export const newSubtitle = (url = '', lang = ''): SubtitleRow => ({ id: rowId(), url, lang })

/** An absolute http(s) URL without whitespace. */
export function validHttpUrl(value: string): boolean {
  const v = value.trim()
  if (!v || /\s/.test(v)) return false
  try {
    const u = new URL(v)
    return (u.protocol === 'http:' || u.protocol === 'https:') && !!u.hostname
  } catch {
    return false
  }
}

// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f]/

function validHeaderName(name: string): boolean {
  const n = name.trim()
  return n.length > 0 && !/[:\s]/.test(n) && !CONTROL.test(n)
}

/** Rows with both fields blank are ignored; the rest need a valid, unique name. */
export function headerRowsValid(rows: HeaderRow[]): boolean {
  const used = rows.filter((r) => r.name.trim() || r.value.trim())
  const names = used.map((r) => r.name.trim().toLowerCase())
  return used.every((r) => validHeaderName(r.name) && !CONTROL.test(r.value)) && new Set(names).size === names.length
}

export function headersRecord(rows: HeaderRow[]): Record<string, string> | undefined {
  const used = rows.filter((r) => r.name.trim())
  if (used.length === 0) return undefined
  return Object.fromEntries(used.map((r) => [r.name.trim(), r.value]))
}

/** Rows with a blank URL are ignored; the rest must be http(s). */
export function subtitleRowsValid(rows: SubtitleRow[]): boolean {
  return rows.every((r) => !r.url.trim() || validHttpUrl(r.url))
}

export function subtitlesList(rows: SubtitleRow[]): Subtitle[] {
  return rows.filter((r) => r.url.trim()).map((r) => ({ url: r.url.trim(), lang: r.lang.trim() }))
}
