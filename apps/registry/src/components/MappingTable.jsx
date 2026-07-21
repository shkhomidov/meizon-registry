// MappingTable — the saved mappings between two frameworks, editable in place.
//
// Counts alone cannot be audited: "7 partial mappings" says nothing about
// whether any of them is right. This lists the actual pairs, names both ends,
// and lets an auditor correct or remove one.
import { useState } from 'react'
import { Trash2, Check, X, Pencil, AlertTriangle } from 'lucide-react'
import { api } from '../lib/api.js'
import { Badge, Button, Table, THead, TR, TH, TD, Select, Input, EmptyState, Card } from './ui.jsx'
import { GitCompareArrows } from 'lucide-react'

const RELATIONS = ['equivalent', 'partial', 'superset', 'subset']
const RELATION_TONE = { equivalent: 'green', partial: 'amber', superset: 'blue', subset: 'blue' }

export default function MappingTable({ sourceRef, targetRef, rows, editable, onChanged }) {
  const [editing, setEditing] = useState(null)   // mapping id
  const [form, setForm] = useState({ relation: 'partial', notes: '' })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function save(row) {
    setBusy(true); setError('')
    try {
      await api.updateMapping(sourceRef, row.id, {
        nodeKind: row.nodeKind, relation: form.relation, notes: form.notes,
      })
      setEditing(null)
      await onChanged()
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  async function remove(row) {
    setBusy(true); setError('')
    try {
      await api.deleteMappingRow(sourceRef, row.id, row.nodeKind)
      await onChanged()
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  return (
    <Card icon={GitCompareArrows}
      title={`Mappings — ${sourceRef} → ${targetRef} (${rows.length})`}
      className="mt-4">
      {error && <div className="text-[12.5px] text-st-red mb-2">{error}</div>}

      {rows.length === 0 ? (
        <EmptyState icon={GitCompareArrows} title="No mappings between these two"
          hint="Use Auto-map above to propose some, or add them on a requirement." />
      ) : (
        <Table>
          <THead>
            <TR>
              <TH>Type</TH><TH>Source</TH><TH>Target</TH><TH>Relation</TH>
              <TH>Notes</TH><TH>State</TH><TH></TH>
            </TR>
          </THead>
          <tbody>
            {rows.map((m) => {
              const isEditing = editing === m.id
              return (
                <TR key={m.id}>
                  <TD className="text-[12px] text-muted">{m.nodeKind}</TD>
                  <TD>
                    <div className="font-mono text-[11.5px] text-sage">{m.sourceRef}</div>
                    <div className="text-[12px] text-text2 truncate max-w-[190px]">{m.sourceName}</div>
                  </TD>
                  <TD>
                    <div className="font-mono text-[11.5px] text-text">{m.targetRef}</div>
                    <div className="text-[12px] text-text2 truncate max-w-[190px]">
                      {/* Blank when the target framework is not loaded here —
                          the mapping is a stub until it arrives. */}
                      {m.targetName || <span className="text-muted italic">not loaded</span>}
                    </div>
                  </TD>
                  <TD>
                    {isEditing ? (
                      <div className="w-32">
                        <Select value={form.relation} onChange={(e) => setForm({ ...form, relation: e.target.value })}>
                          {RELATIONS.map((r) => <option key={r} value={r}>{r}</option>)}
                        </Select>
                      </div>
                    ) : (
                      <>
                        <Badge tone={RELATION_TONE[m.relation] || 'grey'}>{m.relation}</Badge>
                        {m.confidence > 0 && (
                          <span className="ml-1.5 font-mono text-[10px] text-muted">{m.confidence.toFixed(2)}</span>
                        )}
                      </>
                    )}
                  </TD>
                  <TD className="text-[12px] text-muted max-w-[220px]">
                    {isEditing ? (
                      <Input value={form.notes} placeholder="why this mapping holds"
                        onChange={(e) => setForm({ ...form, notes: e.target.value })} />
                    ) : (
                      <span className="line-clamp-2">{m.notes || m.rationale || '—'}</span>
                    )}
                  </TD>
                  <TD>
                    {m.reviewState === 'needs_review' && (
                      <span title="The target framework changed since this was agreed">
                        <Badge tone="amber">re-check</Badge>
                      </span>
                    )}
                    {m.reviewState === 'orphaned' && (
                      <span title="The requirement this points at no longer exists">
                        <Badge tone="red">orphaned</Badge>
                      </span>
                    )}
                    {m.reviewState !== 'needs_review' && m.reviewState !== 'orphaned' && (
                      m.resolved ? <Badge tone="green">resolved</Badge> : <Badge tone="grey">stub</Badge>
                    )}
                  </TD>
                  <TD className="text-right whitespace-nowrap">
                    {!editable ? null : isEditing ? (
                      <span className="inline-flex gap-1">
                        <Button size="sm" busy={busy} icon={Check} onClick={() => save(m)}>Save</Button>
                        <Button size="sm" variant="ghost" icon={X} onClick={() => setEditing(null)}>Cancel</Button>
                      </span>
                    ) : (
                      <span className="inline-flex gap-1">
                        <button title="Edit this mapping"
                          onClick={() => { setEditing(m.id); setForm({ relation: m.relation, notes: m.notes || '' }) }}
                          className="p-1 rounded text-muted hover:text-sage hover:bg-inset">
                          <Pencil size={14} />
                        </button>
                        <button title="Delete this mapping"
                          onClick={() => remove(m)}
                          className="p-1 rounded text-muted hover:text-st-red hover:bg-inset">
                          <Trash2 size={14} />
                        </button>
                      </span>
                    )}
                  </TD>
                </TR>
              )
            })}
          </tbody>
        </Table>
      )}

      {!editable && rows.length > 0 && (
        // Mapping codes are inside the signed bundle, so editing one after
        // publication would invalidate the signature that vouches for it.
        <p className="flex items-start gap-2 text-[12px] text-muted mt-3">
          <AlertTriangle size={13} className="text-st-amber shrink-0 mt-0.5" />
          Mappings can only be edited while the source framework is a DRAFT.
        </p>
      )}
    </Card>
  )
}
