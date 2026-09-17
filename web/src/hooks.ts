import { useCallback, useEffect, useRef, useState } from 'react'
import { getEvents, getMeta, type Selection } from './api'
import { eventKey, eventMillis, type Event, type Meta } from './types'

export function useMeta() {
  const [meta, setMeta] = useState<Meta | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    const ctl = new AbortController()
    getMeta(ctl.signal)
      .then(setMeta)
      .catch((e: Error) => {
        if (!ctl.signal.aborted) setError(e.message)
      })
    return () => ctl.abort()
  }, [])
  return { meta, error }
}

// useTick re-renders every ms milliseconds so relative labels stay fresh.
export function useTick(ms: number): number {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), ms)
    return () => window.clearInterval(id)
  }, [ms])
  return tick
}

// useFetch runs load whenever key or refresh changes, keeping the last
// good result while a new one is loading.
export function useFetch<T>(load: (signal: AbortSignal) => Promise<T>, key: string, refresh: number) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const loadRef = useRef(load)
  loadRef.current = load
  useEffect(() => {
    const ctl = new AbortController()
    setLoading(true)
    loadRef
      .current(ctl.signal)
      .then((d) => {
        if (ctl.signal.aborted) return
        setData(d)
        setError(null)
        setLoading(false)
      })
      .catch((e: Error) => {
        if (ctl.signal.aborted) return
        setError(e.message)
        setLoading(false)
      })
    return () => ctl.abort()
  }, [key, refresh])
  return { data, error, loading }
}

const PAGE = 200
const MAX_EVENTS = 5000
const MAX_SEEN = 20000

export interface EventsState {
  events: Event[]
  loading: boolean
  error: string | null
  hasMore: boolean
}

// useEvents pages through the events of a selection, newest first, and
// accepts live events on top. key identifies the selection; the selection
// object itself may change on every render (its bounds move with time).
export function useEvents(selection: Selection, key: string) {
  const [state, setState] = useState<EventsState>({ events: [], loading: false, error: null, hasMore: false })
  const selRef = useRef(selection)
  selRef.current = selection
  const cursor = useRef<string | undefined>(undefined)
  const seen = useRef(new Set<string>())
  const ctl = useRef<AbortController | null>(null)
  const busy = useRef(false)

  const fetchPage = useCallback(async (reset: boolean) => {
    if (busy.current && !reset) return
    ctl.current?.abort()
    const c = new AbortController()
    ctl.current = c
    busy.current = true
    if (reset) {
      cursor.current = undefined
      seen.current = new Set()
    }
    setState((s) => (reset ? { events: [], loading: true, error: null, hasMore: false } : { ...s, loading: true, error: null }))
    try {
      const page = await getEvents(selRef.current, cursor.current, PAGE, c.signal)
      if (c.signal.aborted) return
      cursor.current = page.next_cursor
      const fresh = page.events.filter((e) => {
        const k = eventKey(e)
        if (seen.current.has(k)) return false
        seen.current.add(k)
        return true
      })
      setState((s) => ({ events: reset ? fresh : [...s.events, ...fresh], loading: false, error: null, hasMore: page.has_more }))
    } catch (err) {
      if (c.signal.aborted) return
      setState((s) => ({ ...s, loading: false, error: (err as Error).message }))
    } finally {
      if (ctl.current === c) busy.current = false
    }
  }, [])

  useEffect(() => {
    void fetchPage(true)
    return () => ctl.current?.abort()
  }, [key, fetchPage])

  const loadMore = useCallback(() => void fetchPage(false), [fetchPage])
  const reload = useCallback(() => void fetchPage(true), [fetchPage])

  // prepend adds a live event; it reports whether the event was new
  const prepend = useCallback((e: Event): boolean => {
    const k = eventKey(e)
    if (seen.current.has(k)) return false
    if (seen.current.size > MAX_SEEN) seen.current = new Set()
    seen.current.add(k)
    setState((s) => {
      const events = [e, ...s.events]
      if (events.length > MAX_EVENTS) events.length = MAX_EVENTS
      return { ...s, events }
    })
    return true
  }, [])

  return { ...state, loadMore, reload, prepend }
}

export type LiveStatus = 'off' | 'connecting' | 'live' | 'retrying'

// useLive keeps a Server-Sent Events connection open while url is set.
// The browser reconnects by itself and resends the last event id, which
// the server replays; a "reset" frame (client too slow) reconnects with an
// explicit since= so nothing is lost either.
export function useLive(url: string | null, onEvent: (e: Event) => void, onNotice: (msg: string) => void) {
  const [status, setStatus] = useState<LiveStatus>('off')
  const [rate, setRate] = useState(0)
  const onEventRef = useRef(onEvent)
  onEventRef.current = onEvent
  const onNoticeRef = useRef(onNotice)
  onNoticeRef.current = onNotice

  useEffect(() => {
    if (!url) {
      setStatus('off')
      setRate(0)
      return
    }
    let es: EventSource | null = null
    let closed = false
    let retry: number | undefined
    let last = 0
    const times: number[] = []

    const open = (since?: number) => {
      setStatus('connecting')
      es = new EventSource(since ? `${url}&since=${since}` : url)
      es.onopen = () => setStatus('live')
      es.onerror = () => {
        if (!closed) setStatus('retrying')
      }
      es.addEventListener('event', (ev) => {
        try {
          const e = JSON.parse((ev as MessageEvent).data) as Event
          last = Math.max(last, eventMillis(e))
          const now = Date.now()
          times.push(now)
          while (times.length && times[0] < now - 60e3) times.shift()
          onEventRef.current(e)
        } catch {
          // malformed frame, ignore
        }
      })
      es.addEventListener('reset', () => {
        onNoticeRef.current('Live stream was too slow and reconnected; the gap is being replayed')
        es?.close()
        retry = window.setTimeout(() => open(last || undefined), 1000)
      })
      es.addEventListener('gap', () => onNoticeRef.current('More events were missed than the stream could replay; reload the page for a complete view'))
      es.addEventListener('warning', (ev) => {
        try {
          onNoticeRef.current(String(JSON.parse((ev as MessageEvent).data).error))
        } catch {
          // ignore
        }
      })
    }
    open()
    const id = window.setInterval(() => setRate(times.length / 60), 5000)
    return () => {
      closed = true
      es?.close()
      window.clearInterval(id)
      if (retry) window.clearTimeout(retry)
    }
  }, [url])

  return { status, rate }
}
