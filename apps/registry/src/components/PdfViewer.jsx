// PdfViewer — renders the uploaded source PDF page by page with react-pdf, and
// highlights the passage a selected requirement was derived from.
//
// Which page to show comes from the page-aligned extracted text (docextract
// emits one form-feed-separated segment per PDF page), so page N of the text
// corresponds to page N of the PDF. The highlight itself is drawn in pdf.js's
// text layer via customTextRenderer.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Document, Page, pdfjs } from 'react-pdf'
import 'react-pdf/dist/Page/TextLayer.css'
import 'react-pdf/dist/Page/AnnotationLayer.css'
import { ChevronLeft, ChevronRight, Loader2 } from 'lucide-react'
import { normalize, needleVariants, pageContaining } from '../lib/textmatch.js'

// Vite resolves this to a hashed asset URL; pdf.js runs off the main thread.
pdfjs.GlobalWorkerOptions.workerSrc = new URL(
  'pdfjs-dist/build/pdf.worker.min.mjs',
  import.meta.url,
).toString()

const norm = normalize

// Module-level: a fresh object each render makes react-pdf reload the document.
const PDF_OPTIONS = {}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ))
}

// matchRun returns the indexes of the shortest contiguous run of text items
// whose joined text contains the excerpt — the actual source passage, rather
// than every item that happens to share a word with it.
const MAX_RUN = 120
function matchRun(items, excerpt) {
  if (!items?.length) return new Set()
  for (const needle of needleVariants(excerpt)) {
    const hit = runFor(items, needle)
    if (hit.size) return hit
  }
  return new Set()
}

function runFor(items, ne) {
  const strs = items.map((i) => normalize(i.str))
  let best = null
  for (let start = 0; start < strs.length; start++) {
    if (!strs[start]) continue
    let acc = ''
    for (let end = start; end < strs.length && end - start < MAX_RUN; end++) {
      // pdf.js emits whitespace-only items between words — they carry no text,
      // so skip them when accumulating (a separator is added for real pieces).
      const piece = strs[end]
      if (piece) acc = acc ? `${acc} ${piece}` : piece
      if (acc.length > ne.length * 2 + 40) break
      if (acc.includes(ne)) {
        const len = end - start
        if (!best || len < best.len) best = { start, end, len }
        break
      }
    }
  }
  if (!best) return new Set()
  const out = new Set()
  for (let k = best.start; k <= best.end; k++) out.add(k)
  return out
}

export default function PdfViewer({ file, pages, excerpt }) {
  const [numPages, setNumPages] = useState(0)
  const [page, setPage] = useState(1)
  const [width, setWidth] = useState(0)
  const [textItems, setTextItems] = useState([])
  const boxRef = useRef(null)

  // Track the container width so the page scales to the pane.
  useEffect(() => {
    const el = boxRef.current
    if (!el) return
    const ro = new ResizeObserver(([e]) => setWidth(Math.max(0, e.contentRect.width - 24)))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // Jump to the page whose extracted text contains the selected excerpt.
  useEffect(() => {
    if (!excerpt || !pages?.length) return
    const idx = pageContaining(pages, excerpt)
    if (idx >= 0) setPage(idx + 1)
  }, [excerpt, pages])

  // pdf.js splits a line into many small text items, so matching item-by-item
  // would highlight every stray occurrence of a common word. Instead find the
  // shortest CONTIGUOUS run of items whose joined text contains the excerpt, and
  // highlight exactly that run.
  const highlightIdx = useMemo(() => matchRun(textItems, excerpt), [textItems, excerpt])

  const customTextRenderer = useCallback(({ str, itemIndex }) => {
    if (!highlightIdx.has(itemIndex)) return escapeHtml(str)
    return `<mark style="background:rgba(251,191,36,.55);color:inherit;border-radius:2px">${escapeHtml(str)}</mark>`
  }, [highlightIdx])

  const total = numPages || pages?.length || 0

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between px-3 py-1.5 border-b border-border bg-inset sticky top-0 z-10">
        <button disabled={page <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}
          className="p-1 rounded text-muted hover:text-text disabled:opacity-30"><ChevronLeft size={15} /></button>
        <span className="text-[11.5px] text-muted font-mono">Page {page} / {total || '…'}</span>
        <button disabled={total > 0 && page >= total} onClick={() => setPage((p) => Math.min(total || p + 1, p + 1))}
          className="p-1 rounded text-muted hover:text-text disabled:opacity-30"><ChevronRight size={15} /></button>
      </div>

      <div ref={boxRef} className="flex-1 overflow-auto p-3 flex justify-center">
        <Document
          file={file}
          options={PDF_OPTIONS}
          onLoadSuccess={({ numPages: n }) => setNumPages(n)}
          loading={<div className="flex items-center gap-2 text-muted text-[12.5px] py-10"><Loader2 size={15} className="animate-spin" /> Loading document…</div>}
          error={<div className="text-[12.5px] text-st-red py-10">This PDF could not be rendered.</div>}
        >
          <Page
            pageNumber={page}
            width={width || undefined}
            customTextRenderer={customTextRenderer}
            onGetTextSuccess={({ items }) => setTextItems(items || [])}
            renderAnnotationLayer={false}
            loading={<div className="text-muted text-[12.5px] py-10">Rendering page…</div>}
          />
        </Document>
      </div>
    </div>
  )
}
