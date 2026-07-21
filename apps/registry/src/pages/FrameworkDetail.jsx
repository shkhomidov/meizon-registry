import { useEffect, useState, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft, Send, Check, UploadCloud, ListChecks, History, Info, GitBranch, Download } from 'lucide-react'
import { api } from '../lib/api.js'
import { useSession, canApprove, canAuthor } from '../context/SessionContext.jsx'
import StructureTree from '../components/StructureTree.jsx'
import LanguageBar from '../components/LanguageBar.jsx'
import AutoMapPanel from '../components/AutoMapPanel.jsx'
import CatalogControls from '../components/CatalogControls.jsx'
import CatalogPolicies from '../components/CatalogPolicies.jsx'
import {
  Page, Card, Button, StatusBadge, Tabs, Table, THead, TR, TH, TD, EmptyState,
} from '../components/ui.jsx'

function countRequirements(structure) {
  let n = 0
  for (const c of structure || []) n += c.requirements.length
  return n
}

export default function FrameworkDetail() {
  const { ref } = useParams()
  const navigate = useNavigate()
  const { viewer } = useSession()
  const [data, setData] = useState(null)
  const [structure, setStructure] = useState([])
  const [controls, setControls] = useState([])
  const [policies, setPolicies] = useState([])
  const [error, setError] = useState('')
  const [tab, setTab] = useState('structure')
  const [aiConfigured, setAiConfigured] = useState(false)
  const [frameworks, setFrameworks] = useState([])

  const load = useCallback(async () => {
    setError('')
    try {
      const [detail, tree, lib, pols] = await Promise.all([
        api.getFramework(ref), api.getStructure(ref),
        api.controlsLibrary(ref), api.policyTemplates(ref),
      ])
      setData(detail)
      setStructure(tree)
      setControls(lib)
      setPolicies(pols)
    } catch (e) { setError(e.message) }
  }, [ref])
  useEffect(() => { load() }, [load])
  useEffect(() => {
    api.aiStatus().then((s) => setAiConfigured(s.configured)).catch(() => {})
    api.listFrameworks().then(setFrameworks).catch(() => {})
  }, [])

  async function act(fn) {
    setError('')
    try { await fn(); await load() } catch (e) { setError(e.message) }
  }

  if (error && !data) return <div className="text-st-red text-[13px]">{error}</div>
  if (!data) return <div className="text-muted text-[13px]">Loading…</div>

  const { framework, versions, latestControls } = data
  const latest = versions[0]
  const status = latest?.status
  const draft = status === 'DRAFT'

  const actions = (
    <div className="flex items-center gap-2">
      <Button variant="ghost" icon={Download}
        onClick={() => api.downloadFramework(ref).catch((e) => setError(e.message))}>Download JSON</Button>
      {canAuthor(viewer) && <Button variant="ghost" icon={GitBranch} onClick={() => navigate(`/frameworks/${ref}/new-version`)}>New version from document</Button>}
      {draft && canAuthor(viewer) && <Button variant="ghost" icon={Send} onClick={() => act(() => api.submit(ref))}>Submit for review</Button>}
      {status === 'IN_REVIEW' && canApprove(viewer) && <Button icon={Check} onClick={() => act(() => api.approve(ref, 'approved via console'))}>Approve</Button>}
      {status === 'APPROVED' && canApprove(viewer) && <Button icon={UploadCloud} onClick={() => act(() => api.publish(ref))}>Publish &amp; sign</Button>}
    </div>
  )

  return (
    <Page
      title={
        <span className="flex items-center gap-3">
          <button onClick={() => navigate('/')} className="text-muted hover:text-text"><ArrowLeft size={20} /></button>
          {framework.name}
        </span>
      }
      subtitle={<span className="font-mono text-[12px]">{framework.id} · {framework.region} · {framework.license} · {framework.public ? 'public' : 'private'}</span>}
      action={actions}
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}

      <div className="flex items-center gap-3 mb-5">
        <span className="text-[13px] text-muted">Latest version</span>
        <span className="font-mono text-[13px] text-text">{latest?.version}</span>
        {status && <StatusBadge status={status} />}
        {latest?.contentHash && <span className="font-mono text-[11px] text-muted truncate">{latest.contentHash}</span>}
      </div>

      <LanguageBar refId={ref} editable={canAuthor(viewer)} aiConfigured={aiConfigured} />

      {draft && canAuthor(viewer) && <Stepper tab={tab} setTab={setTab} />}

      <Tabs
        active={tab}
        onChange={setTab}
        tabs={[
          { key: 'structure', label: `Structure (${countRequirements(structure)})` },
          { key: 'catalog', label: `Controls (${controls.length})` },
          { key: 'mappings', label: 'Cross-mappings' },
          { key: 'policies', label: `Policies (${policies.length})` },
          { key: 'overview', label: 'Overview' },
          ...(latestControls?.length ? [{ key: 'controls', label: `Legacy controls (${latestControls.length})` }] : []),
          { key: 'versions', label: 'Versions' },
        ]}
      />

      {tab === 'structure' && (
        <StructureTree refId={ref} structure={structure} editable={draft && canAuthor(viewer)} onChanged={load} />
      )}

      {tab === 'mappings' && (
        <AutoMapPanel refId={ref} frameworks={frameworks} editable={draft && canAuthor(viewer)}
          aiConfigured={aiConfigured} onChanged={load} />
      )}

      {tab === 'catalog' && (
        <CatalogControls refId={ref} controls={controls} editable={canAuthor(viewer)}
          itemCodes={allRequirementCodes(structure)} structure={structure} onChanged={load} />
      )}

      {tab === 'policies' && (
        <CatalogPolicies refId={ref} policies={policies} editable={canAuthor(viewer)}
          controlCodes={controls.map((c) => c.code)} onChanged={load} />
      )}

      {tab === 'overview' && (
        <Card icon={Info} title="Metadata">
          <dl className="grid grid-cols-2 md:grid-cols-3 gap-y-4 gap-x-6 text-[13px]">
            <Meta label="Reference" value={framework.id} mono />
            <Meta label="Region" value={framework.region} />
            <Meta label="Authority" value={framework.authority || '—'} />
            <Meta label="License" value={framework.license} />
            <Meta label="Public catalog" value={framework.public ? 'yes' : 'no'} />
            <Meta label="Latest status" value={<StatusBadge status={status} />} />
          </dl>
        </Card>
      )}

      {tab === 'controls' && (
        <div className="space-y-4">
          <Card icon={ListChecks} title={`Legacy controls (${latestControls?.length || 0})`}>
            {(!latestControls || latestControls.length === 0) ? (
              <EmptyState icon={ListChecks} title="No controls yet" hint={draft ? 'Add controls above, then submit for review.' : 'This version has no controls.'} />
            ) : (
              <Table>
                <THead><TR><TH>Ref</TH><TH>Name</TH><TH>Section</TH></TR></THead>
                <tbody>
                  {latestControls.map((c) => (
                    <TR key={c.refId}>
                      <TD className="font-mono text-[12px] text-text">{c.refId}</TD>
                      <TD className="text-text">{c.name}</TD>
                      <TD className="text-muted">{c.section || '—'}</TD>
                    </TR>
                  ))}
                </tbody>
              </Table>
            )}
          </Card>
        </div>
      )}

      {tab === 'versions' && (
        <Card icon={History} title="Version history">
          <Table>
            <THead><TR><TH>Version</TH><TH>Status</TH><TH>Content hash</TH><TH>Published</TH></TR></THead>
            <tbody>
              {versions.map((v) => (
                <TR key={v.id}>
                  <TD className="font-mono text-[12px] text-text">{v.version}</TD>
                  <TD><StatusBadge status={v.status} /></TD>
                  <TD className="font-mono text-[11px] text-muted">{v.contentHash || '—'}</TD>
                  <TD className="text-muted">{v.publishedAt ? new Date(v.publishedAt).toLocaleString() : '—'}</TD>
                </TR>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </Page>
  )
}

// Stepper — the step-by-step authoring rail shown on drafts. Each step maps to
// a tab; auditors move left to right: structure → mappings → controls &
// evidence → policies → review, then submit.
const STEPS = [
  { key: 'structure', label: '1 Structure' },
  { key: 'structure-map', tab: 'structure', label: '2 Mappings' },
  { key: 'catalog', label: '3 Controls & evidence' },
  { key: 'policies', label: '4 Policies' },
  { key: 'overview', label: '5 Review' },
]

function Stepper({ tab, setTab }) {
  return (
    <div className="flex flex-wrap items-center gap-1.5 mb-4">
      {STEPS.map((s, i) => {
        const target = s.tab || s.key
        const active = tab === target && (s.tab ? false : true)
        return (
          <span key={s.key} className="flex items-center gap-1.5">
            {i > 0 && <span className="text-subtle text-[11px]">→</span>}
            <button onClick={() => setTab(target)}
              className={`font-mono text-[11px] uppercase tracking-[0.06em] px-2 py-1 rounded-badge border transition-colors ${active ? 'text-sage border-sage' : 'text-muted border-border hover:text-text'}`}>
              {s.label}
            </button>
          </span>
        )
      })}
    </div>
  )
}

function allRequirementCodes(structure) {
  const codes = []
  for (const c of structure || []) for (const r of c.requirements) codes.push(r.code)
  return codes
}

function Meta({ label, value, mono }) {
  return (
    <div>
      <dt className="font-mono text-[10px] uppercase tracking-[0.08em] text-muted mb-1">{label}</dt>
      <dd className={mono ? 'font-mono text-[12px] text-text' : 'text-text'}>{value}</dd>
    </div>
  )
}

