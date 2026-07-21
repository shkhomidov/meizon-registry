// CatalogControls — the framework's control library: implementable controls
// linked to requirement items, each with evidence guidance rows.
import { useState, useMemo } from 'react'
import { ShieldCheck, Plus, Trash2, ChevronDown, ChevronRight, ClipboardCheck } from 'lucide-react'
import { api } from '../lib/api.js'
import { Badge, Button, Card, Dialog, Field, Input, Select, Textarea, EmptyState } from './ui.jsx'
import RequirementDetail from './RequirementDetail.jsx'

const EVIDENCE_TONE = { automated_test: 'green', document: 'blue', policy: 'amber', interview: 'grey', observation: 'grey' }

export default function CatalogControls({ refId, controls, itemCodes, structure = [], editable, onChanged }) {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [openRequirement, setOpenRequirement] = useState(null)

  // Requirements indexed by code, carrying their category — so a control's
  // "satisfies" list can show what it actually satisfies instead of a bare
  // code, and open the obligation on click.
  const requirementByCode = useMemo(() => {
    const map = {}
    for (const c of structure || []) {
      for (const r of c.requirements || []) {
        map[r.code] = { ...r, categoryCode: c.code, categoryName: c.name }
      }
    }
    return map
  }, [structure])

  // Which controls satisfy a given requirement — the reverse of control.items.
  const controlsByRequirement = useMemo(() => {
    const map = {}
    for (const c of controls || []) {
      for (const code of c.items || []) {
        (map[code] = map[code] || []).push(c)
      }
    }
    return map
  }, [controls])

  async function del(id) {
    setError('')
    try { await api.deleteControlEntry(refId, id); onChanged() } catch (e) { setError(e.message) }
  }

  return (
    <div className="space-y-3">
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
      {editable && (
        <div className="flex justify-end">
          <Button size="sm" icon={Plus} onClick={() => setCreating(true)}>Control</Button>
        </div>
      )}

      {controls.length === 0 ? (
        <div className="bg-card border border-border rounded-card">
          <EmptyState icon={ShieldCheck} title="No controls yet"
            hint="Controls are the implementable measures that satisfy requirement items. Add one and link it to items." />
        </div>
      ) : controls.map((c) => (
        <ControlRow key={c.id} refId={refId} control={c} editable={editable} onDelete={del} onChanged={onChanged}
          requirementByCode={requirementByCode} onOpenRequirement={setOpenRequirement} />
      ))}

      {creating && (
        <ControlDialog refId={refId} itemCodes={itemCodes}
          onClose={() => setCreating(false)} onDone={() => { setCreating(false); onChanged() }} />
      )}

      {openRequirement && (
        <RequirementDetail
          requirement={openRequirement}
          controls={controlsByRequirement[openRequirement.code] || []}
          onClose={() => setOpenRequirement(null)} />
      )}
    </div>
  )
}

function ControlRow({ refId, control, editable, onDelete, onChanged, requirementByCode = {}, onOpenRequirement }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="bg-card border border-border rounded-card px-4 py-2">
      <div className="flex items-center gap-2 py-1 group">
        <button onClick={() => setOpen(!open)} className="text-muted hover:text-text shrink-0">
          {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        </button>
        <span className="font-mono text-[12px] text-sage shrink-0">{control.code}</span>
        <span className="text-[13px] text-text truncate">{control.name}</span>
        {control.domain && <Badge tone="grey">{control.domain}</Badge>}
        {control.origin === 'ai' && <Badge tone="blue">AI</Badge>}
        <span className="font-mono text-[10px] text-muted shrink-0">{control.items.length} item(s) · {control.evidence.length} evidence</span>
        <span className="ml-auto opacity-0 group-hover:opacity-100 transition-opacity">
          {editable && (
            <button onClick={() => onDelete(control.id)} className="p-1 rounded text-muted hover:text-st-red hover:bg-surface"><Trash2 size={14} /></button>
          )}
        </span>
      </div>

      {open && (
        <div className="ml-6 mb-2 space-y-3">
          {control.description && <div className="text-[12.5px] text-muted">{control.description}</div>}
          <div>
            <div className="eyebrow mb-1.5">Satisfies {control.items.length} requirement(s)</div>
            {control.items.length === 0 ? (
              <span className="text-[12px] text-muted">none linked</span>
            ) : (
              <div className="space-y-0.5">
                {control.items.map((code) => {
                  const r = requirementByCode[code]
                  return (
                    <button key={code}
                      onClick={() => r && onOpenRequirement?.(r)}
                      disabled={!r}
                      title={r ? 'Open this requirement' : 'This requirement is not in the current version'}
                      className={`flex items-start gap-2 w-full text-left px-1.5 py-1 rounded ${r ? 'hover:bg-inset cursor-pointer' : 'cursor-default'}`}>
                      <span className="font-mono text-[11.5px] text-sage shrink-0 mt-px w-24 truncate">{code}</span>
                      {/* The title is the point: a control claiming to satisfy
                          "7.2.1" is unreviewable until you can see what 7.2.1
                          actually requires. */}
                      <span className={`text-[12.5px] min-w-0 ${r ? 'text-text2' : 'text-muted italic'}`}>
                        {r ? r.title : 'not in this version'}
                      </span>
                    </button>
                  )
                })}
              </div>
            )}
          </div>
          <EvidencePanel refId={refId} control={control} editable={editable} onChanged={onChanged} />
        </div>
      )}
    </div>
  )
}

