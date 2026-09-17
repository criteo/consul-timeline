import type { Filter, Range, Split } from './types'
import { FILTER_KEYS } from './types'

// The whole view lives in the URL so any state can be shared.
export interface AppState {
  dc: string
  range: Range
  filters: Filter[]
  tz: 'local' | 'utc'
  live: boolean
  split: Split // histogram breakdown, meaningful across datacenters
}

export const MIN = 60e3
export const HOUR = 3600e3
export const DAY = 24 * HOUR
export const PRESETS = [15 * MIN, HOUR, 3 * HOUR, 6 * HOUR, DAY, 3 * DAY, 7 * DAY, 14 * DAY]

export function rangeBounds(r: Range, now = Date.now()): [number, number] {
  switch (r.kind) {
    case 'preset':
      return [now - r.ms, now]
    case 'around':
      return [r.at - r.win, r.at + r.win]
    case 'abs':
      return [r.from, r.to]
  }
}

export const endsNow = (r: Range) => r.kind === 'preset'

export function shiftRange(r: Range, dir: 1 | -1, now = Date.now()): Range {
  const [from, to] = rangeBounds(r, now)
  const span = to - from
  const nf = from + dir * span
  const nt = to + dir * span
  if (nt >= now) return { kind: 'preset', ms: span }
  return { kind: 'abs', from: nf, to: nt }
}

export function zoomOut(r: Range, now = Date.now()): Range {
  const [from, to] = rangeBounds(r, now)
  const span = to - from
  if (r.kind === 'preset') return { kind: 'preset', ms: span * 2 }
  return { kind: 'abs', from: from - span / 2, to: Math.min(now, to + span / 2) }
}

export function parseFilter(raw: string): Filter | null {
  raw = raw.trim()
  if (!raw) return null
  let not = false
  if (raw.startsWith('-')) {
    not = true
    raw = raw.slice(1)
  }
  const i = raw.indexOf(':')
  if (i > 0) {
    const field = raw.slice(0, i).toLowerCase()
    const value = raw.slice(i + 1).trim()
    if ((FILTER_KEYS as readonly string[]).includes(field) && value) return { field, value, not }
  }
  return { field: 'text', value: raw, not: false }
}

export const filterString = (f: Filter) => (f.not ? '-' : '') + (f.field === 'text' ? f.value : `${f.field}:${f.value}`)

export function sameFilter(a: Filter, b: Filter): boolean {
  return a.field === b.field && a.value === b.value && a.not === b.not
}

function serializeRange(r: Range): string {
  switch (r.kind) {
    case 'preset':
      return `p:${r.ms}`
    case 'around':
      return `m:${r.at},${r.win}`
    case 'abs':
      return `a:${Math.round(r.from)},${Math.round(r.to)}`
  }
}

function parseRange(s: string | null): Range {
  if (!s) return { kind: 'preset', ms: HOUR }
  const [k, rest = ''] = s.split(':')
  const nums = rest.split(',').map(Number)
  switch (k) {
    case 'p':
      return { kind: 'preset', ms: nums[0] > 0 ? nums[0] : HOUR }
    case 'm':
      if (nums.length === 2 && nums.every(Number.isFinite)) return { kind: 'around', at: nums[0], win: nums[1] }
      break
    case 'a':
      if (nums.length === 2 && nums.every(Number.isFinite) && nums[1] > nums[0]) return { kind: 'abs', from: nums[0], to: nums[1] }
      break
  }
  return { kind: 'preset', ms: HOUR }
}

export function parseState(search: string, localDc: string): AppState {
  const p = new URLSearchParams(search)
  const filters: Filter[] = []
  for (const raw of p.getAll('f')) {
    const f = parseFilter(raw)
    if (f) filters.push(f)
  }
  const q = p.get('q')
  if (q) filters.push({ field: 'text', value: q, not: false })
  return {
    dc: p.get('dc') || localDc,
    range: parseRange(p.get('r')),
    filters,
    tz: p.get('tz') === 'utc' ? 'utc' : 'local',
    live: p.get('live') !== '0',
    split: p.get('s') === 'dc' ? 'dc' : 'status',
  }
}

export function stateToSearch(s: AppState): string {
  const p = new URLSearchParams()
  p.set('dc', s.dc)
  p.set('r', serializeRange(s.range))
  for (const f of s.filters) {
    if (f.field === 'text') p.set('q', f.value)
    else p.append('f', filterString(f))
  }
  if (s.tz === 'utc') p.set('tz', 'utc')
  if (!s.live) p.set('live', '0')
  if (s.split === 'dc') p.set('s', 'dc')
  return '?' + p.toString()
}
