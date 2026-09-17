import { useState } from 'react'
import { fmtDateShort, fmtDur, fromInput, parseMoment, toInput, tzLabel, type Tz } from '../format'
import { PRESETS, rangeBounds } from '../state'
import type { Range } from '../types'

interface Props {
  range: Range
  tz: Tz
  retentionDays: number
  onRange: (r: Range) => void
  onTz: (tz: Tz) => void
  onClose: () => void
}

const WINDOWS: [number, string][] = [
  [5 * 60e3, '5 min'],
  [15 * 60e3, '15 min'],
  [30 * 60e3, '30 min'],
  [3600e3, '1 hour'],
  [3 * 3600e3, '3 hours'],
]

export function RangePopover({ range, tz, retentionDays, onRange, onTz, onClose }: Props) {
  const now = Date.now()
  const [from, to] = rangeBounds(range, now)
  const [at, setAt] = useState(toInput(range.kind === 'around' ? range.at : now - 86400e3, tz))
  const [win, setWin] = useState(range.kind === 'around' ? range.win : 30 * 60e3)
  const [absFrom, setAbsFrom] = useState(toInput(from, tz))
  const [absTo, setAbsTo] = useState(toInput(to, tz))
  const [paste, setPaste] = useState('')
  const [error, setError] = useState<string | null>(null)

  const around = (ms: number) => {
    if (Number.isNaN(ms)) {
      setError('That moment could not be parsed')
      return
    }
    onRange({ kind: 'around', at: ms, win })
  }

  return (
    <div className="pop show rpop" onClick={(e) => e.stopPropagation()}>
      <div className="pophead">
        <span>Time range</span>
        <span className="tzsw">
          <button className={tz === 'local' ? 'on' : ''} onClick={() => onTz('local')}>
            Local
          </button>
          <button className={tz === 'utc' ? 'on' : ''} onClick={() => onTz('utc')}>
            UTC
          </button>
          <button className="ghost" onClick={onClose} title="Close">
            ✕
          </button>
        </span>
      </div>
      <div className="popgrid">
        <div>
          <div className="popt">Quick</div>
          <div className="presets">
            {PRESETS.map((ms) => (
              <button key={ms} className={range.kind === 'preset' && range.ms === ms ? 'on' : ''} onClick={() => onRange({ kind: 'preset', ms })}>
                {fmtDur(ms)}
              </button>
            ))}
          </div>
          <div className="hint">
            A window ending at <b>now</b> keeps the live tail on. Drag on the histogram to zoom into a burst.
          </div>
        </div>
        <div>
          <div className="popt">Around a moment</div>
          <div className="frm">
            <label>at</label>
            <input type="datetime-local" step={1} value={at} onChange={(e) => setAt(e.target.value)} />
            <label>±</label>
            <select value={win} onChange={(e) => setWin(Number(e.target.value))}>
              {WINDOWS.map(([ms, label]) => (
                <option key={ms} value={ms}>
                  {label}
                </option>
              ))}
            </select>
            <div className="full">
              <button className="primary" onClick={() => around(fromInput(at, tz))}>
                Show around
              </button>
            </div>
            <label>paste</label>
            <input
              className="mono"
              placeholder="ISO, epoch s / ms, or 'yesterday 02:00'"
              value={paste}
              onChange={(e) => setPaste(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') around(parseMoment(paste, tz))
              }}
            />
          </div>
          <div className="hint">For “why was X down yesterday at 2 AM”: type the moment, get a ± window.</div>
        </div>
        <div>
          <div className="popt">Absolute</div>
          <div className="frm">
            <label>from</label>
            <input type="datetime-local" step={1} value={absFrom} onChange={(e) => setAbsFrom(e.target.value)} />
            <label>to</label>
            <input type="datetime-local" step={1} value={absTo} onChange={(e) => setAbsTo(e.target.value)} />
            <div className="full">
              <button
                className="primary"
                onClick={() => {
                  const a = fromInput(absFrom, tz)
                  const b = fromInput(absTo, tz)
                  if (Number.isNaN(a) || Number.isNaN(b) || b <= a) {
                    setError('The range needs a start before its end')
                    return
                  }
                  onRange({ kind: 'abs', from: a, to: b })
                }}
              >
                Apply
              </button>
            </div>
          </div>
          <div className="hint">
            Times are shown in <b>{tzLabel(tz)}</b>. Toggle above; the URL keeps the choice.
          </div>
        </div>
      </div>
      <div className="popfoot">
        <span>
          Retention {retentionDays} days · oldest stored event about <b>{fmtDateShort(now - retentionDays * 86400e3, tz)}</b>
        </span>
        <span>Rows marked legacy come from the previous version and carry fewer fields</span>
      </div>
      {error && <div className="hint err">{error}</div>}
    </div>
  )
}
