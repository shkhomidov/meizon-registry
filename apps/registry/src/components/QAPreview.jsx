// QAPreview — a chat-style dry run of an audit template.
//
// It walks the template's main sequence in order; each answer is scored by the
// server's shared evaluator (never re-implemented here), which also reports
// which follow-ups fired. Follow-ups are inserted inline before the sequence
// continues, so the flow the auditor will actually experience is what you see.
import { useMemo, useState } from 'react'
import { Send, RotateCcw, CheckCircle2 } from 'lucide-react'
import { api } from '../lib/api.js'
import { Button, Badge } from './ui.jsx'

const VERDICT_TONE = { compliant: 'green', partial: 'amber', non_compliant: 'red', not_applicable: 'grey' }

export default function QAPreview({ frameworkRef, template }) {
  // The main sequence is the non-conditional questions in order.
  const sequence = useMemo(() => {
    const out = []
    for (const s of template.sections || []) {
      for (const q of s.questions || []) if (!q.conditional) out.push(q)
    }
    return out
  }, [template])

  const byId = useMemo(() => {
    const m = {}
    for (const s of template.sections || []) for (const q of s.questions || []) m[q.id] = q
    return m
  }, [template])

  // queue is the list of question ids still to ask; transcript is what happened.
  const [queue, setQueue] = useState(() => sequence.map((q) => q.id))
  const [transcript, setTranscript] = useState([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const current = queue.length ? byId[queue[0]] : null

  function reset() {
    setQueue(sequence.map((q) => q.id))
    setTranscript([])
    setError('')
  }

  async function answer(rawAnswer, display) {
    if (!current) return
    setBusy(true); setError('')
    try {
      const res = await api.qaEvaluate(frameworkRef, current.id, rawAnswer)
      setTranscript((t) => [...t, {
        question: current, display, verdict: res.verdict, score: res.score,
      }])

      // Build the next queue: drop the answered question, insert any fired
      // follow-ups at the front, and honour a skipTo by dropping ahead.
      let rest = queue.slice(1)
      if (res.skipTo) {
        const at = rest.indexOf(res.skipTo)
        if (at >= 0) rest = rest.slice(at)
      }
      const follow = (res.followUps || []).filter((id) => byId[id])
      setQueue([...follow, ...rest])
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="max-w-2xl">
      <div className="space-y-3">
        {transcript.map((t, i) => (
          <div key={i} className="space-y-1.5">
            <div className="bg-inset rounded-lg px-3 py-2 text-[13px]">{t.question.text}</div>
            <div className="flex items-center gap-2 justify-end">
              <span className="bg-sage/15 text-sage rounded-lg px-3 py-1.5 text-[13px]">{t.display}</span>
              {t.verdict && (
                <Badge tone={VERDICT_TONE[t.verdict] || 'grey'}>
                  {t.verdict}{t.score != null ? ` · ${t.score}` : ''}
                </Badge>
              )}
            </div>
          </div>
        ))}
      </div>

      {error && <div className="text-st-red text-[12.5px] mt-3">{error}</div>}

      {current ? (
        <div className="mt-4 border-t border-border pt-4">
          <div className="text-[13px] mb-1">{current.text}</div>
          <div className="text-[11.5px] text-muted mb-3 font-mono">{current.requirementRef} · {current.type}</div>
          <AnswerControl question={current} busy={busy} onAnswer={answer} />
        </div>
      ) : (
        <Done transcript={transcript} template={template} onReset={reset} />
      )}

      {(transcript.length > 0 && current) && (
        <button onClick={reset} className="mt-4 text-[12px] text-muted hover:text-text inline-flex items-center gap-1">
          <RotateCcw size={12} /> Restart
        </button>
      )}
    </div>
  )
}

// AnswerControl renders the input appropriate to the question type and reports
// both the runner answer shape and a human display string.
function AnswerControl({ question, busy, onAnswer }) {
  const [text, setText] = useState('')
  const [num, setNum] = useState('')
  const [selected, setSelected] = useState([])

  const yesNo = (labels) => (
    <div className="flex gap-2 flex-wrap">
      {labels.map(({ value, label }) => (
        <Button key={value} size="sm" variant="ghost" disabled={busy}
          onClick={() => onAnswer({ value, evidence: value === 'yes' ? 1 : 0 }, label)}>{label}</Button>
      ))}
    </div>
  )

  switch (question.type) {
    case 'yes_no':
      return yesNo([{ value: 'yes', label: 'Yes' }, { value: 'no', label: 'No' }])
    case 'yes_no_na':
      return yesNo([{ value: 'yes', label: 'Yes' }, { value: 'no', label: 'No' }, { value: 'na', label: 'N/A' }])
    case 'yes_no_evidence':
      return yesNo([{ value: 'yes', label: 'Yes (with evidence)' }, { value: 'no', label: 'No' }])
    case 'scale':
      return (
        <div className="flex gap-1.5">
          {[0, 1, 2, 3, 4, 5].map((n) => (
            <Button key={n} size="sm" variant="ghost" disabled={busy}
              onClick={() => onAnswer({ score: n }, `Level ${n}`)}>{n}</Button>
          ))}
        </div>
      )
    case 'single_choice':
      return (
        <div className="flex gap-2 flex-wrap">
          {(question.options || []).map((o) => (
            <Button key={o.value} size="sm" variant="ghost" disabled={busy}
              onClick={() => onAnswer({ value: o.value }, o.label)}>{o.label}</Button>
          ))}
        </div>
      )
    case 'multi_choice':
      return (
        <div className="space-y-2">
          <div className="flex gap-2 flex-wrap">
            {(question.options || []).map((o) => {
              const on = selected.includes(o.value)
              return (
                <button key={o.value} disabled={busy}
                  onClick={() => setSelected((s) => on ? s.filter((x) => x !== o.value) : [...s, o.value])}
                  className={`px-2.5 py-1 rounded text-[12px] border ${on ? 'bg-sage/15 border-sage text-sage' : 'border-border text-muted'}`}>
                  {o.label}
                </button>
              )
            })}
          </div>
          <Button size="sm" icon={Send} disabled={busy}
            onClick={() => onAnswer({ selected }, selected.join(', ') || '(none)')}>Submit</Button>
        </div>
      )
    case 'numeric':
    case 'date':
      return (
        <div className="flex gap-2">
          <input type={question.type === 'date' ? 'date' : 'number'} value={num}
            onChange={(e) => setNum(e.target.value)}
            className="bg-surface border border-border rounded-md px-2 py-1 text-[13px] w-40" />
          <Button size="sm" icon={Send} disabled={busy}
            onClick={() => {
              if (question.type === 'date') {
                const age = Math.max(0, Math.round((Date.now() - new Date(num).getTime()) / 86400000))
                onAnswer({ ageDays: age }, num)
              } else {
                onAnswer({ numeric: Number(num) }, `${num}${question.unit ? ' ' + question.unit : ''}`)
              }
            }}>Submit</Button>
        </div>
      )
    case 'evidence':
      return (
        <div className="flex gap-2">
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ evidence: 1 }, 'Evidence attached')}>Attach evidence</Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ evidence: 0 }, 'None')}>None</Button>
        </div>
      )
    case 'attestation':
      return (
        <div className="flex gap-2">
          <Button size="sm" disabled={busy} onClick={() => onAnswer({ attested: true, evidence: 1 }, 'Attested')}>I attest</Button>
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => onAnswer({ attested: false }, 'Cannot attest')}>Cannot attest</Button>
        </div>
      )
    default: // free_text
      return (
        <div className="flex gap-2">
          <input value={text} onChange={(e) => setText(e.target.value)} placeholder={question.placeholder || 'Type an answer…'}
            className="bg-surface border border-border rounded-md px-2 py-1 text-[13px] flex-1" />
          <Button size="sm" icon={Send} disabled={busy} onClick={() => onAnswer({ value: text }, text || '(blank)')}>Send</Button>
        </div>
      )
  }
}

