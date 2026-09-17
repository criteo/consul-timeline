import { useEffect, useRef, useState, type RefObject } from 'react'
import { getSuggest } from '../api'
import { filterString, parseFilter, sameFilter } from '../state'
import { ENUM_VALUES, FILTER_KEYS, KEY_HELP, type Filter } from '../types'

interface Props {
  filters: Filter[]
  dc: string
  onChange: (filters: Filter[]) => void
  inputRef: RefObject<HTMLInputElement | null>
}

interface Suggestion {
  text: string // what goes into the input or becomes a chip
  hint: string
  kind: 'key' | 'value'
}

const SUGGEST_FIELDS = ['service', 'node', 'check', 'team', 'app', 'tag']

// suggestionsFor builds the dropdown for the typed text: filter keys while
// the field is being typed, values once a known key and a colon are in.
async function suggestionsFor(raw: string, dc: string, signal: AbortSignal): Promise<Suggestion[]> {
  const enums = ENUM_VALUES
  const trimmed = raw.trim()
  const neg = trimmed.startsWith('-') ? '-' : ''
  const body = neg ? trimmed.slice(1) : trimmed
  const i = body.indexOf(':')
  const out: Suggestion[] = []

  if (i > 0 && (FILTER_KEYS as readonly string[]).includes(body.slice(0, i).toLowerCase())) {
    const key = body.slice(0, i).toLowerCase()
    const prefix = body.slice(i + 1)
    let values: string[] = []
    if (enums[key]) values = enums[key].filter((v) => v.includes(prefix.toLowerCase()))
    else if (SUGGEST_FIELDS.includes(key) && prefix) values = await getSuggest(dc, key, prefix, signal)
    for (const v of values.slice(0, 12)) out.push({ text: `${neg}${key}:${v}`, hint: KEY_HELP[key], kind: 'value' })
    if (prefix && !enums[key] && !prefix.endsWith('*')) out.push({ text: `${neg}${key}:${prefix}*`, hint: 'prefix match', kind: 'value' })
    return out
  }

  const lower = body.toLowerCase()
  for (const k of FILTER_KEYS) if (!lower || k.startsWith(lower)) out.push({ text: `${neg}${k}:`, hint: KEY_HELP[k], kind: 'key' })
  if (body.length >= 2) {
    const [services, nodes, checks] = await Promise.all([
      getSuggest(dc, 'service', body, signal).catch(() => []),
      getSuggest(dc, 'node', body, signal).catch(() => []),
      getSuggest(dc, 'check', body, signal).catch(() => []),
    ])
    for (const v of services.slice(0, 5)) out.push({ text: `${neg}service:${v}`, hint: 'service', kind: 'value' })
    for (const v of nodes.slice(0, 3)) out.push({ text: `${neg}node:${v}`, hint: 'node', kind: 'value' })
    for (const v of checks.slice(0, 3)) out.push({ text: `${neg}check:${v}`, hint: 'check', kind: 'value' })
    if (!neg) out.push({ text: trimmed, hint: 'free text: names or check output contain this', kind: 'value' })
  }
  return out
}

export function FilterBar({ filters, dc, onChange, inputRef }: Props) {
  const [raw, setRaw] = useState('')
  const [items, setItems] = useState<Suggestion[]>([])
  const [index, setIndex] = useState(0)
  const [open, setOpen] = useState(false)
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => {
    if (!open) return
    const ctl = new AbortController()
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => {
      suggestionsFor(raw, dc, ctl.signal)
        .then((s) => {
          if (!ctl.signal.aborted) {
            setItems(s)
            setIndex((i) => Math.min(i, Math.max(0, s.length - 1)))
          }
        })
        .catch(() => undefined)
    }, 120)
    return () => {
      ctl.abort()
      window.clearTimeout(timer.current)
    }
  }, [raw, dc, open])

  const add = (text: string) => {
    const f = parseFilter(text)
    if (!f) return
    if (f.field === 'text') {
      // one free text at a time
      onChange([...filters.filter((x) => x.field !== 'text'), f])
    } else if (!filters.some((x) => sameFilter(x, f))) onChange([...filters, f])
    setRaw('')
    setIndex(0)
  }

  const apply = (s: Suggestion) => {
    if (s.kind === 'key') {
      setRaw(s.text)
      setIndex(0)
      return
    }
    add(s.text)
  }

  const onKey = (ev: React.KeyboardEvent<HTMLInputElement>) => {
    switch (ev.key) {
      case 'ArrowDown':
        ev.preventDefault()
        setIndex((i) => Math.min(items.length - 1, i + 1))
        break
      case 'ArrowUp':
        ev.preventDefault()
        setIndex((i) => Math.max(0, i - 1))
        break
      case 'Tab':
        if (items[index]) {
          ev.preventDefault()
          setRaw(items[index].text)
        }
        break
      case 'Enter': {
        ev.preventDefault()
        const chosen = items[index]
        if (chosen && (chosen.kind === 'key' || index > 0 || !raw.trim())) apply(chosen)
        else if (raw.trim()) add(raw)
        break
      }
      case 'Backspace':
        if (!raw && filters.length) onChange(filters.slice(0, -1))
        break
      case 'Escape':
        inputRef.current?.blur()
        break
    }
  }

  return (
    <div className="fbar" onClick={() => inputRef.current?.focus()}>
      <span className="ficon">⌕</span>
      <div className="chips">
        {filters.map((f, i) => (
          <span key={filterString(f)} className={'chip' + (f.not ? ' neg' : '')} title={filterString(f)}>
            {f.not && <span className="k">not </span>}
            {f.field !== 'text' && <span className="k">{f.field}:</span>}
            {f.value}
            <span
              className="x"
              onClick={(ev) => {
                ev.stopPropagation()
                onChange(filters.filter((_, j) => j !== i))
              }}
            >
              ✕
            </span>
          </span>
        ))}
      </div>
      <input
        ref={inputRef}
        value={raw}
        placeholder={filters.length ? 'add a filter…' : 'service:web-frontend*  -check:pod_running  to:critical  healthy:0  or free text'}
        autoComplete="off"
        spellCheck={false}
        onChange={(e) => {
          setRaw(e.target.value)
          setIndex(0)
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => window.setTimeout(() => setOpen(false), 150)}
        onKeyDown={onKey}
      />
      {(raw || filters.length > 0) && (
        <button
          className="ghost"
          title="Clear all filters"
          onClick={(ev) => {
            ev.stopPropagation()
            setRaw('')
            onChange([])
          }}
        >
          ✕
        </button>
      )}
      {open && items.length > 0 && (
        <div className="pop sug show">
          <div className="sgt">{raw.trim() ? 'Enter to add · Tab to complete' : 'Filter keys · type to search services, nodes and checks'}</div>
          {items.map((s, i) => (
            <div
              key={s.text + s.kind}
              className={'sg' + (i === index ? ' on' : '')}
              onMouseDown={(ev) => {
                ev.preventDefault()
                apply(s)
                inputRef.current?.focus()
              }}
            >
              <span className="k">{s.text}</span>
              <span className="d">{s.hint}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
