import { useEffect, useRef, useState } from 'react'
import type { HistogramResponse } from '../api'
import { fmtDateShort, fmtHM, fmtHMS, type Tz } from '../format'

interface Props {
  data: HistogramResponse | null
  from: number
  to: number
  tz: Tz
  loading: boolean
  onBrush: (from: number, to: number) => void
}

const HEIGHT = 52
const ORDER = ['missing', 'unknown', 'maintenance', 'passing', 'warning', 'critical'] as const
const CLASS: Record<string, string> = { missing: 'gone', unknown: 'gone', maintenance: 'maint', passing: 'ok', warning: 'warn', critical: 'crit' }

export function Histogram({ data, from, to, tz, loading, onBrush }: Props) {
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
            return ORDER.map((st) => {
              const c = b.by_status[st] ?? 0
              if (!c) return null
              const h = (c / max) * (HEIGHT - 4)
              y -= h
              return <rect key={`${i}-${st}`} className={`bar ${CLASS[st]}`} x={xOf(t) + 1} y={y} width={widthPx} height={h} rx={1} />
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
            {ORDER.filter((s) => hovered.by_status[s])
              .map((s) => `${s} ${hovered.by_status[s]}`)
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
      </div>
    </section>
  )
}
