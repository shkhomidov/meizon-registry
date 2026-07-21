// AIAssist — the human-in-the-loop proposal dialog. The auditor picks a step,
// gives a brief, generates proposals, then edits/deselects each row before
// accepting. Nothing reaches the draft without the Accept action.
import { useState } from 'react'
import { Sparkles, Check } from 'lucide-react'
import { api } from '../lib/api.js'
import { Badge, Button, Dialog, Field, Input, Select, Textarea } from './ui.jsx'

const STEPS = [
  { key: 'categories', label: 'Categories', parent: null },
  { key: 'requirements', label: 'Requirements', parent: 'Category code' },
  { key: 'mappings', label: 'Cross-mappings', parent: null },
]

export default function AIAssist({ refId, onClose, onApplied }) {
  const [step, setStep] = useState('categories')
  const [brief, setBrief] = useState('')
  const [parent, setParent] = useState('')
  const [proposals, setProposals] = useState(null) // {generationId, rows:[{...,__selected}]}
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [accepting, setAccepting] = useState(false)

  const stepDef = STEPS.find((s) => s.key === step)

  async function generate() {
    setError(''); setBusy(true); setProposals(null)
    try {
      const res = await api.aiGenerate(refId, { step, brief, parent })
      const rows = flatten(step, res)
      setProposals({ generationId: res.generationId, rows })
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  async function accept() {
    const selected = proposals.rows.filter((r) => r.__selected)
    if (selected.length === 0) { setError('Select at least one proposal (or close to discard all).'); return }
    setError(''); setAccepting(true)
    try {
      const payload = unflatten(step, selected, parent)
      payload.generationId = proposals.generationId
      payload.step = step
      const res = await api.aiAccept(refId, payload)
      onApplied(res.applied)
    } catch (e) { setError(e.message) } finally { setAccepting(false) }
  }

  function update(i, key, value) {
    const rows = proposals.rows.slice()
    rows[i] = { ...rows[i], [key]: value }
    setProposals({ ...proposals, rows })
  }

  return (
    <Dialog title="Generate with AI" onClose={onClose}
      footer={proposals ? (
        <>
          <Button variant="ghost" onClick={onClose}>Discard all</Button>
          <Button busy={accepting} icon={Check} onClick={accept}>
            Accept selected ({proposals.rows.filter((r) => r.__selected).length})
          </Button>
        </>
      ) : (
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button busy={busy} icon={Sparkles} onClick={generate}>Generate proposals</Button>
        </>
      )}>
      {!proposals && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Step">
              <Select value={step} onChange={(e) => { setStep(e.target.value); setParent('') }}>
                {STEPS.map((s) => <option key={s.key} value={s.key}>{s.label}</option>)}
              </Select>
            </Field>
            {stepDef.parent && (
              <Field label={stepDef.parent}>
                <Input value={parent} onChange={(e) => setParent(e.target.value)} placeholder={step === 'requirements' ? 'G4' : 'Requirement 7'} />
              </Field>
            )}
          </div>
          <Field label="Brief" hint="What should the model draft? Context, scope, granularity…">
            <Textarea rows="3" value={brief} onChange={(e) => setBrief(e.target.value)}
              placeholder="e.g. Access-control requirements for a payment security standard" />
          </Field>
          <div className="text-[11.5px] text-muted">
            Proposals are recorded for audit and <span className="text-text">never applied without your acceptance</span>.
            For proprietary standards the model paraphrases — verify against the source text.
          </div>
        </>
      )}

      {proposals && (
        <div className="flex flex-col gap-2 max-h-[420px] overflow-y-auto">
          <div className="text-[11.5px] text-muted">Review each proposal — edit inline, untick to discard.</div>
          {proposals.rows.map((row, i) => (
            <div key={i} className={`border border-border rounded-md p-2.5 flex flex-col gap-1.5 ${row.__selected ? '' : 'opacity-45'}`}>
              <div className="flex items-center gap-2">
                <input type="checkbox" checked={row.__selected} onChange={(e) => update(i, '__selected', e.target.checked)} className="accent-[var(--b-sage)]" />
                <Input value={row.code || row.itemCode || ''} onChange={(e) => update(i, row.itemCode !== undefined ? 'itemCode' : 'code', e.target.value)} className="!w-32 font-mono !text-[12px]" />
                {step === 'mappings' ? (
                  <>
                    <Select value={row.relation} onChange={(e) => update(i, 'relation', e.target.value)} className="!w-32">
                      <option>equivalent</option><option>partial</option><option>superset</option><option>subset</option>
                    </Select>
                    <Input value={row.framework} onChange={(e) => update(i, 'framework', e.target.value)} className="!w-32 font-mono !text-[12px]" />
                    <Input value={row.item} onChange={(e) => update(i, 'item', e.target.value)} className="!w-28 font-mono !text-[12px]" />
                  </>
                ) : (
                  <Input value={row.name || row.title || ''} onChange={(e) => update(i, row.name !== undefined ? 'name' : 'title', e.target.value)} />
                )}
                <Badge tone="blue">AI</Badge>
              </div>
              {row.__section && <div className="text-[11px] text-muted ml-6">section {row.__section}</div>}
              {row.description !== undefined && row.description !== '' && (
                <Textarea rows="2" value={row.description} onChange={(e) => update(i, 'description', e.target.value)} className="ml-6 !w-auto" />
              )}
            </div>
          ))}
        </div>
      )}

      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}

// flatten turns per-step proposal payloads into a uniform editable row list.
function flatten(step, res) {
  const sel = (r) => ({ ...r, __selected: true })
  if (step === 'categories') return (res.categories || []).map(sel)
  if (step === 'requirements') return (res.requirements || []).map(sel)
  if (step === 'mappings') return (res.mappings || []).map(sel)
  return []
}

// unflatten rebuilds the accept payload from selected (possibly edited) rows.
function unflatten(step, rows, parent) {
  if (step === 'categories') return { categories: rows.map(({ __selected, ...r }) => r) }
  if (step === 'requirements') return { categoryCode: parent, requirements: rows.map(({ __selected, ...r }) => r) }
  return { mappings: rows.map(({ __selected, ...r }) => r) }
}
