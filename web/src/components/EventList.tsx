import { memo, useCallback, useEffect, useRef, useState } from 'react'
import { fmtDateShort, fmtFull, fmtRel, fmtTime, type Tz } from '../format'
import { eventKey, eventMillis, KIND_INSTANCE, KIND_NODE, STATUS_CLASS, STATUS_GLYPH, STATUS_NAMES, type Event } from '../types'

interface Props {
  events: Event[]
  tz: Tz
  clock: number
  selectedKey: string | null
  onSelect: (e: Event) => void
  hasMore: boolean
  loading: boolean
  error: string | null
  loadMore: () => void
  pending: number
  onJumpTop: () => void
  onScrolled: (scrolled: boolean) => void
  onWiden: () => void
  onClearFilters: () => void
  hasFilters: boolean
  showDc: boolean
}

export const ROW_HEIGHT = 46
const HEADER_HEIGHT = 30
const OVERSCAN = 8

export function Status({ code }: { code: number }) {
  return (
    <span className={`st ${STATUS_CLASS[code] ?? 'gone'}`} title={STATUS_NAMES[code] ?? 'unknown'}>
      {STATUS_GLYPH[code] ?? '?'}
    </span>
  )
}

export function Transition({ from, to }: { from: number; to: number }) {
  return (
    <span className="trans">
      <Status code={from} />→<Status code={to} />
    </span>
  )
}

function Healthy({ e }: { e: Event }) {
  if (e.kind === KIND_NODE) return null
  const zero = e.new_healthy === 0 && e.old_healthy > 0
  const cls = zero ? 'zero' : e.new_healthy < e.old_healthy ? 'down' : e.new_healthy > e.old_healthy ? 'up' : ''
  if (!cls && e.kind !== KIND_INSTANCE) return null
  return (
    <span className={`hb ${cls}`} title={`healthy instances before → after${e.total_instances ? ` (of ${e.total_instances})` : ''}`}>
      {e.old_healthy} → {e.new_healthy}
      {e.total_instances ? ` / ${e.total_instances}` : ''}
    </span>
  )
}

interface RowProps {
  e: Event
  prev?: Event
  top: number
  index: number
  tz: Tz
  clock: number
  selected: boolean
  showDc: boolean
  onSelect: (e: Event) => void
}

const Row = memo(function Row({ e, prev, top, index, tz, selected, showDc, onSelect }: RowProps) {
  const ms = eventMillis(e)
  const grouped = !!prev && e.kind !== KIND_NODE && Math.floor(eventMillis(prev) / 1000) === Math.floor(ms / 1000) && prev.node_name === e.node_name && prev.service_id === e.service_id
  const outage = e.old_healthy > 0 && e.new_healthy === 0 && e.kind !== KIND_NODE

  let headline: React.ReactNode
  let sub: string
  if (e.kind === KIND_NODE) {
    const ns = e.new_node_status ?? 0
    headline = (
      <>
        <Transition from={e.old_node_status ?? 0} to={ns} />
        {e.check_name ? <span title={e.check_name}>{e.check_name}</span> : <span className={`pill ${STATUS_CLASS[ns] ?? 'gone'}`}>node {ns === 1 ? 'deregistered' : ns === 0 ? 'registered' : STATUS_NAMES[ns]}</span>}
      </>
    )
    sub = e.check_name ? [e.check_type, 'node check'].filter(Boolean).join(' · ') : 'node'
  } else if (e.kind === KIND_INSTANCE) {
    const ns = e.new_service_status ?? 0
    headline = (
      <>
        <Transition from={e.old_service_status ?? 0} to={ns} />
        <span className={`pill ${ns === 1 ? 'gone' : 'ok'}`}>instance {ns === 1 ? 'deregistered' : 'registered'}</span>
      </>
    )
    sub = 'service instance'
  } else {
    headline = (
      <>
        <Transition from={e.old_check_status ?? 0} to={e.new_check_status ?? 0} />
        <span title={e.check_name}>{e.check_name}</span>
      </>
    )
    sub = [e.check_type, 'service check'].filter(Boolean).join(' · ')
  }

  return (
    <div
      id={`row-${index}`}
      className={`row${grouped ? ' grp' : ''}${outage ? ' outage' : ''}${selected ? ' sel' : ''}`}
      style={{ transform: `translateY(${top}px)` }}
      onClick={() => onSelect(e)}
    >
      <div className="c time">
        <div className="l1" title={`${fmtFull(ms, tz)} · ${fmtRel(ms)}`}>
          {grouped ? '〃' : fmtTime(ms, tz)}
        </div>
        <div className="l2">
          {fmtDateShort(ms, tz)}
          {showDc && (
            <>
              {' · '}
              <span className="dcb">{e.datacenter}</span>
            </>
          )}
          {' · '}
          {fmtRel(ms)}
        </div>
      </div>
      <div className="c node">
        <div className="l1" title={e.node_ip}>
          {e.node_name}
        </div>
        <div className="l2">{e.node_ip}</div>
      </div>
      <div className="c svc">
        <div className="l1" title={e.service_id}>
          {e.kind === KIND_NODE ? <span className="muted">— node —</span> : e.service_name}
          <Healthy e={e} />
        </div>
        <div className="l2">
          {e.kind !== KIND_NODE && (
            <>
              {e.team && <span>{e.team}</span>}
              {e.version && <span>v{e.version}</span>}
            </>
          )}
        </div>
      </div>
      <div className="c chk">
        <div className="l1">{headline}</div>
        <div className="l2">{sub}</div>
      </div>
      <div className="c out">
        <div className="l1" title={e.check_output}>
          {e.check_output || <span className="muted">—</span>}
        </div>
      </div>
    </div>
  )
})

