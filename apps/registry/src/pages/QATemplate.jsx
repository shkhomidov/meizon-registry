// QATemplate — generate, view, and edit an audit questionnaire for a framework,
// and preview it as a chat conversation.
import { useCallback, useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft, Sparkles, MessageSquare, ListChecks, Trash2, Save } from 'lucide-react'
import { api } from '../lib/api.js'
import { Page, Card, Button, Badge, Textarea, EmptyState, Tabs } from '../components/ui.jsx'
import QAPreview from '../components/QAPreview.jsx'

export default function QATemplate() {
  const { ref } = useParams()
  const navigate = useNavigate()
  const [template, setTemplate] = useState(null)
  const [status, setStatus] = useState('idle') // idle | generating | ready | none
  const [error, setError] = useState('')
  const [tab, setTab] = useState('edit')

  const load = useCallback(async () => {
    try {
      const tpl = await api.qaTemplate(ref)
      setTemplate(tpl)
      setStatus('ready')
    } catch (e) {
      // Not-found is the expected "no template yet" state, not an error.
      if (/not found|resource/i.test(e.message)) setStatus('none')
      else setError(e.message)
    }
  }, [ref])
  useEffect(() => { load() }, [load])

  async function generate() {
    setError(''); setStatus('generating')
    try {
      const { jobId } = await api.qaGenerate(ref)
      // Poll the shared generate-status route until the job leaves running.
      for (;;) {
        await new Promise((r) => setTimeout(r, 1200))
        const st = await api.generateStatus(jobId)
        if (st.status === 'error') throw new Error(st.error || 'generation failed')
        if (st.status === 'done') break
      }
      await load()
    } catch (e) {
      setError(e.message); setStatus('none')
    }
  }

  const title = (
    <span className="flex items-center gap-3">
      <button onClick={() => navigate(`/frameworks/${ref}`)} className="text-muted hover:text-text"><ArrowLeft size={20} /></button>
      Audit template · {ref}
    </span>
  )

  const actions = template && (
    <Button variant="ghost" icon={Sparkles} onClick={generate} busy={status === 'generating'}>Regenerate</Button>
  )

  return (
    <Page title={title} subtitle="AI-generated audit questions, keyed to requirements" action={actions}>
      {error && <div className="text-st-red text-[12.5px] mb-3">{error}</div>}

      {status === 'none' && (
        <EmptyState icon={ListChecks} title="No audit template yet"
          hint="Generate an ordered set of audit questions from this framework's published requirements.">
          <Button icon={Sparkles} onClick={generate} busy={status === 'generating'}>Generate audit template</Button>
        </EmptyState>
      )}

      {status === 'generating' && !template && (
        <div className="text-muted text-[13px]">Generating questions from requirements…</div>
      )}

      {template && (
        <>
          <Tabs tabs={[
            { key: 'edit', label: 'Questions', icon: ListChecks },
            { key: 'preview', label: 'Chat preview', icon: MessageSquare },
          ]} active={tab} onChange={setTab} />

          {tab === 'edit'
            ? <Editor template={template} onChanged={load} />
            : <div className="mt-4"><QAPreview frameworkRef={ref} template={template} /></div>}
        </>
      )}
    </Page>
  )
}

function Editor({ template, onChanged }) {
  return (
    <div className="mt-4 space-y-5">
      {(template.sections || []).map((sec) => (
        <Card key={sec.ref} icon={ListChecks} title={`${sec.name} (${sec.ref})`}>
          <div className="space-y-2">
            {sec.questions.map((q) => (
              <QuestionRow key={q.id} templateId={template.id} question={q} onChanged={onChanged} />
            ))}
          </div>
        </Card>
      ))}
    </div>
  )
}

function QuestionRow({ templateId, question, onChanged }) {
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
          <div className="flex items-center gap-2 mt-1">
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
      </div>
    </div>
  )
}
