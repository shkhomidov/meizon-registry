// StructureTree — the structure editor: categories → requirements, with
// relation-typed cross-mappings per requirement (resolved ✓ / stub ◌). The
// requirement is the assessable leaf. All authoring happens here in DRAFT;
// published versions render read-only.
import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, Plus, Trash2, Link2, Sparkles, ClipboardCheck } from 'lucide-react'
import { api } from '../lib/api.js'
import AIAssist from './AIAssist.jsx'
import { QuestionRow } from './QATab.jsx'
import { Badge, Button, Dialog, Field, Input, Select, Textarea, EmptyState } from './ui.jsx'

const RELATION_TONE = { equivalent: 'green', partial: 'amber', superset: 'blue', subset: 'blue' }

function cx(...p) { return p.filter(Boolean).join(' ') }

export default function StructureTree({ refId, structure, editable, onChanged, qaByReq = {}, qaTemplateId, qaEditable, onQuestionChanged }) {
  const [dialog, setDialog] = useState(null) // {level, parent}
  const [error, setError] = useState('')
  const [ai, setAi] = useState({ configured: false })
  const [assist, setAssist] = useState(false)
  const [notice, setNotice] = useState('')

  useEffect(() => {
    api.aiStatus().then(setAi).catch(() => {})
  }, [])

  async function del(level, code) {
    setError('')
    try {
      await api.deleteNode(refId, level, code)
      onChanged()
    } catch (e) { setError(e.message) }
  }

  return (
    <div className="space-y-3">
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}

      {notice && <div className="text-[12.5px]" style={{ color: 'var(--b-green)' }}>{notice}</div>}
      {editable && (
        <div className="flex justify-end gap-2">
          {ai.configured && (
            <Button size="sm" variant="ghost" icon={Sparkles} onClick={() => setAssist(true)}>Generate with AI</Button>
          )}
          <Button size="sm" icon={Plus} onClick={() => setDialog({ level: 'category' })}>Category</Button>
        </div>
      )}

      {structure.length === 0 && (
        <div className="bg-card border border-border rounded-card">
          <EmptyState icon={Link2} title="No structure yet"
            hint={editable ? 'Add a category to start authoring, or import a framework JSON.' : 'This version has no hierarchical structure.'} />
        </div>
      )}

      {structure.map((cat) => (
        <CategoryNode key={cat.code} refId={refId} cat={cat} editable={editable}
          onAdd={(level, parent) => setDialog({ level, parent })} onDelete={del} onChanged={onChanged}
          qaByReq={qaByReq} qaTemplateId={qaTemplateId} qaEditable={qaEditable} onQuestionChanged={onQuestionChanged} />
      ))}

      {dialog && (
        <AddNodeDialog refId={refId} level={dialog.level} parent={dialog.parent}
          onClose={() => setDialog(null)} onDone={() => { setDialog(null); onChanged() }} />
      )}
      {assist && (
        <AIAssist refId={refId} onClose={() => setAssist(false)}
          onApplied={(n) => { setAssist(false); setNotice(`applied ${n} AI proposal(s)`); onChanged() }} />
      )}
    </div>
  )
}

