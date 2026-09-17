export function Help({ onClose }: { onClose: () => void }) {
  return (
    <div className="pop show help" onClick={(e) => e.stopPropagation()}>
      <div className="pophead">
        <span>Keyboard</span>
        <button className="ghost" onClick={onClose}>
          ✕
        </button>
      </div>
      <table>
        <tbody>
          <tr>
            <td>
              <kbd>/</kbd>
            </td>
            <td>Focus the filter bar</td>
            <td>
              <kbd>j</kbd> <kbd>k</kbd>
            </td>
            <td>Move the selection</td>
          </tr>
          <tr>
            <td>
              <kbd>Esc</kbd>
            </td>
            <td>Close details and popovers</td>
            <td>
              <kbd>[</kbd> <kbd>]</kbd>
            </td>
            <td>Shift the time window</td>
          </tr>
          <tr>
            <td>
              <kbd>l</kbd>
            </td>
            <td>Toggle the live tail</td>
            <td>
              <kbd>t</kbd>
            </td>
            <td>Toggle local time and UTC</td>
          </tr>
        </tbody>
      </table>
      <div className="hint">
        Filters: <span className="mono">key:value</span>, prefix with <span className="mono">-</span> to exclude, <span className="mono">*</span> at the end for a prefix. Keys: dc, service, node, check, kind, to, from, tag, team, app, version,
        type, healthy. Plain text searches names and check output. Click a facet to keep only that value, alt-click to exclude it. Drag on the histogram to zoom. With all DCs selected, dc: filters, the Datacenters facet and the histogram's datacenter split compare datacenters; the live tail stays per datacenter.
      </div>
    </div>
  )
}
