// CatalogPolicies — policy templates: a name + markdown body linked to controls
// from the framework's control library.
import { useState } from 'react'
import { FileText, Plus, Trash2, Pencil } from 'lucide-react'
import { api } from '../lib/api.js'
import { Badge, Button, Dialog, Field, Input, Textarea, EmptyState } from './ui.jsx'

export default function CatalogPolicies({ refId, policies, controlCodes, editable, onChanged }) {
  const [editing, setEditing] = useState(null) // null | {} (new) | policy
  const [error, setError] = useState('')

  async function del(id) {
    setError('')
    try { await api.deletePolicyTemplate(refId, id); onChanged() } catch (e) { setError(e.message) }
  }

  return (
    <div className="space-y-3">
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
      {editable && (
        <div className="flex justify-end">
          <Button size="sm" icon={Plus} onClick={() => setEditing({})}>Policy template</Button>
        </div>
      )}

      {policies.length === 0 ? (
        <div className="bg-card border border-border rounded-card">
          <EmptyState icon={FileText} title="No policy templates yet"
            hint="Policy templates are reusable documents (markdown) linked to the controls they operationalise." />
        </div>
      ) : policies.map((p) => (
        <div key={p.id} className="bg-card border border-border rounded-card px-4 py-3 group">
          <div className="flex items-center gap-2">
            <FileText size={15} className="text-sage shrink-0" />
            <span className="text-[13.5px] font-medium text-text truncate">{p.name}</span>
            {p.origin === 'ai' && <Badge tone="blue">AI</Badge>}
            <span className="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
              {editable && <>
                <button onClick={() => setEditing(p)} className="p-1 rounded text-muted hover:text-sage hover:bg-surface"><Pencil size={14} /></button>
                <button onClick={() => del(p.id)} className="p-1 rounded text-muted hover:text-st-red hover:bg-surface"><Trash2 size={14} /></button>
              </>}
            </span>
          </div>
          <div className="flex flex-wrap gap-1.5 mt-2">
            {p.controls.map((c) => <Badge key={c} tone="green">{c}</Badge>)}
            {p.controls.length === 0 && <span className="text-[12px] text-muted">no controls linked</span>}
          </div>
          {p.body && (
            <pre className="mt-3 bg-inset border border-border rounded-md p-3 text-[12px] text-text2 whitespace-pre-wrap font-mono max-h-40 overflow-y-auto">{p.body}</pre>
          )}
        </div>
      ))}

      {editing !== null && (
        <PolicyDialog refId={refId} policy={editing} controlCodes={controlCodes}
          onClose={() => setEditing(null)} onDone={() => { setEditing(null); onChanged() }} />
      )}
    </div>
  )
}

function PolicyDialog({ refId, policy, controlCodes, onClose, onDone }) {
  const isNew = !policy.id
  const [name, setName] = useState(policy.name || '')
  const [body, setBody] = useState(policy.body || '')
  const [selected, setSelected] = useState(policy.controls || [])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  function toggle(code) {
    setSelected((s) => s.includes(code) ? s.filter((c) => c !== code) : [...s, code])
  }

  async function submit() {
    setError(''); setBusy(true)
    try {
      await api.upsertPolicyTemplate(refId, { id: policy.id || '', name, body, controls: selected })
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title={isNew ? 'New policy template' : `Edit — ${policy.name}`} onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>{isNew ? 'Create template' : 'Save changes'}</Button></>}>
      <Field label="Name"><Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Access Control Policy" autoFocus /></Field>
      <Field label="Body (markdown)">
        <Textarea rows="8" value={body} onChange={(e) => setBody(e.target.value)}
          placeholder={'# Access Control Policy\n\n## Purpose\n…'} className="font-mono !text-[12px]" />
      </Field>
      <Field label="Linked controls">
        <div className="flex flex-wrap gap-1.5 bg-surface border border-border rounded-md p-2.5 max-h-28 overflow-y-auto">
          {controlCodes.length === 0 && <span className="text-[12px] text-muted">no controls in the library yet</span>}
          {controlCodes.map((code) => (
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
