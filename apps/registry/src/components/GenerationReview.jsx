// GenerationReview — the split review surface for a generated framework, in the
// FLAT meizon-framework/v2 shape the pipeline now produces:
//
//   categories[]   { ref, name }
//   requirements[] { ref, category, name, description, controls[] }
//   controls[]     { ref, name, description, category }
//
// Left: everything editable in place, requirements grouped under their category.
// Right: the source document (real PDF when one was uploaded, else paginated
// text) with the passage a selected requirement came from highlighted.
// Provenance is keyed by requirement ref; diff keys are "req:<ref>".
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import {
  ChevronDown, ChevronRight, Plus, Trash2, ArrowUp, ArrowDown, FileText, ChevronLeft, Wrench,
} from 'lucide-react'
import { Badge } from './ui.jsx'
import PdfViewer from './PdfViewer.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { normalize, pageContaining, locateSpan } from '../lib/textmatch.js'

const CONTROL_CATEGORIES = [
  'Policy', 'Access', 'Logging', 'Encryption', 'Network', 'Endpoint', 'Vulnerability',
  'Incident', 'Vendor', 'HR', 'Physical', 'Governance', 'Monitoring', 'Backup', 'Other',
]

const CHANGE_TONE = { added: 'green', modified: 'amber', removed: 'red', unchanged: 'grey' }
function ChangeBadge({ kind }) {
  if (!kind) return null
  return <Badge tone={CHANGE_TONE[kind] || 'grey'}>{kind}</Badge>
}

// ---- source pane (text fallback) -------------------------------------------
const norm = normalize
const locate = locateSpan

function paginate(text) {
  if (!text) return ['']
  if (text.includes('\f')) return text.split('\f')
  const lines = text.split('\n')
  const PER = 48
  if (lines.length <= PER) return [text]
  const pages = []
  for (let i = 0; i < lines.length; i += PER) pages.push(lines.slice(i, i + PER).join('\n'))
  return pages
}

function TextPane({ text, excerpt }) {
  const pages = useMemo(() => paginate(text), [text])
  const [page, setPage] = useState(0)
  const markRef = useRef(null)

  useEffect(() => {
    if (!excerpt) return
    const idx = pageContaining(pages, excerpt)
    if (idx >= 0) setPage(idx)
  }, [excerpt, pages])

  useLayoutEffect(() => {
    if (markRef.current) markRef.current.scrollIntoView({ block: 'center' })
  }, [excerpt, page])

  if (!text) return <div className="text-[12.5px] text-muted p-4">No source document to preview.</div>

  const cur = pages[Math.min(page, pages.length - 1)] || ''
  const span = locate(cur, excerpt)
  const missing = excerpt && pageContaining(pages, excerpt) < 0

  return (
    <div className="flex flex-col h-full">
      {pages.length > 1 && (
        <div className="flex items-center justify-between px-3 py-1.5 border-b border-border bg-inset sticky top-0 z-10">
          <button disabled={page === 0} onClick={() => setPage((p) => Math.max(0, p - 1))}
            className="p-1 rounded text-muted hover:text-text disabled:opacity-30"><ChevronLeft size={15} /></button>
          <span className="text-[11.5px] text-muted font-mono">Page {page + 1} / {pages.length}</span>
          <button disabled={page >= pages.length - 1} onClick={() => setPage((p) => Math.min(pages.length - 1, p + 1))}
            className="p-1 rounded text-muted hover:text-text disabled:opacity-30"><ChevronRight size={15} /></button>
        </div>
      )}
      <div className="overflow-y-auto p-4 flex-1">
        {missing && <div className="text-[11.5px] text-muted mb-2 italic">Source span for this item couldn’t be located.</div>}
        <pre className="whitespace-pre-wrap font-mono text-[12.5px] leading-relaxed text-text2">
          {span ? (<>
            {cur.slice(0, span[0])}
            <mark ref={markRef} className="bg-amber-200 text-black rounded px-0.5">{cur.slice(span[0], span[1])}</mark>
            {cur.slice(span[1])}
          </>) : cur}
        </pre>
      </div>
    </div>
  )
}

