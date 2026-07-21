// RequirementDetail — everything known about one requirement, in a modal.
//
// Reached from a control's "Satisfies" list: a control claims to satisfy a
// requirement, and the only way to judge that claim is to read the obligation
// itself. A bare code like "7.2.1" is not something anyone can assess.
import { FileText, X, Link2, ShieldCheck } from 'lucide-react'
import { Badge } from './ui.jsx'

const RELATION_TONE = { equivalent: 'green', partial: 'amber', superset: 'blue', subset: 'blue' }

export default function RequirementDetail({ requirement, controls = [], onClose, onOpenControl }) {
  if (!requirement) return null
  const r = requirement

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" onClick={onClose}>
      <div className="bg-card border border-border rounded-card w-full max-w-2xl max-h-[85vh] flex flex-col"
        onClick={(e) => e.stopPropagation()}>

        <div className="flex items-start gap-3 px-4 py-3 border-b border-border shrink-0">
          <FileText size={16} className="text-sage shrink-0 mt-0.5" />
          <div className="min-w-0">
            <div className="font-mono text-[12px] text-sage">{r.number || r.code}</div>
            <div className="text-[13.5px] text-text">{r.title}</div>
            {r.categoryName && (
              <div className="text-[12px] text-muted mt-0.5">{r.categoryCode} · {r.categoryName}</div>
            )}
          </div>
          <button onClick={onClose} className="ml-auto p-1 rounded text-muted hover:text-text hover:bg-inset shrink-0">
            <X size={16} />
          </button>
        </div>

        <div className="flex-1 min-h-0 overflow-auto px-4 py-3 space-y-4">
          <div className="flex flex-wrap gap-1.5">
            {r.itemType && <Badge tone="grey">{r.itemType.replace(/_/g, ' ')}</Badge>}
            {r.origin === 'ai' && <Badge tone="blue">AI generated</Badge>}
          </div>

          {r.description ? (
            <div>
              <div className="eyebrow mb-1">Obligation</div>
              <p className="text-[13px] text-text2 whitespace-pre-wrap leading-relaxed">{r.description}</p>
            </div>
          ) : (
            <p className="text-[12.5px] text-muted">This requirement has no obligation text.</p>
          )}

          {r.guidance && (
            <div>
              <div className="eyebrow mb-1">Guidance</div>
              <p className="text-[12.5px] text-muted whitespace-pre-wrap">{r.guidance}</p>
            </div>
          )}

          <div>
            <div className="eyebrow mb-1.5">Satisfied by</div>
            {controls.length === 0 ? (
              <span className="text-[12px] text-muted">No control is linked to this requirement.</span>
            ) : (
              <div className="space-y-1">
                {controls.map((c) => (
                  <button key={c.id || c.code}
                    onClick={() => onOpenControl?.(c)}
                    className="flex items-start gap-2 w-full text-left px-2 py-1.5 rounded hover:bg-inset">
                    <ShieldCheck size={13} className="text-sage shrink-0 mt-0.5" />
                    <span className="min-w-0">
                      <span className="font-mono text-[11.5px] text-sage">{c.code}</span>
                      <span className="text-[12.5px] text-text2 ml-2">{c.name}</span>
                      {c.description && (
                        <span className="block text-[12px] text-muted truncate">{c.description}</span>
                      )}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>

          {r.mappings?.length > 0 && (
            <div>
              <div className="eyebrow mb-1.5">Cross-mappings</div>
              <div className="space-y-1">
                {r.mappings.map((m) => (
                  <div key={m.id} className="flex items-center gap-2 text-[12.5px]">
                    <Link2 size={12} className="text-muted shrink-0" />
                    <Badge tone={RELATION_TONE[m.relation] || 'grey'}>{m.relation}</Badge>
                    <span className="font-mono text-[12px] text-text">
                      {m.framework}{m.version ? `@${m.version}` : ''} · {m.item}
                    </span>
                    {/* Resolved means the target framework is published here and
                        the mapping points at a real requirement, not just a code. */}
                    {m.resolved ? <Badge tone="green">resolved</Badge> : <Badge tone="grey">stub</Badge>}
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