function EvidencePanel({ refId, control, editable, onChanged }) {
  const [form, setForm] = useState({ type: 'document', hint: '', cadence: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function add(e) {
    e.preventDefault(); setError(''); setBusy(true)
    try {
      await api.addEvidence(refId, control.id, {
        type: form.type, hint: form.hint,
        renewalCadenceMonths: form.cadence ? Number(form.cadence) : null,
      })
      setForm({ type: 'document', hint: '', cadence: '' })
      onChanged()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  async function del(id) {
    setError('')
    try { await api.deleteEvidence(refId, id); onChanged() } catch (err) { setError(err.message) }
  }

  return (
    <div className="bg-inset border border-border rounded-md p-3 space-y-2">
      <div className="eyebrow flex items-center gap-1.5"><ClipboardCheck size={12} className="text-sage" /> Evidence guidance</div>
      {control.evidence.length === 0 && <div className="text-[12px] text-muted">No evidence guidance yet.</div>}
      {control.evidence.map((ev) => (
        <div key={ev.id} className="flex items-center gap-2 text-[12.5px]">
          <Badge tone={EVIDENCE_TONE[ev.type] || 'grey'}>{ev.type}</Badge>
          <span className="text-text2 truncate">{ev.hint || '—'}</span>
          {ev.renewalCadenceMonths && <span className="font-mono text-[11px] text-muted shrink-0">every {ev.renewalCadenceMonths}mo</span>}
          {ev.origin === 'ai' && <Badge tone="blue">AI</Badge>}
          {editable && (
            <button onClick={() => del(ev.id)} className="ml-auto text-muted hover:text-st-red shrink-0"><Trash2 size={13} /></button>
          )}
        </div>
      ))}
      {editable && (
        <form onSubmit={add} className="flex flex-wrap items-end gap-2 pt-1">
          <div className="w-40">
            <Select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })}>
              <option value="automated_test">automated_test</option>
              <option value="document">document</option>
              <option value="policy">policy</option>
              <option value="interview">interview</option>
              <option value="observation">observation</option>
            </Select>
          </div>
          <div className="flex-1 min-w-[160px]"><Input placeholder="hint (e.g. quarterly access review report)" value={form.hint} onChange={(e) => setForm({ ...form, hint: e.target.value })} /></div>
          <div className="w-28"><Input type="number" placeholder="cadence (mo)" value={form.cadence} onChange={(e) => setForm({ ...form, cadence: e.target.value })} /></div>
          <Button size="sm" type="submit" busy={busy} icon={Plus}>Evidence</Button>
        </form>
      )}
      {error && <div className="text-[12px] text-st-red">{error}</div>}
    </div>
  )
}

function ControlDialog({ refId, itemCodes, onClose, onDone }) {
  const [form, setForm] = useState({ code: '', name: '', description: '', domain: '' })
  const [selected, setSelected] = useState([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  function toggle(code) {
    setSelected((s) => s.includes(code) ? s.filter((c) => c !== code) : [...s, code])
  }

  async function submit() {
    setError(''); setBusy(true)
    try {
      await api.addControlEntry(refId, { ...form, items: selected })
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title="New control" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Add control</Button></>}>
      <div className="grid grid-cols-2 gap-4">
        <Field label="Code"><Input value={form.code} onChange={set('code')} placeholder="PCI-7.2" autoFocus /></Field>
        <Field label="Domain"><Input value={form.domain} onChange={set('domain')} placeholder="access_control" /></Field>
      </div>
      <Field label="Name"><Input value={form.name} onChange={set('name')} /></Field>
      <Field label="Description"><Textarea rows="2" value={form.description} onChange={set('description')} /></Field>
      <Field label="Satisfies items" hint="link the requirement items this control addresses">
        <div className="flex flex-wrap gap-1.5 bg-surface border border-border rounded-md p-2.5 max-h-32 overflow-y-auto">
          {itemCodes.length === 0 && <span className="text-[12px] text-muted">no items in the latest version yet</span>}
          {itemCodes.map((code) => (
            <button key={code} type="button" onClick={() => toggle(code)}
              className={`font-mono text-[11px] px-1.5 py-0.5 rounded-badge border transition-colors ${selected.includes(code) ? 'text-sage-fg bg-sage border-sage' : 'text-muted border-border hover:text-text'}`}>
              {code}
            </button>
          ))}
        </div>
      </Field>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
