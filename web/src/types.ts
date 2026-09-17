// Shapes served by /api/v1 and the vocabulary shared with the backend.

export const STATUS_NAMES = ['unknown', 'missing', 'critical', 'warning', 'passing', 'maintenance'] as const
export const KIND_NAMES = ['unknown', 'check', 'instance', 'node'] as const

// css class and glyph per status code
export const STATUS_CLASS = ['gone', 'gone', 'crit', 'warn', 'ok', 'maint'] as const
export const STATUS_GLYPH = ['?', '∅', '✕', '!', '✓', '⚙'] as const

export const KIND_CHECK = 1
export const KIND_INSTANCE = 2
export const KIND_NODE = 3

export interface Event {
  id?: number
  time: string
  datacenter: string
  kind: number
  consul_index?: number

  node_name?: string
  node_ip?: string
  old_node_status?: number
  new_node_status?: number

  service_name?: string
  service_id?: string
  team?: string
  app?: string
  version?: string
  tags?: string[] // the instance's service tags at the time
  old_service_status?: number
  new_service_status?: number
  old_healthy: number
  new_healthy: number
  total_instances?: number

  check_id?: string
  check_name?: string
  check_type?: string
  old_check_status?: number
  new_check_status?: number
  check_output?: string

  legacy?: boolean
}

export function oldStatus(e: Event): number {
  switch (e.kind) {
    case KIND_NODE:
      return e.old_node_status ?? 0
    case KIND_INSTANCE:
      return e.old_service_status ?? 0
    default:
      return e.old_check_status ?? 0
  }
}

export function newStatus(e: Event): number {
  switch (e.kind) {
    case KIND_NODE:
      return e.new_node_status ?? 0
    case KIND_INSTANCE:
      return e.new_service_status ?? 0
    default:
      return e.new_check_status ?? 0
  }
}

// a stable identity for deduplicating live and replayed frames
export function eventKey(e: Event): string {
  return `${e.time}|${e.datacenter}|${e.node_name ?? ''}|${e.service_id ?? ''}|${e.check_id ?? e.check_name ?? ''}|${e.kind}|${newStatus(e)}`
}

export function eventMillis(e: Event): number {
  return Date.parse(e.time)
}

export interface Instance {
  datacenter: string
  node_name: string
  service_id: string
  service_name: string
  node_ip?: string
  address?: string
  port?: number
  team?: string
  app?: string
  version?: string
  tags?: string[]
  meta?: Record<string, string>
  node_meta?: Record<string, string>
  first_seen: string
  last_seen: string
}

export interface Meta {
  local_dc: string
  datacenters: string[]
  retention_days: number
  version: string
  fields: string[]
  facet_fields: string[]
  kinds: string[]
  statuses: string[]
}

export interface Filter {
  field: string // a filter field, or "text" for the free text search
  value: string
  not: boolean
}

export type Range =
  | { kind: 'preset'; ms: number }
  | { kind: 'around'; at: number; win: number }
  | { kind: 'abs'; from: number; to: number }

export interface Bucket {
  t: string
  total: number
  by_status: Record<string, number>
}

export interface FacetValue {
  value: string
  count: number
}

export const FILTER_KEYS = ['service', 'node', 'check', 'kind', 'to', 'from', 'tag', 'team', 'app', 'version', 'type', 'healthy'] as const

export const KEY_HELP: Record<string, string> = {
  service: 'service name, * for a prefix',
  node: 'node name, * for a prefix',
  check: 'check name, * for a prefix',
  kind: 'check | instance | node',
  to: 'status after the event',
  from: 'status before the event',
  tag: 'any service tag of the instance, * for a prefix',
  team: 'owning team, from service meta',
  app: 'application, from service meta',
  version: 'deployed version, from service meta',
  type: 'check type: http tcp ttl script serf',
  healthy: 'healthy instances after the event, e.g. 0',
}

// fields whose values are a fixed vocabulary, completed without the API
export const ENUM_VALUES: Record<string, readonly string[]> = {
  kind: ['check', 'instance', 'node'],
  to: ['critical', 'warning', 'passing', 'missing', 'maintenance'],
  from: ['critical', 'warning', 'passing', 'missing', 'maintenance'],
  type: ['http', 'tcp', 'ttl', 'script', 'serf', 'grpc', 'alias'],
  healthy: ['0', '1', '2'],
}
