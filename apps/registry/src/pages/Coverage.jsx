import { useEffect, useState, useCallback } from 'react'
import { GitCompareArrows, RefreshCw, Sparkles, ShieldCheck, Check } from 'lucide-react'
import { api } from '../lib/api.js'
import { useSession } from '../context/SessionContext.jsx'
import {
  Page, Card, Stat, Button, Badge, Table, THead, TR, TH, TD, Field, Select, EmptyState,
} from '../components/ui.jsx'
import IngestProgress from '../components/IngestProgress.jsx'
import MappingTable from '../components/MappingTable.jsx'

const AUTOMAP_STEPS = [
  { key: 'plan', label: 'Plan' },
  { key: 'retrieve', label: 'Shortlist candidates' },
  { key: 'adjudicate', label: 'Adjudicate' },
  { key: 'record', label: 'Record proposals' },
]

// Only mappings this confident can be accepted in bulk; anything less deserves
// someone reading the reason before it becomes a compliance claim.
const BULK_ACCEPT_FLOOR = 0.8

const RELATION_TONE = { equivalent: 'green', partial: 'amber', superset: 'blue', subset: 'blue' }

export default function Coverage() {
  const { viewer } = useSession()
  const isSuper = viewer?.role === 'superadmin'

  const [frameworks, setFrameworks] = useState([])
  const [source, setSource] = useState('')
  const [target, setTarget] = useState('')
  const [report, setReport] = useState(null)
  const [unresolved, setUnresolved] = useState(null)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [mapping, setMapping] = useState(null)   // running job id
  const [mapStatus, setMapStatus] = useState(null)
  const [proposals, setProposals] = useState([])
  const [mappings, setMappings] = useState([])
  const [sourceDraft, setSourceDraft] = useState(false)

  useEffect(() => {
    api.listFrameworks().then((fs) => {
      setFrameworks(fs)
      if (fs.length > 0) setSource(fs[0].id)
    }).catch((e) => setError(e.message))
  }, [])

  // Mappings are editable only on a DRAFT: their codes live inside the signed
  // bundle, so changing one after publication would break its signature.
  useEffect(() => {
    const f = frameworks.find((x) => x.id === source)
    setSourceDraft((f?.latestStatus || '') === 'DRAFT')
  }, [frameworks, source])

  const loadUnresolved = useCallback(() => {
    if (!isSuper) return
    api.adminUnresolvedMappings().then(setUnresolved).catch(() => {})
  }, [isSuper])
  useEffect(() => { loadUnresolved() }, [loadUnresolved])

  // The actual pairs, not just per-relation counts: "7 partial mappings" is
  // not something an auditor can check.
  const loadMappings = useCallback(async (src, tgt) => {
    if (!src || !tgt) { setMappings([]); return }
    try { setMappings(await api.mappingsBetween(src, tgt)) } catch { setMappings([]) }
  }, [])

  const loadProposals = useCallback(async (ref) => {
    if (!ref) { setProposals([]); return }
    try {
      const r = await api.mappingProposals(ref)
      setProposals((r.proposals || []).filter((p) => p.status === 'pending'))
    } catch { setProposals([]) }
  }, [])

  useEffect(() => {
    if (!source) return
    setError('')
    api.coverage(source, target).then(setReport).catch((e) => { setReport(null); setError(e.message) })
    loadProposals(source)
    loadMappings(source, target)
  }, [source, target, loadProposals, loadMappings])

  // Poll while a mapping run is in flight.
  useEffect(() => {
    if (!mapping) return undefined
    const t = setInterval(async () => {
      try {
        const st = await api.generateStatus(mapping)
        setMapStatus(st)
        if (st.status === 'done') {
          setMapping(null); setMapStatus(null)
          await loadProposals(source)
          await loadMappings(source, target)
          setNotice('Mapping finished — review the proposals below.')
        }
        if (st.status === 'error') {
          setMapping(null); setMapStatus(null); setError(st.error || 'mapping failed')
        }
      } catch (e) { setMapping(null); setMapStatus(null); setError(e.message) }
    }, 1500)
    return () => clearInterval(t)
  }, [mapping, source, loadProposals])

  async function autoMap(nodeKind) {
    if (!target) { setError('Choose a target framework to map against.'); return }
    setError(''); setNotice('')
    try {
      const { jobId } = await api.autoMap(source, { target, nodeKind })
      setMapping(jobId)
    } catch (e) { setError(e.message) }
  }

  async function acceptProposals(ids) {
    if (!ids.length) return
    setBusy(true); setError('')
    try {
      const res = await api.decideProposals(source, ids, false)
      setNotice(`saved ${res.decided} mapping(s)`)
      await loadProposals(source)
      await loadMappings(source, target)
      setReport(await api.coverage(source, target))
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  async function resolveNow() {
    setBusy(true); setNotice(''); setError('')
    try {
      const res = await api.adminResolveMappings()
      setNotice(`resolved ${res.resolved} stub(s)`)
      loadUnresolved()
      if (source) setReport(await api.coverage(source, target))
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  const sum = (rows, key) => (rows || []).reduce((n, r) => n + r[key], 0)
  const totalMapped = sum(report?.rows, 'total') + sum(report?.controlRows, 'total')
  const totalResolved = sum(report?.rows, 'resolved') + sum(report?.controlRows, 'resolved')

  return (
    <Page
      title="Cross-mapping coverage"
      subtitle="How one framework's items map onto other frameworks. Stubs (targets not yet loaded) resolve automatically when the target framework publishes."
      action={
        <div className="flex items-center gap-2">
          <Button icon={ShieldCheck} disabled={!target || !!mapping} onClick={() => autoMap('control')}>
            {mapping ? 'Mapping…' : 'Auto-map controls'}
          </Button>
          <Button variant="ghost" icon={Sparkles} disabled={!target || !!mapping} onClick={() => autoMap('requirement')}>
            Requirements
          </Button>
          {isSuper && <Button variant="ghost" icon={RefreshCw} busy={busy} onClick={resolveNow}>Resolve stubs now</Button>}
        </div>
      }
    >
      {notice && <div className="text-[12.5px] mb-3" style={{ color: 'var(--b-green)' }}>{notice}</div>}
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}

      <div className="grid grid-cols-2 gap-4 max-w-xl mb-6">
        <Field label="Source framework">
          <Select value={source} onChange={(e) => setSource(e.target.value)}>
            {frameworks.map((f) => <option key={f.id} value={f.id}>{f.id}</option>)}
          </Select>
        </Field>
        <Field label="Target framework (optional)">
          <Select value={target} onChange={(e) => setTarget(e.target.value)}>
            <option value="">all targets</option>
            {frameworks.filter((f) => f.id !== source).map((f) => <option key={f.id} value={f.id}>{f.id}</option>)}
          </Select>
        </Field>
      </div>

      {mapStatus && (
        <Card icon={Sparkles} title="Mapping in progress" className="mb-4">
          <IngestProgress steps={AUTOMAP_STEPS} status={mapStatus}
            caption="Shortlisting candidates in English, then judging each pair…" />
        </Card>
      )}

      {proposals.length > 0 && (
        <Card icon={Check} title={`Proposed mappings awaiting review (${proposals.length})`} className="mb-4"
          action={
            <div className="flex items-center gap-2">
              <Button size="sm" variant="ghost" busy={busy}
                onClick={() => acceptProposals(proposals.filter((p) => p.confidence >= BULK_ACCEPT_FLOOR).map((p) => p.id))}>
                Save {proposals.filter((p) => p.confidence >= BULK_ACCEPT_FLOOR).length} confident
              </Button>
              <Button size="sm" busy={busy} onClick={() => acceptProposals(proposals.map((p) => p.id))}>
                Save all {proposals.length}
              </Button>
            </div>
          }>
          {/* Saved only on a decision: an LLM mapping accepted unread becomes a
              compliance claim nobody checked. Least confident first, so the
              doubtful ones get the attention. */}
          <p className="text-[12.5px] text-muted mb-3">
            Least confident first. These are not mappings until saved.
          </p>
          <Table>
            <THead><TR><TH>Type</TH><TH>Source</TH><TH>Target</TH><TH>Relation</TH><TH>Confidence</TH><TH>Why</TH></TR></THead>
            <tbody>
              {proposals.slice(0, 25).map((p) => (
                <TR key={p.id}>
                  <TD className="text-muted text-[12px]">{p.nodeKind}</TD>
                  <TD>
                    <div className="font-mono text-[11.5px] text-sage">{p.sourceRef}</div>
                    <div className="text-[12px] text-text2 truncate max-w-[200px]">{p.sourceName}</div>
                  </TD>
                  <TD>
                    <div className="font-mono text-[11.5px] text-text">{p.target} · {p.targetRef}</div>
                    <div className="text-[12px] text-text2 truncate max-w-[200px]">{p.targetName}</div>
                  </TD>
                  <TD><Badge tone={RELATION_TONE[p.relation] || 'grey'}>{p.relation}</Badge></TD>
                  <TD className="tabular-nums text-[12.5px]">{p.confidence.toFixed(2)}</TD>
                  <TD className="text-[12px] text-muted max-w-[280px]">{p.rationale}</TD>
                </TR>
              ))}
            </tbody>
          </Table>
        </Card>
      )}

      {source && target && (
        <MappingTable sourceRef={source} targetRef={target} rows={mappings}
          editable={sourceDraft}
          onChanged={async () => {
            await loadMappings(source, target)
            setReport(await api.coverage(source, target))
          }} />
      )}

      {report && (
        <>
          <div className="grid grid-cols-2 md:grid-cols-3 gap-4 mb-6">
            <Stat value={report.totalItems} label="Items in source" caption={`${report.sourceFramework}@${report.sourceVersion}`} />
            <Stat value={totalMapped} label="Mappings" />
            <Stat value={totalMapped - totalResolved} label="Unresolved stubs" tone={totalMapped - totalResolved > 0 ? 'amber' : 'green'} />
          </div>

          {/* Requirements and controls are mapped separately, so they are
              reported separately — a single merged number hides which half of
              the framework is actually covered. */}
          <div className="grid md:grid-cols-2 gap-4">
            <CoverageTable icon={GitCompareArrows}
              title={`Requirements — ${report.sourceFramework}${target ? ` → ${target}` : ''}`}
              rows={report.rows}
              empty="No requirement mappings yet. Use Auto-map above, or add them on a requirement." />
            <CoverageTable icon={ShieldCheck}
              title={`Controls — ${report.sourceFramework}${target ? ` → ${target}` : ''}`}
              rows={report.controlRows}
              empty="No control mappings yet. Use Auto-map controls above." />
          </div>
        </>
      )}

      {isSuper && unresolved && (
        <Card icon={RefreshCw} title="Unresolved stubs by target framework" className="mt-4">
          {unresolved.length === 0 ? (
            <EmptyState icon={RefreshCw} title="Everything is resolved" />
          ) : (
            <Table>
              <THead><TR><TH>Target framework</TH><TH>Pinned version</TH><TH>Stubs</TH></TR></THead>
              <tbody>
                {unresolved.map((u, i) => (
                  <TR key={i}>
                    <TD className="font-mono text-[12px] text-text">{u.targetFrameworkCode}</TD>
                    <TD className="text-muted">{u.targetFrameworkVersion || 'any'}</TD>
                    <TD className="tabular-nums">{u.count}</TD>
                  </TR>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      )}
    </Page>
  )
}

// CoverageTable renders one level's per-relation counts.
function CoverageTable({ icon, title, rows, empty }) {
  return (
    <Card icon={icon} title={title}>
      {!rows || rows.length === 0 ? (
        <EmptyState icon={icon} title="No mappings" hint={empty} />
      ) : (
        <Table>
          <THead><TR><TH>Relation</TH><TH>Mappings</TH><TH>Resolved</TH><TH>Stubs</TH></TR></THead>
          <tbody>
            {rows.map((r) => (
              <TR key={r.relation}>
                <TD><Badge tone={RELATION_TONE[r.relation] || 'grey'}>{r.relation}</Badge></TD>
                <TD className="tabular-nums">{r.total}</TD>
                <TD className="tabular-nums" style={{ color: 'var(--b-green)' }}>{r.resolved}</TD>
                <TD className="tabular-nums">{r.total - r.resolved}</TD>
              </TR>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  )
}
