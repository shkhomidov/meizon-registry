// QATab — the audit-template authoring surface, shown as a tab on a framework
// and (for now) on the standalone /qa route. It generates/regenerates the
// template, toggles its draft/ready status, edits questions, and previews the
// audit as a chat. The template is generated per framework VERSION (draft or
// published) and lives beside the requirements it audits.
import { useState, useEffect } from 'react'
import { Sparkles, MessageSquare, ListChecks, Trash2, Save, CheckCircle2, Undo2, Languages } from 'lucide-react'
import { api } from '../lib/api.js'
import { Card, Button, Badge, Textarea, EmptyState, Tabs, Select } from './ui.jsx'
import QAPreview from './QAPreview.jsx'

export function countQuestions(template) {
  let n = 0
  for (const s of template?.sections || []) n += (s.questions || []).length
  return n
}

// indexByRequirement groups a template's questions by their requirementRef, so
// the structure tree can show each requirement's linked questions.
export function indexByRequirement(template) {
  const by = {}
  for (const s of template?.sections || []) {
    for (const q of s.questions || []) {
      (by[q.requirementRef] ||= []).push(q)
    }
  }
  return by
}

export default function QATab({ refId, template, editable, onChanged }) {
  const [tab, setTab] = useState('edit')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [coverage, setCoverage] = useState(null)
  // Language viewing: '' is the canonical (editable) template; a code shows that
  // translation read-only. Available languages come from the framework's own.
  const [lang, setLang] = useState('')
  const [langs, setLangs] = useState([])
  const [translated, setTranslated] = useState(null) // fetched non-canonical view

  useEffect(() => {
    api.translations(refId)
      .then((v) => setLangs((v?.languages || []).map((l) => l.language).filter((l) => l && l !== v.canonical)))
      .catch(() => {})
  }, [refId])

  useEffect(() => {
    if (!lang) { setTranslated(null); return }
    let live = true
    api.qaTemplate(refId, lang).then((t) => { if (live) setTranslated(t) }).catch((e) => { if (live) setError(e.message) })
    return () => { live = false }
  }, [refId, lang])

  // The canonical template is editable; a translation is a read-only view.
  const viewing = lang ? translated : template
  const canEdit = editable && !lang

  async function generate() {
    setError(''); setBusy(true)
    try {
      const { jobId } = await api.qaGenerate(refId)
      // Poll the shared generate-status route until the job leaves running.
      for (;;) {
        await new Promise((r) => setTimeout(r, 1200))
        const st = await api.generateStatus(jobId)
        if (st.status === 'error') throw new Error(st.error || 'generation failed')
        if (st.status === 'done') { setCoverage(st.coverage || null); break }
      }
      await onChanged()
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  async function setStatus(status) {
    setError('')
    try { await api.qaSetStatus(template.templateId, status); await onChanged() }
    catch (e) { setError(e.message) }
  }

  if (!template) {
    return (
      <div className="space-y-3">
        {error && <div className="text-st-red text-[12.5px]">{error}</div>}
        <EmptyState icon={ListChecks} title="No audit template yet"
          hint="Generate the audit questions from this version's requirements. This normally runs automatically when a framework is created, if an LLM is configured.">
          {editable && <Button icon={Sparkles} onClick={generate} busy={busy}>Generate audit template</Button>}
        </EmptyState>
      </div>
    )
  }

  const ready = template.status === 'ready'

  return (
    <div className="space-y-4">
      {error && <div className="text-st-red text-[12.5px]">{error}</div>}
      {coverage && <CoverageNote coverage={coverage} onDismiss={() => setCoverage(null)} />}

      <div className="flex items-center gap-3 flex-wrap">
        <Badge tone={ready ? 'green' : 'amber'}>{ready ? 'ready' : 'draft'}</Badge>
        <span className="text-[12.5px] text-muted">{countQuestions(viewing || template)} questions · {template.model || 'ai'}</span>
        {langs.length > 0 && (
          <label className="flex items-center gap-1.5 text-[12px] text-muted">
            <Languages size={13} />
            <Select value={lang} onChange={(e) => setLang(e.target.value)}>
              <option value="">Canonical</option>
              {langs.map((l) => <option key={l} value={l}>{l}</option>)}
            </Select>
          </label>
        )}
        {lang && <Badge tone="grey">translation · read-only</Badge>}
        {canEdit && (
          <div className="ml-auto flex items-center gap-2">
            {ready
              ? <Button size="sm" variant="ghost" icon={Undo2} onClick={() => setStatus('draft')}>Back to draft</Button>
              : <Button size="sm" variant="ghost" icon={CheckCircle2} onClick={() => setStatus('ready')}>Mark ready</Button>}
            <Button size="sm" variant="ghost" icon={Sparkles} busy={busy} onClick={generate}>Regenerate</Button>
          </div>
        )}
      </div>

      {lang && !translated ? (
        <div className="text-muted text-[13px]">Loading {lang} translation…</div>
      ) : lang ? (
        // A translation is view-only. The chat preview scores against canonical
        // question ids, so it stays on the canonical template.
        <Editor template={viewing} editable={false} onChanged={onChanged} />
      ) : (
        <>
          <Tabs tabs={[
            { key: 'edit', label: 'Questions', icon: ListChecks },
            { key: 'preview', label: 'Chat preview', icon: MessageSquare },
          ]} active={tab} onChange={setTab} />
          {tab === 'edit'
            ? <Editor template={template} editable={editable} onChanged={onChanged} />
            : <div className="mt-4"><QAPreview frameworkRef={refId} template={template} /></div>}
        </>
      )}
    </div>
  )
}

function Editor({ template, editable, onChanged }) {
  return (
    <div className="mt-4 space-y-5">
      {(template.sections || []).map((sec) => (
        <Card key={sec.ref} icon={ListChecks} title={`${sec.name} (${sec.ref})`}>
          <div className="space-y-2">
            {sec.questions.map((q) => (
              <QuestionRow key={q.id} templateId={template.templateId} question={q} editable={editable} onChanged={onChanged} />
            ))}
          </div>
        </Card>
      ))}
    </div>
  )
}

// QuestionRow renders one question with its type and follow-up markers, and (when
// editable) lets the author edit its text or delete it. Shared shape with the
// per-requirement view in StructureTree.
export function QuestionRow({ templateId, question, editable, onChanged }) {
  const [editing, setEditing] = useState(false)
  const [text, setText] = useState(question.text)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function save() {
    setBusy(true); setError('')
    try {
      await api.qaUpdateQuestion(templateId, question.id, { ...question, text })
      setEditing(false)
      await onChanged()
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }
  async function remove() {
    if (!window.confirm('Delete this question?')) return
    setBusy(true); setError('')
    try { await api.qaDeleteQuestion(templateId, question.id); await onChanged() }
    catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  return (
    <div className="border border-border rounded-md px-3 py-2">
      <div className="flex items-start justify-between gap-3">
        <div className="flex-1 min-w-0">
          {editing ? (
            <Textarea value={text} onChange={(e) => setText(e.target.value)} rows={2} />
          ) : (
            <div className="text-[13px]">{question.text}</div>
          )}
          {/* Intent is the hint: why the question is asked / what it establishes. */}
          {question.intent && !editing && (
            <div className="text-[12px] text-muted italic mt-0.5">{question.intent}</div>
          )}
          <div className="flex items-center gap-2 mt-1 flex-wrap">
            <span className="font-mono text-[11px] text-sage">{question.requirementRef}</span>
            <Badge tone="grey">{question.type}</Badge>
            {question.conditional && <Badge tone="amber">conditional</Badge>}
            {question.weight ? <span className="text-[11px] text-muted">weight {question.weight}</span> : null}
            {(question.followUps || []).length > 0 && (
              <span className="text-[11px] text-muted">{question.followUps.length} follow-up{question.followUps.length > 1 ? 's' : ''}</span>
            )}
          </div>
          {error && <div className="text-st-red text-[11.5px] mt-1">{error}</div>}
        </div>
        {editable && (
          <div className="flex items-center gap-1 shrink-0">
            {editing ? (
              <>
                <Button size="sm" icon={Save} busy={busy} onClick={save}>Save</Button>
                <Button size="sm" variant="ghost" onClick={() => { setEditing(false); setText(question.text) }}>Cancel</Button>
              </>
            ) : (
              <>
                <button title="Edit" onClick={() => setEditing(true)} className="text-[12px] text-muted hover:text-sage px-2 py-1">Edit</button>
                <button title="Delete" onClick={remove} className="p-1 text-muted hover:text-st-red"><Trash2 size={14} /></button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

// CoverageNote surfaces what the last generation produced per requirement: totals
// plus any requirements the model left empty that were auto-backfilled (re-asked
// or given a synthesized baseline), so a reviewer knows which to check first.
export function CoverageNote({ coverage, onDismiss }) {
  const backfilled = coverage.backfilled || []
  const reasked = backfilled.filter((b) => b.how === 'reask')
  const synth = backfilled.filter((b) => b.how === 'synthesized')
  return (
    <div className="rounded-lg border border-border bg-surface px-3.5 py-2.5 text-[12.5px]">
      <div className="flex items-center justify-between gap-3">
        <span className="text-text">
          Generated <b>{coverage.questions}</b> question{coverage.questions === 1 ? '' : 's'} across{' '}
          <b>{coverage.requirements}</b> requirement{coverage.requirements === 1 ? '' : 's'}.
          {backfilled.length === 0
            ? ' Every requirement was answered directly.'
            : ` ${backfilled.length} requirement${backfilled.length === 1 ? '' : 's'} needed backfill.`}
        </span>
        <button onClick={onDismiss} className="text-muted hover:text-text shrink-0">Dismiss</button>
      </div>
      {backfilled.length > 0 && (
        <div className="mt-2 space-y-1 text-muted">
          {reasked.length > 0 && <div>Re-asked individually: {reasked.map((b) => b.ref).join(', ')}</div>}
          {synth.length > 0 && <div>Synthesized a baseline question for: {synth.map((b) => b.ref).join(', ')}</div>}
        </div>
      )}
    </div>
  )
}
