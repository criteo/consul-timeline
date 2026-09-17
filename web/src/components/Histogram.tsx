import { useEffect, useRef, useState } from 'react'
import type { HistogramResponse } from '../api'
import { fmtDateShort, fmtHM, fmtHMS, type Tz } from '../format'
import type { Bucket, Split } from '../types'

interface Props {
  data: HistogramResponse | null
  from: number
  to: number
  tz: Tz
  loading: boolean
  split: Split
  canSplit: boolean // more than one datacenter in scope
  datacenters: string[] // known datacenters, for stable colours
  onBrush: (from: number, to: number) => void
  onSplit: (s: Split) => void
}

const HEIGHT = 52
const ORDER = ['missing', 'unknown', 'maintenance', 'passing', 'warning', 'critical']
const CLASS: Record<string, string> = { missing: 'gone', unknown: 'gone', maintenance: 'maint', passing: 'ok', warning: 'warn', critical: 'crit' }
const DC_COLOURS = 8

// dcKeys lists the datacenters to draw: the known ones first, so a
// datacenter keeps its colour across queries, then any other seen in the data
function dcKeys(known: string[], buckets: Bucket[]): string[] {
  const keys = [...known]
  for (const b of buckets) for (const k of Object.keys(b.by)) if (!keys.includes(k)) keys.push(k)
  return keys
}

export function Histogram({ data, from, to, tz, loading, split, canSplit, datacenters, onBrush, onSplit }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(800)
  const [hover, setHover] = useState<number | null>(null)
  const [brush, setBrush] = useState<[number, number] | null>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(() => setWidth(el.clientWidth))
    ro.observe(el)
    setWidth(el.clientWidth)
    return () => ro.disconnect()
  }, [])

  const buckets = data?.buckets ?? []
  const n = buckets.length
  const span = to - from
  const max = Math.max(1, ...buckets.map((b) => b.total))
  const xOf = (t: number) => ((t - from) / span) * width
  const widthPx = data && n ? Math.max(1, (data.bucket_seconds * 1000 * width) / span - 2) : 0
  const byDc = (data?.split ?? split) === 'dc'
  const keys = byDc ? dcKeys(datacenters, buckets) : ORDER
  const cls = (k: string) => (byDc ? `dc${Math.max(0, keys.indexOf(k)) % DC_COLOURS}` : (CLASS[k] ?? 'gone'))
  const present = keys.filter((k) => buckets.some((b) => b.by[k]))

  const posToTime = (clientX: number) => {
    const rect = ref.current!.getBoundingClientRect()
    const x = Math.max(0, Math.min(rect.width, clientX - rect.left))
    return from + (x / rect.width) * span
  }

  const hovered = hover !== null && n ? buckets[Math.min(n - 1, Math.max(0, Math.floor(((hover - from) / span) * n)))] : undefined

  return (
    <section className="histo">
      <div
        ref={ref}
        className="hcanvas"
        onMouseMove={(e) => {
          const t = posToTime(e.clientX)
          setHover(t)
          if (brush) setBrush([brush[0], t])
        }}
        onMouseLeave={() => setHover(null)}
        onMouseDown={(e) => setBrush([posToTime(e.clientX), posToTime(e.clientX)])}
        onMouseUp={(e) => {
          if (!brush) return
          const a = Math.min(brush[0], posToTime(e.clientX))
          const b = Math.max(brush[0], posToTime(e.clientX))
          setBrush(null)
          if (b - a > span * 0.01) onBrush(Math.round(a), Math.round(b))
        }}
      >
        <svg width={width} height={HEIGHT}>
          {buckets.map((b, i) => {
            const t = Date.parse(b.t)
            let y = HEIGHT
            return keys.map((k) => {
              const c = b.by[k] ?? 0
              if (!c) return null
              const h = (c / max) * (HEIGHT - 4)
              y -= h
              return <rect key={`${i}-${k}`} className={`bar ${cls(k)}`} x={xOf(t) + 1} y={y} width={widthPx} height={h} rx={1} />
            })
          })}
          {brush && <rect className="brush" x={xOf(Math.min(...brush))} y={0} width={Math.abs(xOf(brush[1]) - xOf(brush[0]))} height={HEIGHT} />}
        </svg>
        {hovered && hover !== null && (
          <div className="tip" style={{ left: Math.min(xOf(hover) + 14, width - 220) }}>
            <b>
              {fmtHMS(Date.parse(hovered.t), tz)} – {fmtHMS(Date.parse(hovered.t) + (data?.bucket_seconds ?? 0) * 1000, tz)}
            </b>{' '}
            · {hovered.total} events
            <br />
            {keys
              .filter((k) => hovered.by[k])
              .map((k) => `${k} ${hovered.by[k]}`)
              .join(' · ')}
          </div>
        )}
      </div>
      <div className="haxis">
        {Array.from({ length: 7 }, (_, i) => {
          const t = from + (span * i) / 6
          return <span key={i}>{span > 26 * 3600e3 ? `${fmtDateShort(t, tz)} ${fmtHM(t, tz)}` : fmtHM(t, tz)}</span>
        })}
      </div>
      <div className="legend">
        {data?.sampled && <span className="muted">sampled</span>}
        {loading && <span className="muted">loading…</span>}
        {canSplit && (
          <span className="split" title="What the bars are coloured by">
            <button className={split === 'status' ? 'on' : ''} onClick={() => onSplit('status')}>
              status
            </button>
            <button className={split === 'dc' ? 'on' : ''} onClick={() => onSplit('dc')}>
              datacenter
            </button>
          </span>
        )}
        {byDc ? (
          present.map((k) => (
            <span key={k}>
              <i className={cls(k)} /> {k}
            </span>
          ))
        ) : (
          <>
            <span>
              <i className="crit" /> critical
            </span>
            <span>
              <i className="warn" /> warning
            </span>
            <span>
              <i className="ok" /> passing
            </span>
            <span>
              <i className="gone" /> missing
            </span>
            <span>
              <i className="maint" /> maintenance
            </span>
          </>
        )}
      </div>
    </section>
  )
}
