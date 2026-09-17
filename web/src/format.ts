// Time formatting in the chosen zone. Everything takes milliseconds.

export type Tz = 'local' | 'utc'

const MON = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const DOW = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

const pad = (n: number, w = 2) => String(n).padStart(w, '0')

interface Parts {
  y: number
  mo: number
  d: number
  h: number
  mi: number
  s: number
  ms: number
  wd: number
}

function parts(ms: number, tz: Tz): Parts {
  const d = new Date(ms)
  if (tz === 'utc') {
    return { y: d.getUTCFullYear(), mo: d.getUTCMonth(), d: d.getUTCDate(), h: d.getUTCHours(), mi: d.getUTCMinutes(), s: d.getUTCSeconds(), ms: d.getUTCMilliseconds(), wd: d.getUTCDay() }
  }
  return { y: d.getFullYear(), mo: d.getMonth(), d: d.getDate(), h: d.getHours(), mi: d.getMinutes(), s: d.getSeconds(), ms: d.getMilliseconds(), wd: d.getDay() }
}

export function tzLabel(tz: Tz): string {
  if (tz === 'utc') return 'UTC'
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'local'
  } catch {
    return 'local'
  }
}

export const fmtTime = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${pad(p.h)}:${pad(p.mi)}:${pad(p.s)}.${pad(p.ms, 3)}`
}
export const fmtHM = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${pad(p.h)}:${pad(p.mi)}`
}
export const fmtHMS = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${pad(p.h)}:${pad(p.mi)}:${pad(p.s)}`
}
export const fmtDate = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${DOW[p.wd]} ${p.d} ${MON[p.mo]}`
}
export const fmtDateShort = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${p.d} ${MON[p.mo]}`
}
export const fmtFull = (ms: number, tz: Tz) => {
  const p = parts(ms, tz)
  return `${p.y}-${pad(p.mo + 1)}-${pad(p.d)} ${pad(p.h)}:${pad(p.mi)}:${pad(p.s)}.${pad(p.ms, 3)} ${tzLabel(tz)}`
}

export function fmtRel(ms: number, now = Date.now()): string {
  const d = now - ms
  if (d < 1000) return 'now'
  if (d < 60e3) return `${Math.round(d / 1000)}s ago`
  if (d < 3600e3) return `${Math.round(d / 60e3)}m ago`
  if (d < 86400e3) return `${(d / 3600e3).toFixed(1)}h ago`
  return `${Math.round(d / 86400e3)}d ago`
}

// fmtDur renders exact durations such as presets: 15m, 1h, 3d.
export function fmtDur(ms: number): string {
  if (ms % 86400e3 === 0) return `${ms / 86400e3}d`
  if (ms % 3600e3 === 0) return `${ms / 3600e3}h`
  if (ms % 60e3 === 0) return `${ms / 60e3}m`
  return `${ms / 1000}s`
}

// datetime-local inputs work in the displayed zone
export function toInput(ms: number, tz: Tz): string {
  const p = parts(ms, tz)
  return `${p.y}-${pad(p.mo + 1)}-${pad(p.d)}T${pad(p.h)}:${pad(p.mi)}:${pad(p.s)}`
}

export function fromInput(v: string, tz: Tz): number {
  const m = v.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?/)
  if (!m) return NaN
  const [y, mo, d, h, mi, s] = m.slice(1).map((x) => Number(x ?? 0))
  return tz === 'utc' ? Date.UTC(y, mo - 1, d, h, mi, s || 0) : new Date(y, mo - 1, d, h, mi, s || 0).getTime()
}

// parseMoment accepts ISO 8601, unix seconds or milliseconds, and
// "yesterday 02:00" in the displayed zone.
export function parseMoment(s: string, tz: Tz, now = Date.now()): number {
  s = s.trim()
  if (!s) return NaN
  if (/^\d{13}$/.test(s)) return Number(s)
  if (/^\d{10}$/.test(s)) return Number(s) * 1000
  const y = s.match(/^yesterday\s+(\d{1,2}):(\d{2})$/i)
  if (y) {
    const p = parts(now - 86400e3, tz)
    return tz === 'utc' ? Date.UTC(p.y, p.mo, p.d, Number(y[1]), Number(y[2])) : new Date(p.y, p.mo, p.d, Number(y[1]), Number(y[2])).getTime()
  }
  const t = Date.parse(s)
  if (!Number.isNaN(t)) return t
  return fromInput(s.replace(' ', 'T'), tz)
}