// ---- editable fields --------------------------------------------------------
function GrowText({ value, onChange, placeholder, className }) {
  const ref = useRef(null)
  const autosize = useCallback(() => {
    const el = ref.current
    if (el) { el.style.height = 'auto'; el.style.height = `${el.scrollHeight}px` }
  }, [])
  useLayoutEffect(() => { autosize() }, [value, autosize])
  // Re-measure on width change: a field measured while its column is still
  // collapsed keeps a wildly wrong height otherwise.
  useEffect(() => {
    const el = ref.current
    if (!el) return
    let last = el.clientWidth
    const ro = new ResizeObserver(() => {
      if (el.clientWidth !== last) { last = el.clientWidth; autosize() }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [autosize])

  return (
    <textarea ref={ref} rows={1} value={value || ''} placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)} onClick={(e) => e.stopPropagation()}
      className={`bg-transparent border border-transparent hover:border-border focus:border-sage rounded px-1.5 py-0.5 outline-none w-full flex-1 min-w-0 resize-none overflow-hidden leading-snug ${className || ''}`} />
  )
}

function CodeField({ value, onChange, className }) {
  return (
    <input value={value || ''} onChange={(e) => onChange(e.target.value)} onClick={(e) => e.stopPropagation()}
      className={`bg-transparent border border-transparent hover:border-border focus:border-sage rounded px-1 py-0.5 outline-none font-mono shrink-0 ${className || ''}`} />
  )
}

function RowActions({ onBefore, onAfter, onDelete }) {
  const btn = 'p-1 rounded text-muted hover:text-text hover:bg-inset'
  return (
    <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0"
      onClick={(e) => e.stopPropagation()}>
      {onBefore && <button className={btn} title="Add before" onClick={onBefore}><ArrowUp size={13} /></button>}
      {onAfter && <button className={btn} title="Add after" onClick={onAfter}><ArrowDown size={13} /></button>}
      {onDelete && <button className={`${btn} hover:text-st-red`} title="Delete" onClick={onDelete}><Trash2 size={13} /></button>}
    </div>
  )
}

// ---- main -------------------------------------------------------------------
export default function GenerationReview({
  doc, setDoc, documentText, sourceFile, provenance = {}, diff = null, duplicates = [], active, setActive,
}) {
  const activeExcerpt = active ? provenance[active] : ''
  const isPdf = Boolean(sourceFile) && /pdf$/i.test(sourceFile.name || '')
  const textPages = useMemo(() => paginate(documentText), [documentText])

  const mutate = (fn) => { const next = structuredClone(doc); fn(next); setDoc(next) }

  const categories = doc.categories || []
  const requirements = doc.requirements || []
  const controls = doc.controls || []
  // Duplicates are allowed — some standards repeat wording verbatim — so they
  // are marked here rather than rejected. Each affected requirement is told
  // which OTHER refs it matches, because "this is a duplicate" is not
  // actionable without knowing a duplicate of what.
  const duplicateOf = useMemo(() => {
    const map = {}
    for (const g of duplicates || []) {
      for (const ref of g.refs || []) {
        map[ref] = (g.refs || []).filter((other) => other !== ref)
      }
    }
    return map
  }, [duplicates])

  const controlByRef = useMemo(
    () => Object.fromEntries(controls.map((c) => [c.ref, c])), [controls])

  // Requirements grouped under their category, preserving document order.
  const groups = useMemo(() => {
    const out = categories.map((c) => ({ cat: c, reqs: [] }))
    const index = Object.fromEntries(categories.map((c, i) => [c.ref, i]))
    const loose = { cat: null, reqs: [] }
    requirements.forEach((r, ri) => {
      const g = r.category != null && index[r.category] !== undefined ? out[index[r.category]] : loose
      g.reqs.push({ r, ri })
    })
    if (loose.reqs.length) out.push(loose)
    return out
  }, [categories, requirements])

  const blankReq = () => ({ ref: 'NEW', category: '', name: '', description: '', controls: [] })

  return (
    <div className="grid grid-cols-[minmax(0,3fr)_minmax(0,2fr)] gap-0 border border-border rounded-lg overflow-hidden flex-1 min-h-0">
      {/* LEFT — editable flat framework */}
      <div className="overflow-y-auto border-r border-border bg-surface">
        <div className="flex items-center justify-between px-3 py-2 border-b border-border sticky top-0 bg-surface z-10">
          <span className="text-[11px] uppercase tracking-wide text-muted">
            {requirements.length} requirements · {controls.length} controls
          </span>
          <button className="flex items-center gap-1 text-[12px] text-sage hover:underline"
            onClick={() => mutate((d) => { (d.categories ||= []).push({ ref: 'NEW', name: '' }) })}>
            <Plus size={13} /> Category
          </button>
        </div>

        <div className="p-2 space-y-3">
          {groups.map((g, gi) => (
            <div key={g.cat ? g.cat.ref + gi : 'ungrouped'}>
              {/* category header */}
              {g.cat ? (
                <div className="group flex items-start gap-1 px-1.5 py-1 rounded hover:bg-inset">
                  <CodeField value={g.cat.ref} className="w-20 text-[12px] text-sage mt-0.5"
                    onChange={(v) => mutate((d) => {
                      const old = d.categories[gi].ref
                      d.categories[gi].ref = v
                      d.requirements.forEach((r) => { if (r.category === old) r.category = v })
                    })} />
                  <GrowText value={g.cat.name} placeholder="Category name"
                    className="text-[13px] text-text font-medium"
                    onChange={(v) => mutate((d) => { d.categories[gi].name = v })} />
                  {diff && <div className="mt-1"><ChangeBadge kind={diff[`cat:${g.cat.ref}`]} /></div>}
                  <RowActions onDelete={() => mutate((d) => {
                    const ref = d.categories[gi].ref
                    d.categories.splice(gi, 1)
                    d.requirements.forEach((r) => { if (r.category === ref) r.category = '' })
                  })} />
                </div>
              ) : (
                <div className="px-1.5 py-1 text-[12px] text-muted italic">Ungrouped</div>
              )}

              {/* requirements in this category */}
              <div className="ml-4 border-l border-border pl-2 space-y-1.5 mt-1">
                {g.reqs.map(({ r, ri }) => {
                  const selected = active === r.ref
                  const dupes = duplicateOf[r.ref]
                  return (
                    <div key={ri}
                      className={`group rounded px-1.5 py-1 cursor-pointer ${selected ? 'bg-sage/15' : 'hover:bg-inset'} ${dupes ? 'border-l-2 border-st-amber -ml-[2px] pl-2' : ''}`}
                      onClick={() => setActive(r.ref)}>
                      <div className="flex items-start gap-1">
                        <CodeField value={r.ref} className="w-24 text-[11.5px] text-muted mt-0.5"
                          onChange={(v) => mutate((d) => { d.requirements[ri].ref = v })} />
                        <GrowText value={r.name} placeholder="Requirement name"
                          className="text-[12.5px] text-text2"
                          onChange={(v) => mutate((d) => { d.requirements[ri].name = v })} />
                        {diff && <div className="mt-1"><ChangeBadge kind={diff[`req:${r.ref}`]} /></div>}
                        {dupes && (
                          <span className="mt-0.5 shrink-0 font-mono text-[10px] uppercase tracking-[0.06em] px-1.5 py-0.5 rounded-badge border border-st-amber/50 text-st-amber"
                            title={`Identical description to ${dupes.join(', ')}`}>
                            dup
                          </span>
                        )}
                        <RowActions
                          onBefore={() => mutate((d) => d.requirements.splice(ri, 0, { ...blankReq(), category: r.category }))}
                          onAfter={() => mutate((d) => d.requirements.splice(ri + 1, 0, { ...blankReq(), category: r.category }))}
                          onDelete={() => mutate((d) => d.requirements.splice(ri, 1))} />
                      </div>

                      {r.description !== undefined && (
                        <div className="ml-[6.4rem]">
                          <GrowText value={r.description} placeholder="Description…"
                            className="text-[11.5px] text-muted"
                            onChange={(v) => mutate((d) => { d.requirements[ri].description = v })} />
                        </div>
                      )}

                      {/* linked controls */}
                      {(r.controls || []).length > 0 && (
                        <div className="ml-[6.4rem] flex flex-wrap gap-1 mt-1">
                          {r.controls.map((ref) => (
                            <span key={ref} title={controlByRef[ref]?.name || ref}
                              className="inline-flex items-center gap-1 bg-inset border border-border rounded-badge px-1.5 py-0.5 text-[10.5px] font-mono text-muted">
                              <Wrench size={10} /> {ref}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })}
                {g.reqs.length === 0 && (
                  <button className="text-[11.5px] text-muted hover:text-sage flex items-center gap-1 pl-1"
                    onClick={() => mutate((d) => d.requirements.push({ ...blankReq(), category: g.cat?.ref || '' }))}>
                    <Plus size={12} /> requirement
                  </button>
                )}
              </div>
            </div>
          ))}

          {/* controls library */}
          <div className="pt-2 mt-2 border-t border-border">
            <div className="flex items-center justify-between px-1.5 pb-1">
              <span className="text-[11px] uppercase tracking-wide text-muted">Suggested controls</span>
              <button className="flex items-center gap-1 text-[12px] text-sage hover:underline"
                onClick={() => mutate((d) => { (d.controls ||= []).push({ ref: 'new-control', name: '', description: '', category: 'Other' }) })}>
                <Plus size={13} /> Control
              </button>
            </div>
            <div className="space-y-1.5">
              {controls.map((c, ci) => (
                <div key={ci} className="group rounded px-1.5 py-1 hover:bg-inset">
                  <div className="flex items-start gap-1">
                    <CodeField value={c.ref} className="w-40 text-[11px] text-sage mt-0.5"
                      onChange={(v) => mutate((d) => {
                        const old = d.controls[ci].ref
                        d.controls[ci].ref = v
                        d.requirements.forEach((r) => {
                          r.controls = (r.controls || []).map((x) => (x === old ? v : x))
                        })
                      })} />
                    <GrowText value={c.name} placeholder="Control name" className="text-[12px] text-text2"
                      onChange={(v) => mutate((d) => { d.controls[ci].name = v })} />
                    <select value={c.category || 'Other'} onClick={(e) => e.stopPropagation()}
                      onChange={(e) => mutate((d) => { d.controls[ci].category = e.target.value })}
                      className="bg-transparent text-[10.5px] text-muted border border-transparent hover:border-border rounded shrink-0 mt-0.5">
                      {CONTROL_CATEGORIES.map((k) => <option key={k} value={k}>{k}</option>)}
                    </select>
                    <RowActions onDelete={() => mutate((d) => {
                      const ref = d.controls[ci].ref
                      d.controls.splice(ci, 1)
                      d.requirements.forEach((r) => {
                        r.controls = (r.controls || []).filter((x) => x !== ref)
                      })
                    })} />
                  </div>
                  {c.description ? (
                    <div className="ml-[10.6rem]">
                      <GrowText value={c.description} placeholder="Description…" className="text-[11px] text-muted"
                        onChange={(v) => mutate((d) => { d.controls[ci].description = v })} />
                    </div>
                  ) : null}
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* RIGHT — source document */}
      <div className="bg-inset flex flex-col min-h-0">
        <div className="flex items-center gap-1.5 px-3 py-2 border-b border-border bg-inset">
          <FileText size={13} className="text-muted" />
          <span className="text-[11px] uppercase tracking-wide text-muted">Source document</span>
        </div>
        <div className="flex-1 min-h-0">
          {isPdf ? (
            // If the PDF cannot render, fall back to the extracted text rather
            // than taking the whole review page down with it.
            <ErrorBoundary fallback={<TextPane text={documentText} excerpt={activeExcerpt} />}>
              <PdfViewer file={sourceFile} pages={textPages} excerpt={activeExcerpt} />
            </ErrorBoundary>
          ) : (
            <TextPane text={documentText} excerpt={activeExcerpt} />
          )}
        </div>
      </div>
    </div>
  )
}
