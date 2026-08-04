// Jobs — what the platform is working on, and what it finished.
//
// Generation, OCR, translation and auto-mapping all run for minutes. Before
// this page they were only visible to the tab that started them: navigate away
// and the run was still going, but you had no way to find it. Now every run is
// recorded, so progress is answerable from anywhere.
import { useEffect, useState, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { Activity, Check, Loader2, CircleAlert, ArrowUpRight } from 'lucide-react'
import { api } from '../lib/api.js'
import { Page, Card, Table, THead, TR, TH, TD, Badge, EmptyState, Button } from '../components/ui.jsx'

const KIND_LABEL = {
  generate: 'Generate framework',
  next_version: 'New version',
  translate: 'Translate',
  automap: 'Cross-map',
  qa_template: 'Audit template',
}

// Steps that count work report a unit, so "12 / 106" reads as pages rather
// than an unexplained number.
const STEP_UNIT = { ocr: 'pages', chunk: 'chunks', extract: 'chunks', controls: 'batches', translate: 'batches', adjudicate: 'batches', retrieve: 'nodes', questions: 'requirements' }

function elapsed(from, to) {
  const ms = new Date(to) - new Date(from)
  if (!Number.isFinite(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  return `${Math.floor(s / 60)}m ${s % 60}s`
}

export default function Jobs() {
  const navigate = useNavigate()
  const [jobs, setJobs] = useState(null)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try { setJobs(await api.listJobs()) } catch (e) { setError(e.message) }
  }, [])
  useEffect(() => { load() }, [load])

  // Keep refreshing while anything is running; stop once everything settles so
  // an idle tab is not polling forever.
  useEffect(() => {
    if (!jobs?.some((j) => j.status === 'running')) return undefined
    const t = setInterval(load, 2000)
    return () => clearInterval(t)
  }, [jobs, load])

  const running = (jobs || []).filter((j) => j.status === 'running')
  const awaiting = (jobs || []).filter(
    (j) => j.status === 'done' && (j.kind === 'generate' || j.kind === 'next_version'))

  return (
    <Page
      title="Jobs"
      subtitle="Document generation, OCR, translation and cross-mapping run in the background. They keep going if you close this page."
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}

      {awaiting.length > 0 && (
        <div className="flex items-start gap-2 text-[12.5px] text-text2 bg-st-amber/10 border border-st-amber/30 rounded-md px-3 py-2 mb-4">
          <Check size={14} className="text-st-amber shrink-0 mt-0.5" />
          <span>
            <span className="font-medium">{awaiting.length} finished run(s) are waiting for review.</span>{' '}
            A generated framework is a proposal until someone accepts it — it will not appear
            under Frameworks until then.
          </span>
        </div>
      )}

      <Card icon={Activity} title={running.length ? `Running (${running.length})` : 'Recent runs'}>
        {jobs && jobs.length === 0 ? (
          <EmptyState icon={Activity} title="Nothing has run yet"
            hint="Generate a framework from a document and it will appear here." />
        ) : (
          <Table>
            <THead>
              <TR><TH></TH><TH>Kind</TH><TH>Subject</TH><TH>Progress</TH><TH>Started</TH><TH>Took</TH><TH></TH></TR>
            </THead>
            <tbody>
              {(jobs || []).map((j) => {
                const unit = STEP_UNIT[j.step] ? ` ${STEP_UNIT[j.step]}` : ''
                return (
                  <TR key={j.id}
                    onClick={() => j.frameworkRef && navigate(`/frameworks/${j.frameworkRef}`)}>
                    <TD>
                      {j.status === 'running' && <Loader2 size={15} className="text-sage animate-spin" />}
                      {j.status === 'done' && <Check size={15} className="text-sage" />}
                      {j.status === 'error' && <CircleAlert size={15} className="text-st-red" />}
                    </TD>
                    <TD className="text-text">{KIND_LABEL[j.kind] || j.kind}</TD>
                    <TD className="text-[12.5px]">
                      <div className="text-text truncate max-w-[220px]">{j.label || j.frameworkRef || '—'}</div>
                      {j.error && (
                        <div className="text-st-red truncate max-w-[320px]" title={j.error}>{j.error}</div>
                      )}
                    </TD>
                    <TD className="text-[12.5px]">
                      {j.status === 'running' ? (
                        <div className="min-w-[150px]">
                          <div className="text-muted font-mono text-[11.5px]">
                            {j.step}{j.total > 0 ? ` · ${j.current}/${j.total}${unit}` : ''}
                          </div>
                          {j.total > 0 && (
                            <div className="h-1 bg-inset rounded mt-1 overflow-hidden">
                              <div className="h-full bg-sage transition-[width] duration-300"
                                style={{ width: `${Math.round((Math.min(j.current, j.total) / j.total) * 100)}%` }} />
                            </div>
                          )}
                        </div>
                      ) : (
                        <Badge tone={j.status === 'done' ? 'green' : 'red'}>{j.status}</Badge>
                      )}
                    </TD>
                    <TD className="text-muted text-[12.5px] whitespace-nowrap">
                      {new Date(j.createdAt).toLocaleString()}
                    </TD>
                    <TD className="text-muted font-mono text-[11.5px]">
                      {elapsed(j.createdAt, j.updatedAt)}
                    </TD>
                    <TD className="text-right">
                      {/* A finished generation is a PROPOSAL, not a framework:
                          it exists only once someone reviews and accepts it.
                          Without this link the result was unreachable. */}
                      {j.status === 'done' && (j.kind === 'generate' || j.kind === 'next_version') && (
                        <Button size="sm" variant="ghost" icon={ArrowUpRight}
                          onClick={(e) => {
                            e.stopPropagation()
                            navigate(j.kind === 'next_version'
                              ? `/frameworks/${j.frameworkRef}/new-version?job=${encodeURIComponent(j.id)}`
                              : `/frameworks/generate?job=${encodeURIComponent(j.id)}`)
                          }}>
                          Review &amp; accept
                        </Button>
                      )}
                    </TD>
                  </TR>
                )
              })}
            </tbody>
          </Table>
        )}
      </Card>
    </Page>
  )
}
