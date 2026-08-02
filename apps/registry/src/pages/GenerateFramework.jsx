// GenerateFramework — the document-first generation studio (a dedicated page,
// not a modal). Step 1 collects the source (upload or paste) + guidance; step 2
// shows generation progress; step 3 is the split review/editor where the AI
// result is edited in place against the highlighted source, then committed as a
// DRAFT. In new-version mode (route carries :ref) the result is diffed against
// the framework's current latest version and committed as its next version.
import { useState, useEffect, useRef } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { Sparkles, Upload, FileText, Check, ArrowLeft, GitBranch, Info } from 'lucide-react'
import { api } from '../lib/api.js'
import { Page, Card, Button, Field, Input, Textarea, Badge } from '../components/ui.jsx'
import GenerationReview from '../components/GenerationReview.jsx'
import IngestProgress from '../components/IngestProgress.jsx'
import RegionSelect from '../components/RegionSelect.jsx'

export default function GenerateFramework() {
  const { ref } = useParams() // present ⇒ new-version mode
  const isVersion = Boolean(ref)
  const navigate = useNavigate()
  const [search] = useSearchParams()
  // ?job=<id> reopens a finished run. A generation produces a PROPOSAL, not a
  // framework — it only becomes one when accepted here — so without a way back
  // to this screen a completed run was paid for and then unreachable.
  const resumeJob = search.get('job') || ''

  const [step, setStep] = useState('source') // source | generating | review
  const [mode, setMode] = useState('file') // file | text
  const [file, setFile] = useState(null)
  const [fileName, setFileName] = useState('')
  const [text, setText] = useState('')
  const [brief, setBrief] = useState('')
  const [version, setVersion] = useState('')
  const [region, setRegion] = useState('') // empty = infer from the document
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const [proposal, setProposal] = useState(null) // { document, provenance, documentText, summary, note?, diff?, baselineVersion? }
  const [doc, setDoc] = useState(null)
  const [active, setActive] = useState(null)
  const [jobStatus, setJobStatus] = useState(null) // live pipeline progress
  const [ocrNote, setOcrNote] = useState('')
  const [jobId, setJobId] = useState('')
  const pollRef = useRef(null)

  // Stop polling when the component unmounts.
  useEffect(() => () => clearTimeout(pollRef.current), [])

  // Reopen a run started elsewhere (another tab, or before a restart).
  useEffect(() => {
    if (!resumeJob) return
    setJobId(resumeJob)
    setStep('generating')
    setBusy(true)
    pollJob(resumeJob)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resumeJob])

  function pollJob(jobId) {
    const tick = async () => {
      try {
        const st = await api.generateStatus(jobId)
        setJobStatus(st)
        if (st.status === 'done') {
          const res = st.result
          const next = structuredClone(res.document)
          // An explicitly chosen region wins over whatever the model inferred.
          if (region) next.regions = [region]
          setProposal({ ...res, diff: st.diff, baselineVersion: st.baselineVersion, resumed: Boolean(resumeJob) })
          setDoc(next)
          setStep('review'); setBusy(false)
          return
        }
        if (st.status === 'error') {
          setError(`Generation failed at the ${st.step || 'pipeline'} step: ${st.error || 'unknown error'}`)
          setStep('source'); setBusy(false)
          return
        }
        pollRef.current = setTimeout(tick, 1500)
      } catch (e) {
        setError(e.message); setStep('source'); setBusy(false)
      }
    }
    tick()
  }

  async function generate() {
    setError('')
    if (mode === 'file' && !file) { setError('Choose a document to upload.'); return }
    if (mode === 'text' && !text.trim()) { setError('Paste the document text.'); return }
    if (isVersion && !version.trim()) { setError('Enter the new version identifier.'); return }
    setBusy(true); setJobStatus(null); setStep('generating')
    try {
      const res = isVersion
        ? (mode === 'file'
          ? await api.generateNextVersionFromFile(ref, file, brief, version)
          : await api.generateNextVersionFromText(ref, { text, brief, version }))
        : (mode === 'file'
          ? await api.generateFromFile(file, brief)
          : await api.generateFromText(text, brief))
      // Say what OCR is about to read before it reads it — the cost is
      // incurred whether or not anyone sees it afterwards.
      if (res.ocrPages > 0) {
        setOcrNote(`${res.ocrPages} of ${res.pages} page(s) have no text layer and will be read by OCR.`)
      } else {
        setOcrNote('')
      }
      setJobId(res.jobId)
      pollJob(res.jobId)
    } catch (e) {
      setError(e.message); setStep('source'); setBusy(false)
    }
  }

  async function commit() {
    setError(''); setBusy(true)
    try {
      const res = isVersion
        ? await api.acceptNextVersion(ref, version, doc)
        : await api.acceptGenerated(doc, jobId)
      navigate(`/frameworks/${res.id}`)
    } catch (e) { setError(e.message); setBusy(false) }
  }

  const title = isVersion ? `New version of ${ref}` : 'Generate framework from a document'
  const subtitle = isVersion
    ? 'Upload the revised standard. Changes are detected against the current latest version; review and edit before creating the new draft version.'
    : 'Upload or paste a source standard. The AI structures it into the required format; review and edit against the source before creating a draft.'

  // Review step: a full-height workspace with a single compact header row —
  // document name, wizard steps and the action buttons — then the split editor.
  if (step === 'review' && doc) {
    return (
      <div className="flex flex-col h-[calc(100vh-4.5rem)] px-6 py-4">
        <div className="flex items-center justify-between gap-4 mb-3">
          <div className="flex items-center gap-2 min-w-0">
            <FileText size={16} className="text-sage shrink-0" />
            <span className="text-[15px] font-medium text-text truncate">{doc.name || doc.id}</span>
            <span className="font-mono text-[11.5px] text-muted shrink-0">{doc.id} · v{isVersion ? version : doc.version}</span>
            <RegionSelect value={doc.regions?.[0] || ''} className="!w-[170px] !py-1 !text-[11.5px] shrink-0 truncate"
              onChange={(v) => setDoc({ ...doc, regions: [v] })} />
            {isVersion && proposal?.baselineVersion && <Badge tone="amber">diff vs {proposal.baselineVersion}</Badge>}
          </div>
          <div className="flex items-center gap-3 shrink-0">
            <Stepper step={step} isVersion={isVersion} inline />
            <Button variant="ghost" onClick={() => setStep('source')}>Back</Button>
            <Button icon={isVersion ? GitBranch : Check} busy={busy} onClick={commit}>
              {isVersion ? `Create version ${version}` : 'Create draft'}
            </Button>
          </div>
        </div>
        {error && <div className="text-[12.5px] text-st-red mb-2">{error}</div>}
        {proposal.note && (
          <div className="flex items-start gap-2 text-[12px] text-text2 bg-st-amber/10 border border-st-amber/30 rounded-md px-3 py-2 mb-2">
            <Info size={14} className="text-st-amber shrink-0 mt-0.5" />
            <span><span className="font-medium">QA note:</span> {proposal.note}</span>
          </div>
        )}
        {/* Advisory checks. These used to abort the whole run; they are shown
            here instead so the reviewer — who can see the actual requirements —
            decides whether they matter. */}
        {(proposal.warnings || []).map((wtext, i) => (
          <div key={i} className="flex items-start gap-2 text-[12px] text-text2 bg-st-amber/10 border border-st-amber/30 rounded-md px-3 py-2 mb-2">
            <Info size={14} className="text-st-amber shrink-0 mt-0.5" />
            <span><span className="font-medium">Check:</span> {wtext}</span>
          </div>
        ))}
        <GenerationReview
          doc={doc} setDoc={setDoc}
          documentText={proposal.documentText}
          sourceFile={mode === 'file' ? file : null}
          provenance={proposal.provenance || {}}
          diff={isVersion ? proposal.diff : null}
          duplicates={proposal.duplicates || []}
          active={active} setActive={setActive}
        />
      </div>
    )
  }

  return (
    <Page title={title} subtitle={subtitle}
      action={<Button variant="ghost" icon={ArrowLeft} onClick={() => navigate(isVersion ? `/frameworks/${ref}` : '/')}>Back</Button>}>
      <Stepper step={step} isVersion={isVersion} />

      {step === 'source' && (
        <Card icon={FileText} title="Source document">
          <div className="flex gap-1 border border-border rounded-md p-0.5 w-max mb-4">
            {['file', 'text'].map((m) => (
              <button key={m} onClick={() => setMode(m)}
                className={`px-3 py-1.5 text-[12px] rounded ${mode === m ? 'bg-surface text-text' : 'text-muted hover:text-text'}`}>
                {m === 'file' ? 'Upload file' : 'Paste text'}
              </button>
            ))}
          </div>

          {mode === 'file' ? (
            <Field label="Standard document" hint="PDF, Excel, CSV, text or markdown. A framework JSON export is imported as-is.">
              <label className="flex items-center gap-2 bg-surface border border-border rounded-md px-3 py-2 cursor-pointer text-[13px] max-w-lg">
                <Upload size={15} className="text-sage" />
                <span className="text-muted">{fileName || 'Choose a file…'}</span>
                <input type="file" accept=".pdf,.xlsx,.csv,.tsv,.txt,.md,.markdown,.json,text/*,application/pdf,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
                  onChange={(e) => { const f = e.target.files?.[0]; setFile(f || null); setFileName(f ? f.name : '') }}
                  className="hidden" />
              </label>
            </Field>
          ) : (
            <Field label="Document text">
              <Textarea rows="10" value={text} onChange={(e) => setText(e.target.value)}
                placeholder="Paste the standard's text — requirements, sections, numbering…" className="font-mono !text-[12px]" />
            </Field>
          )}

          <div className="grid grid-cols-2 gap-4 max-w-2xl">
            {isVersion && (
              <Field label="New version" hint="must not already exist">
                <Input value={version} onChange={(e) => setVersion(e.target.value)} placeholder="2.0" />
              </Field>
            )}
            <Field label="Region" hint="leave unset to use the region stated in the document">
              <RegionSelect value={region} onChange={setRegion} />
            </Field>
            <Field label="Guidance (optional)" hint="steer scope, granularity, mapping targets">
              <Input value={brief} onChange={(e) => setBrief(e.target.value)} placeholder="e.g. map access controls to ISO 27001" />
            </Field>
          </div>

          {error && <div className="text-[12.5px] text-st-red mt-3">{error}</div>}
          <div className="mt-5">
            <Button icon={Sparkles} busy={busy} onClick={generate}>Generate</Button>
          </div>
        </Card>
      )}

      {step === 'generating' && (
        <Card>
          <IngestProgress status={jobStatus} />
          {ocrNote && (
            <p className="text-[12.5px] text-muted text-center mt-2">{ocrNote}</p>
          )}
        </Card>
      )}

    </Page>
  )
}

function Stepper({ step, isVersion, inline }) {
  const steps = [
    { key: 'source', label: '1 Source' },
    { key: 'generating', label: '2 Generating' },
    { key: 'review', label: isVersion ? '3 Review & apply changes' : '3 Review & edit' },
  ]
  const order = ['source', 'generating', 'review']
  const cur = order.indexOf(step)
  return (
    <div className={`flex items-center gap-2 text-[12px] ${inline ? '' : 'mb-5'}`}>
      {steps.map((s, i) => (
        <div key={s.key} className="flex items-center gap-2">
          <span className={`px-2.5 py-1 rounded-full border ${i <= cur ? 'border-sage text-sage bg-sage/10' : 'border-border text-muted'}`}>{s.label}</span>
          {i < steps.length - 1 && <span className="text-border">→</span>}
        </div>
      ))}
    </div>
  )
}