export function EventList(p: Props) {
  const vp = useRef<HTMLDivElement>(null)
  const [scrollTop, setScrollTop] = useState(0)
  const [height, setHeight] = useState(600)
  const frame = useRef<number | undefined>(undefined)

  useEffect(() => {
    const el = vp.current
    if (!el) return
    const ro = new ResizeObserver(() => setHeight(el.clientHeight))
    ro.observe(el)
    setHeight(el.clientHeight)
    return () => ro.disconnect()
  }, [])

  const onScroll = useCallback(() => {
    if (frame.current) return
    frame.current = requestAnimationFrame(() => {
      frame.current = undefined
      const el = vp.current
      if (!el) return
      setScrollTop(el.scrollTop)
      p.onScrolled(el.scrollTop > 10)
      if (p.hasMore && !p.loading && el.scrollTop + el.clientHeight > el.scrollHeight - 8 * ROW_HEIGHT * 4) p.loadMore()
    })
  }, [p])

  const n = p.events.length
  const first = Math.max(0, Math.floor(scrollTop / ROW_HEIGHT) - OVERSCAN)
  const last = Math.min(n, Math.ceil((scrollTop + height) / ROW_HEIGHT) + OVERSCAN)
  const rows = []
  for (let i = first; i < last; i++) {
    const e = p.events[i]
    rows.push(<Row key={eventKey(e) + i} e={e} prev={p.events[i - 1]} index={i} top={HEADER_HEIGHT + i * ROW_HEIGHT} tz={p.tz} clock={p.clock} selected={p.selectedKey === eventKey(e)} showDc={p.showDc} onSelect={p.onSelect} />)
  }

  return (
    <section className="list">
      <div className="vp" id="vp" ref={vp} onScroll={onScroll}>
        <div className="lhead">
          <div>Time</div>
          <div>Node</div>
          <div>Service / instance</div>
          <div>Check · transition</div>
          <div>Output</div>
        </div>
        <div className="spacer" style={{ height: n * ROW_HEIGHT + (p.hasMore || p.loading ? ROW_HEIGHT : 0) }} />
        <div className="rows">{rows}</div>
        {(p.loading || p.hasMore) && n > 0 && (
          <div className="row message" style={{ transform: `translateY(${HEADER_HEIGHT + n * ROW_HEIGHT}px)` }}>
            {p.loading ? 'loading…' : 'scroll for more'}
          </div>
        )}
      </div>
      {p.pending > 0 && (
        <div className="newpill" onClick={p.onJumpTop}>
          ▲ {p.pending} new event{p.pending > 1 ? 's' : ''}
        </div>
      )}
      {p.error && <div className="empty err">{p.error}</div>}
      {!p.error && !p.loading && n === 0 && (
        <div className="empty">
          <div>
            <b>No events</b> in this window{p.hasFilters ? ' for these filters' : ''}.
          </div>
          <div className="actions">
            <button onClick={p.onWiden}>Widen window ×2</button>
            {p.hasFilters && <button onClick={p.onClearFilters}>Clear filters</button>}
          </div>
        </div>
      )}
    </section>
  )
}
