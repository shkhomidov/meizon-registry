// RegionSelect / RegionMultiSelect — the single place regions are chosen.
// Options are grouped continents → blocs → countries so the broadest scope is
// reachable without scrolling; the value stored is always the canonical CODE
// (see lib/regions.js). A value that isn't in the vocabulary (legacy data) is
// preserved as an extra option rather than being silently dropped.
import { X } from 'lucide-react'
import { REGION_GROUPS, isKnownRegion, regionLabel } from '../lib/regions.js'

const selectClass =
  'w-full bg-surface border border-border rounded-md px-3 py-2 text-[13px] text-text outline-none focus:border-sage'

function Groups() {
  return REGION_GROUPS.map((g) => (
    <optgroup key={g.label} label={g.label}>
      {g.options.map((o) => (
        <option key={o.code} value={o.code}>{o.name} ({o.code})</option>
      ))}
    </optgroup>
  ))
}

export default function RegionSelect({ value, onChange, className }) {
  const legacy = value && !isKnownRegion(value)
  return (
    <select value={value || ''} onChange={(e) => onChange(e.target.value)}
      onClick={(e) => e.stopPropagation()} className={`${selectClass} ${className || ''}`}>
      <option value="" disabled>Choose a region…</option>
      {legacy && <option value={value}>{value} (existing)</option>}
      <Groups />
    </select>
  )
}

// RegionMultiSelect edits a list of region codes as removable chips plus an
// "add" dropdown — used for role and token region scopes.
export function RegionMultiSelect({ value = [], onChange }) {
  const add = (code) => {
    if (!code || value.includes(code)) return
    onChange([...value, code])
  }
  const remove = (code) => onChange(value.filter((c) => c !== code))

  return (
    <div className="space-y-2">
      {value.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {value.map((code) => (
            <span key={code} className="inline-flex items-center gap-1 bg-inset border border-border rounded-badge px-2 py-0.5 text-[11.5px] font-mono">
              {code}
              <button type="button" onClick={() => remove(code)} className="text-muted hover:text-st-red" title={`Remove ${regionLabel(code)}`}>
                <X size={11} />
              </button>
            </span>
          ))}
        </div>
      )}
      <select value="" onChange={(e) => add(e.target.value)} className={selectClass}>
        <option value="">Add a region…</option>
        <Groups />
      </select>
    </div>
  )
}
