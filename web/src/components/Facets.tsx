import type { FacetsResponse } from '../api'
import { STATUS_CLASS, STATUS_GLYPH, STATUS_NAMES, type Filter } from '../types'

interface Props {
  data: FacetsResponse | null
  error: string | null
  filters: Filter[]
  legacyCount: number
  outageCount: number
  allDatacenters: boolean
  onToggle: (field: string, value: string, not: boolean) => void
}

const GROUPS: [string, string][] = [
  ['kind', 'Kind'],
  ['to', 'Transition to'],
  ['tag', 'Tags'],
  ['team', 'Team'],
  ['service', 'Services'],
  ['node', 'Nodes'],
  ['check', 'Checks'],
]

function label(field: string, value: string) {
  if (field === 'to') {
    const code = STATUS_NAMES.indexOf(value as (typeof STATUS_NAMES)[number])
    return (
      <>
        <span className={`st ${STATUS_CLASS[code] ?? 'gone'}`}>{STATUS_GLYPH[code] ?? '?'}</span>
        {value}
      </>
    )
  }
  return value
}

export function Facets({ data, error, filters, legacyCount, outageCount, allDatacenters, onToggle }: Props) {
  const active = (field: string, value: string) => {
    const f = filters.find((x) => x.field === field && x.value === value)
    return f ? (f.not ? 'off' : 'on') : ''
  }
  return (
    <aside className="facets">
      <div className="fsum">
        <span>
          <b>{data ? `${data.sampled ? '≥ ' : ''}${data.sample_size.toLocaleString()}` : '…'}</b> events
        </span>
        {outageCount > 0 && (
          <span className="pill crit" title="loaded events where a service dropped to 0 healthy instances" onClick={() => onToggle('healthy', '0', false)}>
            {outageCount} → 0 healthy
          </span>
        )}
      </div>
      {error && <div className="legacynote err">{error}</div>}
      {data?.sampled && <div className="legacynote">Counts come from the {data.sample_size.toLocaleString()} most recent matching events.</div>}
      {GROUPS.map(([field, title]) => {
        const values = data?.facets[field] ?? []
        if (!values.length) return null
        const top = values[0].count || 1
        return (
          <div className="fg" key={field}>
            <div className="fgt">{title}</div>
            {values.map((v) => (
              <div
                key={v.value}
                className={`fi ${active(field, v.value)}`}
                title="click: only this · alt-click or right-click: exclude"
                onClick={(e) => onToggle(field, v.value, e.altKey || e.ctrlKey || e.metaKey)}
                onContextMenu={(e) => {
                  e.preventDefault()
                  onToggle(field, v.value, true)
                }}
              >
                <span className="bar" style={{ width: `${Math.round((v.count / top) * 100)}%` }} />
                <span className="fv">{label(field, v.value)}</span>
                <span className="fc">{v.count.toLocaleString()}</span>
              </div>
            ))}
          </div>
        )
      })}
      {allDatacenters && <div className="legacynote">History across every datacenter. The live tail only exists per datacenter.</div>}
      {legacyCount > 0 && (
        <div className="legacynote">
          <b>{legacyCount.toLocaleString()}</b> loaded rows predate this version. They keep time, node, service, check, statuses, counts and output; tags, team, version, check type and id are not available for them.
        </div>
      )}
    </aside>
  )
}
