import { useEffect, useState } from 'react'
import { getInstance } from '../api'
import { fmtFull, fmtRel, type Tz } from '../format'
import { eventMillis, KIND_NODE, STATUS_NAMES, type Event, type Instance } from '../types'
import { Transition } from './EventList'

interface Props {
  event: Event
  tz: Tz
  onClose: () => void
  onAddFilter: (field: string, value: string) => void
  onAround: (at: number) => void
  onNotice: (msg: string) => void
}

function KV({ rows }: { rows: [string, React.ReactNode, string?][] }) {
  return (
    <div className="kv">
      {rows
        .filter(([, v]) => v !== '' && v !== undefined && v !== null && v !== 0)
        .map(([k, v, cls]) => (
          <div className="kvr" key={k}>
            <div className="k">{k}</div>
            <div className={`v ${cls ?? ''}`}>
              {typeof v === 'string' && /^https?:\/\//.test(v) ? (
                <a href={v} target="_blank" rel="noreferrer">
                  {v}
                </a>
              ) : (
                v
              )}
            </div>
          </div>
        ))}
    </div>
  )
}

function statusText(from: number, to: number) {
  return (
    <>
      <Transition from={from} to={to} /> <span className="muted">{`${STATUS_NAMES[from] ?? 'unknown'} → ${STATUS_NAMES[to] ?? 'unknown'}`}</span>
    </>
  )
}

export function Drawer({ event: e, tz, onClose, onAddFilter, onAround, onNotice }: Props) {
  const [instance, setInstance] = useState<Instance | null | undefined>(undefined)
  const ms = eventMillis(e)

  useEffect(() => {
    setInstance(undefined)
    if (!e.service_id || !e.node_name) return
    const ctl = new AbortController()
    getInstance(e.datacenter, e.node_name, e.service_id, ctl.signal)
      .then((i) => {
        if (!ctl.signal.aborted) setInstance(i)
      })
      .catch(() => {
        if (!ctl.signal.aborted) setInstance(null)
      })
    return () => ctl.abort()
  }, [e.datacenter, e.node_name, e.service_id])

  const tags = e.tags ?? instance?.tags ?? [] // legacy rows only have the registry's
  const meta = instance?.meta ?? {}
  const nodeMeta = instance?.node_meta ?? {}

  return (
    <aside className="drawer open">
      <div className="dh">
        <div>
          <h3>{e.kind === KIND_NODE ? e.node_name : e.service_name}</h3>
          <div className="sub">
            {fmtFull(ms, tz)} · {fmtRel(ms)}
            {e.legacy && (
              <>
                {' · '}
                <span className="tag legacy">legacy row</span>
              </>
            )}
          </div>
        </div>
        <button className="ghost" onClick={onClose} title="Close (Esc)">
          ✕
        </button>
      </div>
      <div className="dsec">
        <div className="actions">
          {e.service_name && <button onClick={() => onAddFilter('service', e.service_name!)}>+ this service</button>}
          {e.node_name && <button onClick={() => onAddFilter('node', e.node_name!)}>+ this node</button>}
          {e.check_name && <button onClick={() => onAddFilter('check', e.check_name!)}>+ this check</button>}
          {e.team && <button onClick={() => onAddFilter('team', e.team!)}>+ this team</button>}
          <button onClick={() => onAround(ms)}>± 5 min around</button>
          <button
            onClick={() => {
              const url = new URL(location.href)
              url.searchParams.set('r', `m:${ms},300000`)
              navigator.clipboard?.writeText(url.toString()).then(
                () => onNotice('Link to this moment copied'),
                () => onNotice(url.toString()),
              )
            }}
          >
            Copy link to this moment
          </button>
        </div>
      </div>
      <div className="dsec">
        <h4>Event</h4>
        <KV
          rows={[
            ['kind', ['unknown', 'check', 'instance', 'node'][e.kind] ?? 'unknown'],
            ['datacenter', e.datacenter],
            ['time', fmtFull(ms, tz), 'mono'],
            ['consul index', e.consul_index, 'mono'],
            ['event id', e.id, 'mono'],
          ]}
        />
      </div>
      <div className="dsec">
        <h4>Node</h4>
        <KV
          rows={[
            ['name', e.node_name],
            ['address', e.node_ip, 'mono'],
            ['rack', nodeMeta.rack_name],
            ['fqdn', nodeMeta.fqdn, 'mono'],
            ['cluster', nodeMeta.cluster_name],
            ['os', [nodeMeta.os_platform_family, nodeMeta.os_platform_version].filter(Boolean).join(' ')],
            e.kind === KIND_NODE ? ['status', statusText(e.old_node_status ?? 0, e.new_node_status ?? 0)] : ['', ''],
          ]}
        />
      </div>
      {e.kind !== KIND_NODE && (
        <div className="dsec">
          <h4>Service</h4>
          <KV
            rows={[
              ['name', e.service_name],
              ['instance id', e.service_id, 'mono'],
              ['team', e.team],
              ['application', e.app],
              ['version', e.version, 'mono'],
              ['address', instance ? `${instance.address}:${instance.port}` : '', 'mono'],
              ['namespace', meta.k8s_namespace],
              ['pod', meta.k8s_pod, 'mono'],
              ['controller', meta.k8s_controller, 'mono'],
              ['marathon app', meta.marathon_app_id],
              ['registered', instance ? `${fmtRel(Date.parse(instance.first_seen))}, last seen ${fmtRel(Date.parse(instance.last_seen))}` : ''],
              [
                'tags',
                tags.length ? (
                  <div className="tags">
                    {tags.map((t, i) => (
                      <span className="tag click" key={`${i}-${t}`} title="filter on this tag" onClick={() => onAddFilter('tag', t)}>
                        {t}
                      </span>
                    ))}
                  </div>
                ) : (
                  ''
                ),
              ],
              ['instance status', statusText(e.old_service_status ?? 0, e.new_service_status ?? 0)],
              ['healthy instances', <span key="h">{e.old_healthy} → <b>{e.new_healthy}</b>{e.total_instances ? ` of ${e.total_instances}` : ''}</span>],
            ]}
          />
          {instance === null && <div className="hint">No registration record for this instance: it predates this version or has been purged.</div>}
        </div>
      )}
      {e.check_name && (
        <div className="dsec">
          <h4>Check</h4>
          <KV
            rows={[
              ['name', e.check_name],
              ['id', e.check_id, 'mono'],
              ['type', e.check_type],
              ['status', statusText(e.old_check_status ?? 0, e.new_check_status ?? 0)],
            ]}
          />
          {e.check_output && <pre className="outp">{e.check_output}</pre>}
        </div>
      )}
      {instance && Object.keys(meta).length > 0 && (
        <details className="dsec">
          <summary>Service meta ({Object.keys(meta).length} keys)</summary>
          <KV rows={Object.keys(meta).sort().map((k) => [k, meta[k], 'mono'] as [string, string, string])} />
        </details>
      )}
      {instance && Object.keys(nodeMeta).length > 0 && (
        <details className="dsec">
          <summary>Node meta ({Object.keys(nodeMeta).length} keys)</summary>
          <KV rows={Object.keys(nodeMeta).sort().map((k) => [k, nodeMeta[k], 'mono'] as [string, string, string])} />
        </details>
      )}
    </aside>
  )
}
