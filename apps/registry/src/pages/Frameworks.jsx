import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Plus, LibraryBig, Upload, Sparkles, Download, FileText } from 'lucide-react'
import { api } from '../lib/api.js'
import { useSession, canAuthor } from '../context/SessionContext.jsx'
import RegionSelect from '../components/RegionSelect.jsx'
import SourcePreview from '../components/SourcePreview.jsx'
import {
  Page, Card, Stat, Button, Badge, StatusBadge, Table, THead, TR, TH, TD,
  Dialog, Field, Input, Select, EmptyState,
} from '../components/ui.jsx'

export default function Frameworks() {
  const { viewer } = useSession()
  const navigate = useNavigate()
  const [frameworks, setFrameworks] = useState(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [importing, setImporting] = useState(false)
  const [aiConfigured, setAiConfigured] = useState(false)
  const [preview, setPreview] = useState(null) // { refId, source }

  async function download(ref) {
    setError('')
    try { await api.downloadFramework(ref) } catch (e) { setError(e.message) }
  }

  async function load() {
    try { setFrameworks(await api.listFrameworks()) } catch (e) { setError(e.message) }
  }
  useEffect(() => {
    load()
    api.aiStatus().then((s) => setAiConfigured(s.configured)).catch(() => {})
  }, [])

  const total = frameworks?.length || 0
  const publicCount = frameworks?.filter((f) => f.public).length || 0
  const regions = new Set(frameworks?.map((f) => f.region))

  return (
    <Page
      title="Frameworks"
      subtitle="Author, review and publish compliance frameworks. Published versions are ed25519-signed and distributed to GRC instances."
      action={canAuthor(viewer) && (
        <div className="flex gap-2">
          <Button variant="ghost" icon={Plus} onClick={() => setCreating(true)}>Blank</Button>
          <Button variant="ghost" icon={Upload} onClick={() => setImporting(true)}>Import JSON</Button>
          {aiConfigured && <Button icon={Sparkles} onClick={() => navigate('/frameworks/generate')}>Generate from document</Button>}
        </div>
      )}
    >
      <div className="grid grid-cols-2 md:grid-cols-3 gap-4 mb-6">
        <Stat value={total} label="Frameworks" />
        <Stat value={publicCount} label="Public catalog" tone="green" />
        <Stat value={regions.size} label="Regions" />
      </div>

      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}

      <Card icon={LibraryBig} title="All frameworks">
        {frameworks && frameworks.length === 0 ? (
          <EmptyState icon={LibraryBig} title="No frameworks yet" hint="Create your first framework to start authoring controls." />
        ) : (
          <Table>
            <THead>
              <TR>
                <TH>Reference</TH><TH>Name</TH><TH>Region</TH>
                <TH>Author</TH><TH>Updated</TH><TH>Source</TH><TH>Visibility</TH><TH></TH>
              </TR>
            </THead>
            <tbody>
              {frameworks?.map((f) => (
                <TR key={f.id} onClick={() => navigate(`/frameworks/${f.id}`)}>
                  <TD className="font-mono text-[12px] text-text">
                    {f.id}
                    {f.latestVersion && <span className="text-muted"> @{f.latestVersion}</span>}
                  </TD>
                  <TD className="text-text">{f.name}</TD>
                  <TD><Badge tone="blue">{f.region}</Badge></TD>
                  {/* Who to ask about this content. Email is the stable
                      identifier; the display name may be unset. */}
                  <TD className="text-[12.5px]">
                    {f.authorEmail ? (
                      <>
                        <div className="text-text truncate max-w-[160px]">{f.authorName || f.authorEmail}</div>
                        {f.authorName && <div className="text-muted truncate max-w-[160px]">{f.authorEmail}</div>}
                      </>
                    ) : <span className="text-muted">—</span>}
                  </TD>
                  <TD className="text-[12.5px] text-muted whitespace-nowrap">
                    <div title={`created ${new Date(f.createdAt).toLocaleString()}`}>
                      {new Date(f.updatedAt).toLocaleDateString()}
                    </div>
                    <div className="text-subtle">{new Date(f.updatedAt).toLocaleTimeString()}</div>
                  </TD>
                  <TD>
                    {f.source ? (
                      <button title={`Preview ${f.source.filename}`}
                        onClick={(e) => { e.stopPropagation(); setPreview({ refId: f.id, source: f.source }) }}
                        className="inline-flex items-center gap-1 text-[12px] text-sage hover:underline max-w-[150px]">
                        <FileText size={13} className="shrink-0" />
                        <span className="truncate">{f.source.filename}</span>
                      </button>
                    ) : (
                      // Hand-authored and imported frameworks have no source
                      // document, and neither do runs from before it was kept.
                      <span className="text-[12px] text-muted">—</span>
                    )}
                  </TD>
                  <TD>{f.public ? <Badge tone="green">public</Badge> : <Badge tone="grey">private</Badge>}</TD>
                  <TD className="text-right">
                    <button title="Download meizon-framework/v2 JSON"
                      onClick={(e) => { e.stopPropagation(); download(f.id) }}
                      className="p-1 rounded text-muted hover:text-sage hover:bg-inset">
                      <Download size={15} />
                    </button>
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {preview && (
        <SourcePreview refId={preview.refId} source={preview.source}
          onClose={() => setPreview(null)} />
      )}

      {creating && <CreateDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
      {importing && <ImportDialog onClose={() => setImporting(false)} onDone={() => { setImporting(false); load() }} />}
    </Page>
  )
}

// ImportDialog uploads a v2 exchange JSON document (hierarchy + mapping stubs)
// and creates a framework with a full draft — the UI-managed import path.
function ImportDialog({ onClose, onDone }) {
  const [fileName, setFileName] = useState('')
  const [doc, setDoc] = useState(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState(null)

  function onFile(e) {
    setError(''); setDoc(null); setFileName('')
    const file = e.target.files?.[0]
    if (!file) return
    setFileName(file.name)
    const reader = new FileReader()
    reader.onload = () => {
      try {
        setDoc(JSON.parse(reader.result))
      } catch {
        setError('The file is not valid JSON.')
      }
    }
    reader.readAsText(file)
  }

  async function submit() {
    if (!doc) { setError('Choose a framework JSON file first.'); return }
    setError(''); setBusy(true)
    try {
      setResult(await api.importFramework(doc))
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  if (result) {
    return (
      <Dialog title="Framework imported" onClose={onDone} footer={<Button onClick={onDone}>Done</Button>}>
        <div className="text-[13px]">
          Draft <span className="font-mono text-sage">{result.id}@{result.version}</span> created with its
          full structure and cross-mapping stubs. Review it, then submit → approve → publish; stubs resolve
          automatically when their target frameworks publish.
        </div>
      </Dialog>
    )
  }

  return (
    <Dialog title="Import framework (JSON)" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Import as draft</Button></>}>
      <Field label="Exchange document (schemaVersion 2.0)" hint="categories → requirements, with cross-mappings by code (legacy files nesting sections/items are flattened on import)">
        <input type="file" accept=".json,application/json" onChange={onFile}
          className="block w-full text-[13px] text-text2 file:mr-3 file:rounded-btn file:border-0 file:bg-sage file:text-sage-fg file:px-3 file:py-2 file:text-[12px] file:font-medium file:cursor-pointer bg-surface border border-border rounded-md" />
      </Field>
      {doc && (
        <div className="text-[12.5px] text-muted">
          <span className="font-mono text-text">{fileName}</span> — {doc.id}@{doc.version}, {doc.name}
        </div>
      )}
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}

function CreateDialog({ onClose, onDone }) {
  const [form, setForm] = useState({ reference: '', name: '', shortName: '', region: 'EU', license: 'public-domain', authority: '' })
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function submit() {
    setError(''); setBusy(true)
    try {
      await api.createFramework(form)
      onDone()
    } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog
      title="New framework"
      onClose={onClose}
      footer={<>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button busy={busy} onClick={submit}>Create draft</Button>
      </>}
    >
      <div className="grid grid-cols-2 gap-4">
        <Field label="Reference id"><Input value={form.reference} onChange={set('reference')} placeholder="nist-800-171-r2" /></Field>
        <Field label="Short name"><Input value={form.shortName} onChange={set('shortName')} placeholder="NIST 800-171" /></Field>
      </div>
      <Field label="Name"><Input value={form.name} onChange={set('name')} placeholder="NIST SP 800-171 Rev 2" /></Field>
      <div className="grid grid-cols-3 gap-4">
        <Field label="Region"><RegionSelect value={form.region} onChange={(v) => setForm({ ...form, region: v })} /></Field>
        <Field label="License">
          <Select value={form.license} onChange={set('license')}>
            <option value="public-domain">public-domain</option>
            <option value="statutory">statutory</option>
            <option value="proprietary">proprietary</option>
          </Select>
        </Field>
        <Field label="Authority"><Input value={form.authority} onChange={set('authority')} placeholder="NIST" /></Field>
      </div>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