function NodeHeader({ open, setOpen, eyebrow, code, title, actions }) {
  return (
    <div className="flex items-center gap-2 py-1.5 group">
      <button onClick={() => setOpen(!open)} className="text-muted hover:text-text shrink-0">
        {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
      </button>
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-subtle w-24 shrink-0">{eyebrow}</span>
      <span className="font-mono text-[12px] text-sage shrink-0">{code}</span>
      <span className="text-[13px] text-text truncate">{title}</span>
      <span className="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">{actions}</span>
    </div>
  )
}

function IconBtn({ title, onClick, icon: Icon, danger }) {
  return (
    <button title={title} onClick={onClick}
      className={cx('p-1 rounded hover:bg-surface', danger ? 'text-muted hover:text-st-red' : 'text-muted hover:text-sage')}>
      <Icon size={14} />
    </button>
  )
}

function CategoryNode({ refId, cat, editable, onAdd, onDelete, onChanged, qaByReq, qaTemplateId, qaEditable, onQuestionChanged }) {
  const [open, setOpen] = useState(true)
  return (
    <div className="bg-card border border-border rounded-card px-3 py-1.5">
      <NodeHeader open={open} setOpen={setOpen} eyebrow="category" code={cat.code} title={cat.name}
        actions={editable && <>
          <IconBtn title="Add requirement" icon={Plus} onClick={() => onAdd('requirement', cat.code)} />
          <IconBtn title="Delete category" icon={Trash2} danger onClick={() => onDelete('category', cat.code)} />
        </>} />
      {open && cat.requirements.map((req) => (
        <RequirementNode key={req.code} refId={refId} req={req} editable={editable}
          onDelete={onDelete} onChanged={onChanged}
          questions={qaByReq[req.code] || []} qaTemplateId={qaTemplateId} qaEditable={qaEditable} onQuestionChanged={onQuestionChanged} />
      ))}
    </div>
  )
}

function RequirementNode({ refId, req, editable, onDelete, onChanged, questions = [], qaTemplateId, qaEditable, onQuestionChanged }) {
  const [open, setOpen] = useState(false)
  const resolved = req.mappings.filter((m) => m.resolved).length
  return (
    <div className="ml-5 border-l border-border pl-3">
      <div className="flex items-center gap-2 py-1.5 group">
        <button onClick={() => setOpen(!open)} className="text-muted hover:text-text shrink-0">
          {open ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
        </button>
        <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-subtle w-24 shrink-0">requirement</span>
        <span className="font-mono text-[12px] text-sage shrink-0">{req.number || req.code}</span>
        <span className="text-[13px] text-text truncate">{req.title}</span>
        {req.origin === 'ai' && <Badge tone="blue">AI</Badge>}
        {questions.length > 0 && (
          <span className="inline-flex items-center gap-1 font-mono text-[10px] text-muted shrink-0" title="Linked audit questions">
            <ClipboardCheck size={12} className="text-sage" />
            {questions.length}
          </span>
        )}
        {req.mappings.length > 0 && (
          <span className="inline-flex items-center gap-1 font-mono text-[10px] text-muted shrink-0">
            <Link2 size={12} className={resolved === req.mappings.length ? 'text-st-green' : 'text-st-amber'} />
            {resolved}/{req.mappings.length}
          </span>
        )}
        <span className="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
          {editable && <IconBtn title="Delete requirement" icon={Trash2} danger onClick={() => onDelete('requirement', req.code)} />}
        </span>
      </div>
      {open && (
        <div className="ml-7 mb-2 space-y-2">
          {req.description && <p className="text-[12.5px] text-text2 whitespace-pre-wrap">{req.description}</p>}
          <RequirementQuestions questions={questions} qaTemplateId={qaTemplateId} editable={qaEditable} onChanged={onQuestionChanged} />
          <MappingPanel refId={refId} item={req} editable={editable} onChanged={onChanged} />
        </div>
      )}
    </div>
  )
}

// RequirementQuestions shows the audit questions linked to this requirement (by
// requirementRef), editable in place. Nothing renders until a template exists.
function RequirementQuestions({ questions, qaTemplateId, editable, onChanged }) {
  if (!qaTemplateId || questions.length === 0) return null
  return (
    <div className="bg-inset border border-border rounded-md p-3 space-y-2">
      <div className="eyebrow">Audit questions</div>
      {questions.map((q) => (
        <QuestionRow key={q.id} templateId={qaTemplateId} question={q} editable={editable} onChanged={onChanged} />
      ))}
    </div>
  )
}

function MappingPanel({ refId, item, editable, onChanged }) {
  const [form, setForm] = useState({ relation: 'partial', framework: '', version: '', item: '', notes: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function add(e) {
    e.preventDefault()
    setError(''); setBusy(true)
    try {
      await api.addMapping(refId, item.code, form)
      setForm({ relation: 'partial', framework: '', version: '', item: '', notes: '' })
      onChanged()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  async function remove(id) {
    setError('')
    try {
      await api.removeMapping(refId, id)
      onChanged()
    } catch (err) { setError(err.message) }
  }

  return (
    <div className="bg-inset border border-border rounded-md p-3 space-y-2">
      <div className="eyebrow">Cross-mappings</div>

      {item.mappings.length === 0 && <div className="text-[12px] text-muted">No mappings yet.</div>}
      {item.mappings.map((m) => (
        <div key={m.id} className="flex items-center gap-2 text-[12.5px]">
          <Badge tone={RELATION_TONE[m.relation] || 'grey'}>{m.relation}</Badge>
          <span className="font-mono text-[12px] text-text">{m.framework}{m.version ? `@${m.version}` : ''}</span>
          <span className="text-muted">→</span>
          <span className="font-mono text-[12px] text-text">{m.item}</span>
          {m.resolved
            ? <Badge tone="green">resolved</Badge>
            : <Badge tone="grey">stub</Badge>}
          {m.notes && <span className="text-muted truncate">· {m.notes}</span>}
          {editable && (
            <button onClick={() => remove(m.id)} className="ml-auto text-muted hover:text-st-red"><Trash2 size={13} /></button>
          )}
        </div>
      ))}

      {editable && (
        <form onSubmit={add} className="flex flex-wrap items-end gap-2 pt-1">
          <div className="w-32">
            <Select value={form.relation} onChange={set('relation')}>
              <option value="equivalent">equivalent</option>
              <option value="partial">partial</option>
              <option value="superset">superset</option>
              <option value="subset">subset</option>
            </Select>
          </div>
          <div className="w-36"><Input placeholder="framework (iso-27001)" value={form.framework} onChange={set('framework')} /></div>
          <div className="w-24"><Input placeholder="version" value={form.version} onChange={set('version')} /></div>
          <div className="w-28"><Input placeholder="requirement (A.5.15)" value={form.item} onChange={set('item')} /></div>
          <div className="flex-1 min-w-[120px]"><Input placeholder="notes" value={form.notes} onChange={set('notes')} /></div>
          <Button size="sm" type="submit" busy={busy} icon={Link2}>Map</Button>
        </form>
      )}
      {error && <div className="text-[12px] text-st-red">{error}</div>}
    </div>
  )
}

function AddNodeDialog({ refId, level, parent, onClose, onDone }) {
  const [form, setForm] = useState({ code: '', name: '', number: '', title: '', description: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function submit() {
    setError(''); setBusy(true)
    try {
      if (level === 'category') await api.addCategory(refId, { code: form.code, name: form.name, description: form.description })
      if (level === 'requirement') await api.addRequirement(refId, { category: parent, code: form.code, number: form.number, title: form.title, description: form.description })
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title={`New ${level}${parent ? ` in ${parent}` : ''}`} onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Add {level}</Button></>}>
      <div className="grid grid-cols-3 gap-4">
        <Field label="Code"><Input value={form.code} onChange={set('code')} placeholder={level === 'item' ? '7.2.1' : 'G4'} autoFocus /></Field>
        {level === 'requirement' && <Field label="Number"><Input value={form.number} onChange={set('number')} placeholder="7" /></Field>}
      </div>
      {level === 'category'
        ? <Field label="Name"><Input value={form.name} onChange={set('name')} /></Field>
        : <Field label="Title"><Input value={form.title} onChange={set('title')} /></Field>}
      <Field label="Description"><Textarea rows="2" value={form.description} onChange={set('description')} /></Field>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