// Done shows the run summary — a per-verdict tally and an overall score computed
// from the template's verdict model, so the preview ends where a real audit would.
function Done({ transcript, template, onReset }) {
  const scored = transcript.filter((t) => t.verdict)
  const tally = scored.reduce((acc, t) => { acc[t.verdict] = (acc[t.verdict] || 0) + 1; return acc }, {})
  const nums = scored.map((t) => t.score).filter((s) => s != null)
  const overall = nums.length ? (nums.reduce((a, b) => a + b, 0) / nums.length) : null

  return (
    <div className="mt-4 border-t border-border pt-4">
      <div className="flex items-center gap-2 text-sage text-[13px] mb-3">
        <CheckCircle2 size={15} /> Audit complete
      </div>
      <div className="flex gap-2 flex-wrap mb-3">
        {Object.entries(tally).map(([v, n]) => (
          <Badge key={v} tone={VERDICT_TONE[v] || 'grey'}>{v}: {n}</Badge>
        ))}
      </div>
      {overall != null && (
        <div className="text-[13px] text-text2 mb-3">
          Overall score <span className="font-mono text-text">{overall.toFixed(2)}</span>
          {' '}<span className="text-muted">(excludes N/A)</span>
        </div>
      )}
      <Button size="sm" icon={RotateCcw} variant="ghost" onClick={onReset}>Run again</Button>
    </div>
  )
}
