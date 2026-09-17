import type { Bucket, Event, FacetValue, Filter, Instance, Meta, Split } from './types'

const BASE = '/api/v1'

export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.status = status
  }
}

async function get<T>(path: string, params?: URLSearchParams, signal?: AbortSignal): Promise<T> {
  const url = params ? `${BASE}${path}?${params}` : `${BASE}${path}`
  const res = await fetch(url, { signal })
  if (!res.ok) {
    let msg = res.statusText
    try {
      const body = await res.json()
      if (body && body.error) msg = body.error
    } catch {
      // not json
    }
    throw new ApiError(msg, res.status)
  }
  return res.json() as Promise<T>
}

// Selection is what identifies a set of events: datacenter, bounds, filters.
export interface Selection {
  dc: string
  from?: number
  to?: number
  filters: Filter[]
}

export function selectionParams(s: Selection): URLSearchParams {
  const p = new URLSearchParams()
  p.set('dc', s.dc || 'all')
  if (s.from) p.set('from', String(Math.floor(s.from)))
  if (s.to) p.set('to', String(Math.ceil(s.to)))
  for (const f of s.filters) {
    if (f.field === 'text') p.set('q', f.value)
    else p.append('f', `${f.not ? '-' : ''}${f.field}:${f.value}`)
  }
  return p
}

export const getMeta = (signal?: AbortSignal) => get<Meta>('/meta', undefined, signal)

export interface EventsPage {
  events: Event[]
  next_cursor?: string
  has_more: boolean
}

export function getEvents(s: Selection, cursor: string | undefined, limit: number, signal?: AbortSignal): Promise<EventsPage> {
  const p = selectionParams(s)
  p.set('limit', String(limit))
  if (cursor) p.set('cursor', cursor)
  return get<EventsPage>('/events', p, signal)
}

export interface HistogramResponse {
  bucket_seconds: number
  sampled: boolean
  split: Split
  buckets: Bucket[]
}

export function getHistogram(s: Selection, buckets: number, split: Split, signal?: AbortSignal): Promise<HistogramResponse> {
  const p = selectionParams(s)
  p.set('buckets', String(buckets))
  if (split !== 'status') p.set('split', split)
  return get<HistogramResponse>('/histogram', p, signal)
}

export interface FacetsResponse {
  sample_size: number
  sampled: boolean
  facets: Record<string, FacetValue[]>
}

export function getFacets(s: Selection, fields: string[], limit: number, signal?: AbortSignal): Promise<FacetsResponse> {
  const p = selectionParams(s)
  p.set('fields', fields.join(','))
  p.set('limit', String(limit))
  return get<FacetsResponse>('/facets', p, signal)
}

export async function getSuggest(dc: string, field: string, q: string, signal?: AbortSignal): Promise<string[]> {
  const p = new URLSearchParams({ dc: dc || 'all', field, q, limit: '15' })
  const res = await get<{ values: string[] }>('/suggest', p, signal)
  return res.values ?? []
}

export async function getInstance(dc: string, node: string, id: string, signal?: AbortSignal): Promise<Instance | null> {
  try {
    return await get<Instance>('/instance', new URLSearchParams({ dc, node, id }), signal)
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null
    throw err
  }
}

// streamUrl is the Server-Sent Events endpoint for the current filters,
// without bounds: the stream is live by definition.
export function streamUrl(s: Selection): string {
  const p = selectionParams({ dc: s.dc, filters: s.filters })
  return `${BASE}/stream?${p}`
}
