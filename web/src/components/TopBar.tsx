import type { RefObject } from 'react'
import { fmtDateShort, fmtDur, fmtHM, fmtHMS } from '../format'
import type { LiveStatus } from '../hooks'
import { endsNow, rangeBounds, type AppState } from '../state'
import type { Filter, Meta } from '../types'
import { FilterBar } from './FilterBar'

interface Props {
  state: AppState
  meta: Meta | null
  live: { status: LiveStatus; rate: number }
  isLive: boolean
  facetsOpen: boolean
  rangeOpen: boolean
  filterInput: RefObject<HTMLInputElement | null>
  onToggleFacets: () => void
  onDc: (dc: string) => void
  onFilters: (filters: Filter[]) => void
  onRangeClick: () => void
  onShift: (dir: 1 | -1) => void
  onZoomOut: () => void
  onToggleLive: () => void
  onToggleTz: () => void
  onToggleTheme: () => void
  onShare: () => void
  onHelp: () => void
}

export function TopBar(p: Props) {
  const { state, meta } = p
  const [from, to] = rangeBounds(state.range)
  const r = state.range

  let rangeLabel: string
  if (r.kind === 'preset') rangeLabel = `Last ${fmtDur(r.ms)}`
  else if (r.kind === 'around') rangeLabel = `${fmtDateShort(r.at, state.tz)} ${fmtHMS(r.at, state.tz)} ± ${fmtDur(r.win)}`
  else {
    const sameDay = fmtDateShort(from, state.tz) === fmtDateShort(to, state.tz)
    rangeLabel = `${fmtDateShort(from, state.tz)} ${fmtHM(from, state.tz)} → ${sameDay ? '' : fmtDateShort(to, state.tz) + ' '}${fmtHM(to, state.tz)}`
  }

  let liveClass = 'live'
  let liveText: string
  if (p.isLive) {
    liveClass += p.live.status === 'live' ? ' on' : ' paused'
    liveText = p.live.status === 'live' ? `Live · ${p.live.rate.toFixed(1)} ev/s` : p.live.status === 'retrying' ? 'Reconnecting…' : 'Connecting…'
  } else if (!endsNow(r)) liveText = 'Historical'
  else if (state.dc === 'all') liveText = 'History across DCs · live tail is per DC'
  else if (meta && state.dc !== meta.local_dc) liveText = `Live only on the ${state.dc} instance`
  else {
    liveClass += ' paused'
    liveText = 'Paused'
  }

  const dcs = meta ? [...meta.datacenters] : state.dc ? [state.dc] : []
  if (meta && !dcs.includes(meta.local_dc)) dcs.unshift(meta.local_dc)

  return (
    <header className="top">
      <div className="brand">
        <span className="logo" />
        Consul Timeline
      </div>
      <button className="ghost" title="Toggle the facet panel" onClick={p.onToggleFacets}>
        ☰
      </button>
      <select className="dc" value={state.dc} title="Datacenter. History is shared across datacenters; the live tail is per instance." onChange={(e) => p.onDc(e.target.value)}>
        {dcs.map((dc) => (
          <option key={dc} value={dc}>
            {dc}
            {meta && dc === meta.local_dc ? ' · local' : ''}
          </option>
        ))}
        <option value="all">all DCs (history)</option>
      </select>
      <FilterBar filters={state.filters} dc={state.dc === 'all' ? '' : state.dc} onChange={p.onFilters} inputRef={p.filterInput} />
      <div className={liveClass} title="Toggle the live tail (l)" onClick={p.onToggleLive}>
        <span className="dot" />
        <span>{liveText}</span>
      </div>
      <div className="rangectl">
        <button className="ghost" title="Shift the window back  [" onClick={() => p.onShift(-1)}>
          ◀
        </button>
        <button className={'rbtn' + (p.rangeOpen ? ' on' : '')} onClick={p.onRangeClick}>
          <span>{rangeLabel}</span>
          <small>▾</small>
        </button>
        <button className="ghost" title="Shift the window forward  ]" onClick={() => p.onShift(1)}>
          ▶
        </button>
        <button className="ghost" title="Zoom out ×2" onClick={p.onZoomOut}>
          ⤢
        </button>
      </div>
      <button className="ghost" title="Toggle local time and UTC  t" onClick={p.onToggleTz}>
        {state.tz === 'utc' ? 'UTC' : 'Local'}
      </button>
      <button className="ghost" title="Toggle dark mode" onClick={p.onToggleTheme}>
        ◐
      </button>
      <button className="ghost" title="Copy a link to this view" onClick={p.onShare}>
        ⤴
      </button>
      <button className="ghost" title="Keyboard shortcuts  ?" onClick={p.onHelp}>
        ?
      </button>
    </header>
  )
}
