import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { getFacets, getHistogram, streamUrl, type Selection } from './api'
import { Drawer } from './components/Drawer'
import { EventList } from './components/EventList'
import { Facets } from './components/Facets'
import { Help } from './components/Help'
import { Histogram } from './components/Histogram'
import { RangePopover } from './components/RangePopover'
import { TopBar } from './components/TopBar'
import { useEvents, useFetch, useLive, useMeta, useTick } from './hooks'
import { endsNow, filterString, parseState, rangeBounds, sameFilter, shiftRange, stateToSearch, zoomOut, type AppState } from './state'
import { eventKey, type Event, type Filter, type Range, type Split } from './types'

const FACET_FIELDS = ['dc', 'kind', 'to', 'tag', 'team', 'service', 'node', 'check']
const HISTOGRAM_BUCKETS = 72

type Theme = 'light' | 'dark'

function initialTheme(): Theme {
  try {
    const saved = localStorage.getItem('ct-theme')
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // storage unavailable
  }
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export default function App() {
  const { meta, error: metaError } = useMeta()
  const [state, setState] = useState<AppState>(() => parseState(location.search, ''))
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const [facetsOpen, setFacetsOpen] = useState(true)
  const [selected, setSelected] = useState<Event | null>(null)
  const [popover, setPopover] = useState<'range' | 'help' | null>(null)
  const [toast, setToast] = useState<string | null>(null)
  const [pending, setPending] = useState(0)
  const scrolled = useRef(false)
  const filterInput = useRef<HTMLInputElement>(null)
  const refreshTick = useTick(15000)
  const clockTick = useTick(5000)

  // adopt the instance's datacenter when the URL did not name one
  useEffect(() => {
    if (meta && !state.dc) setState((s) => ({ ...s, dc: meta.local_dc }))
  }, [meta, state.dc])

  useEffect(() => {
    if (!state.dc) return
    const search = stateToSearch(state)
    if (search !== location.search) history.replaceState(null, '', search)
  }, [state])

  useEffect(() => {
    const onPop = () => setState(parseState(location.search, meta?.local_dc ?? ''))
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [meta])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    try {
      localStorage.setItem('ct-theme', theme)
    } catch {
      // storage unavailable
    }
  }, [theme])

  const showToast = useCallback((msg: string) => {
    setToast(msg)
    window.setTimeout(() => setToast((t) => (t === msg ? null : t)), 4000)
  }, [])

  const isLive = state.live && endsNow(state.range) && !!meta && state.dc === meta.local_dc
  const allDcs = state.dc === 'all'
  const split: Split = allDcs ? state.split : 'status' // one datacenter has nothing to split by
  const queryKey = `${state.dc}|${JSON.stringify(state.range)}|${state.filters.map(filterString).join('&')}`
  const now = Date.now()
  const [from, to] = rangeBounds(state.range, now)
  const selection: Selection = {
    dc: state.dc === 'all' ? '' : state.dc,
    from,
    to: endsNow(state.range) ? undefined : to,
    filters: state.filters,
  }

  const events = useEvents(selection, queryKey)
  const refresh = isLive ? refreshTick : 0
  const histogram = useFetch((signal) => getHistogram(selection, HISTOGRAM_BUCKETS, split, signal), `${queryKey}|${split}`, refresh)
  const facets = useFetch((signal) => getFacets(selection, FACET_FIELDS, 8, signal), queryKey, refresh)

  const onLiveEvent = useCallback(
    (e: Event) => {
      if (events.prepend(e) && scrolled.current) setPending((p) => p + 1)
    },
    [events.prepend],
  )
  const liveUrl = useMemo(() => (isLive && state.dc ? streamUrl(selection) : null), [isLive, state.dc, queryKey]) // eslint-disable-line react-hooks/exhaustive-deps
  const live = useLive(liveUrl, onLiveEvent, showToast)

  // ---- actions --------------------------------------------------------------

  const update = (patch: Partial<AppState>) => setState((s) => ({ ...s, ...patch }))
  // a dc filter only means something over every datacenter: it widens the scope
  const scoped = (s: AppState, filters: Filter[]): AppState => ({ ...s, filters, dc: s.dc !== 'all' && filters.some((f) => f.field === 'dc') ? 'all' : s.dc })
  const setFilters = (filters: Filter[]) => setState((s) => scoped(s, filters))
  const addFilter = (f: Filter) => setState((s) => (s.filters.some((x) => sameFilter(x, f)) ? s : scoped(s, [...s.filters, f])))
  const toggleFilter = (field: string, value: string, not: boolean) =>
    setState((s) => {
      const i = s.filters.findIndex((f) => f.field === field && f.value === value)
      if (i < 0) return scoped(s, [...s.filters, { field, value, not }])
      const filters = [...s.filters]
      if (filters[i].not === not) filters.splice(i, 1)
      else filters[i] = { ...filters[i], not }
      return scoped(s, filters)
    })
  const setRange = (range: Range) => {
    update({ range })
    setPopover(null)
  }
  const around = (at: number, win = 5 * 60e3) => setRange({ kind: 'around', at, win })
  const toggleLive = () => {
    if (!endsNow(state.range)) update({ range: { kind: 'preset', ms: 3600e3 }, live: true })
    else update({ live: !state.live })
  }
  const share = () => {
    navigator.clipboard?.writeText(location.href).then(
      () => showToast('Link copied'),
      () => showToast(location.href),
    )
  }
  const jumpTop = () => {
    setPending(0)
    document.getElementById('vp')?.scrollTo({ top: 0 })
  }
  const onScrolled = (isScrolled: boolean) => {
    scrolled.current = isScrolled
    if (!isScrolled) setPending(0)
  }

  // ---- keyboard ---------------------------------------------------------------

  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const typing = /INPUT|SELECT|TEXTAREA/.test((document.activeElement as HTMLElement | null)?.tagName ?? '')
      if (ev.key === 'Escape') {
        setPopover(null)
        if (!typing) setSelected(null)
        return
      }
      if (typing) return
      switch (ev.key) {
        case '/':
          ev.preventDefault()
          filterInput.current?.focus()
          break
        case '?':
          setPopover((p) => (p === 'help' ? null : 'help'))
          break
        case '[':
          update({ range: shiftRange(state.range, -1) })
          break
        case ']':
          update({ range: shiftRange(state.range, 1) })
          break
        case 'l':
          toggleLive()
          break
        case 't':
          update({ tz: state.tz === 'utc' ? 'local' : 'utc' })
          break
        case 'j':
        case 'k': {
          const list = events.events
          if (!list.length) break
          const i = selected ? list.findIndex((e) => eventKey(e) === eventKey(selected)) : -1
          const n = Math.max(0, Math.min(list.length - 1, i < 0 ? 0 : i + (ev.key === 'j' ? 1 : -1)))
          setSelected(list[n])
          document.getElementById(`row-${n}`)?.scrollIntoView({ block: 'nearest' })
          break
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  // ---- render -----------------------------------------------------------------

  const legacyCount = useMemo(() => events.events.filter((e) => e.legacy).length, [events.events])
  const outageCount = useMemo(() => events.events.filter((e) => e.old_healthy > 0 && e.new_healthy === 0).length, [events.events])

  return (
    <>
      <TopBar
        state={state}
        meta={meta}
        live={live}
        isLive={isLive}
        facetsOpen={facetsOpen}
        rangeOpen={popover === 'range'}
        filterInput={filterInput}
        onToggleFacets={() => setFacetsOpen((v) => !v)}
        onDc={(dc) => update({ dc })}
        onFilters={setFilters}
        onRangeClick={() => setPopover((p) => (p === 'range' ? null : 'range'))}
        onShift={(dir) => update({ range: shiftRange(state.range, dir) })}
        onZoomOut={() => update({ range: zoomOut(state.range) })}
        onToggleLive={toggleLive}
        onToggleTz={() => update({ tz: state.tz === 'utc' ? 'local' : 'utc' })}
        onToggleTheme={() => setTheme((t) => (t === 'dark' ? 'light' : 'dark'))}
        onShare={share}
        onHelp={() => setPopover((p) => (p === 'help' ? null : 'help'))}
      />
      <Histogram
        data={histogram.data}
        from={from}
        to={to}
        tz={state.tz}
        loading={histogram.loading}
        split={split}
        canSplit={allDcs}
        datacenters={meta?.datacenters ?? []}
        onBrush={(a, b) => setRange({ kind: 'abs', from: a, to: b })}
        onSplit={(s) => update({ split: s })}
      />
      <main className="layout">
        {facetsOpen && (
          <Facets
            data={facets.data}
            error={facets.error}
            filters={state.filters}
            legacyCount={legacyCount}
            outageCount={outageCount}
            allDatacenters={allDcs}
            onToggle={toggleFilter}
          />
        )}
        <EventList
          events={events.events}
          tz={state.tz}
          clock={clockTick}
          selectedKey={selected ? eventKey(selected) : null}
          onSelect={setSelected}
          hasMore={events.hasMore}
          loading={events.loading}
          error={events.error}
          loadMore={events.loadMore}
          pending={pending}
          onJumpTop={jumpTop}
          onScrolled={onScrolled}
          onWiden={() => update({ range: zoomOut(state.range) })}
          onClearFilters={() => setFilters([])}
          hasFilters={state.filters.length > 0}
          showDc={allDcs}
        />
        {selected && (
          <Drawer
            event={selected}
            tz={state.tz}
            onClose={() => setSelected(null)}
            onAddFilter={(field, value) => addFilter({ field, value, not: false })}
            onAround={around}
            onNotice={showToast}
          />
        )}
      </main>
      {popover === 'range' && <RangePopover range={state.range} tz={state.tz} retentionDays={meta?.retention_days ?? 14} onRange={setRange} onTz={(tz) => update({ tz })} onClose={() => setPopover(null)} />}
      {popover === 'help' && <Help onClose={() => setPopover(null)} />}
      {(toast || metaError) && <div className="toast">{toast ?? `Cannot reach the API: ${metaError}`}</div>}
    </>
  )
}
