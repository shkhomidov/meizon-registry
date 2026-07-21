// SourcePreview — read the document a framework was generated from, without
// leaving the list.
//
// The point is auditability: a published framework asserts what a standard
// requires, and the only way to check that is against the document it came
// from. Downloading to check would mean leaving the page, so the file opens
// here and downloads only if you want a copy.
import { useEffect, useState } from 'react'
import { FileText, Download, X } from 'lucide-react'
import { api } from '../lib/api.js'
import PdfViewer from './PdfViewer.jsx'
import ErrorBoundary from './ErrorBoundary.jsx'
import { Button, Badge } from './ui.jsx'

function humanSize(bytes) {
  if (!bytes) return ''
  const mb = bytes / (1024 * 1024)
  if (mb >= 1) return `${mb.toFixed(1)} MB`
  return `${Math.max(1, Math.round(bytes / 1024))} KB`
}

export default function SourcePreview({ refId, source, onClose }) {
  const [file, setFile] = useState(null)
  const [error, setError] = useState('')

  // react-pdf needs the bytes, and the endpoint is cookie-authenticated — so
  // the file is fetched rather than handed to an <iframe src>.
  useEffect(() => {
    let revoked = null
    let cancelled = false
    api.sourceDocumentBlob(refId)
      .then((blob) => {
        if (cancelled) return
        revoked = URL.createObjectURL(blob)
        setFile(revoked)
      })
      .catch((e) => { if (!cancelled) setError(e.message) })
    return () => {
      cancelled = true
      if (revoked) URL.revokeObjectURL(revoked)
    }
  }, [refId])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={onClose}>
      <div className="bg-card border border-border rounded-card w-full max-w-4xl h-[88vh] flex flex-col"
        onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-3 px-4 py-3 border-b border-border shrink-0">
          <FileText size={16} className="text-sage shrink-0" />
          <div className="min-w-0">
            <div className="text-[13px] text-text truncate">{source.filename}</div>
            <div className="font-mono text-[11px] text-muted">
              {humanSize(source.byteSize)}
              {source.pageCount ? ` · ${source.pageCount} page(s)` : ''}
              {source.ocrPages ? ` · ${source.ocrPages} read by OCR` : ''}
              {source.uploadedAt ? ` · ${new Date(source.uploadedAt).toLocaleString()}` : ''}
            </div>
          </div>
          {source.ocrPages > 0 && <Badge tone="amber">OCR</Badge>}
          <div className="ml-auto flex items-center gap-2 shrink-0">
            <Button size="sm" variant="ghost" icon={Download}
              onClick={() => api.downloadSourceDocument(refId).catch((e) => setError(e.message))}>
              Download
            </Button>
            <button onClick={onClose} className="p-1 rounded text-muted hover:text-text hover:bg-inset">
              <X size={16} />
            </button>
          </div>
        </div>

        <div className="flex-1 min-h-0 overflow-auto p-4">
          {error && <div className="text-[12.5px] text-st-red">{error}</div>}
          {!error && !file && <div className="text-[13px] text-muted">Loading the document…</div>}
          {file && (
            // A malformed PDF must not take the whole console down with it.
            <ErrorBoundary fallback={
              <div className="text-[12.5px] text-muted">
                This file cannot be previewed here. Download it to open in a PDF reader.
              </div>
            }>
              <PdfViewer file={file} />
            </ErrorBoundary>
          )}
        </div>

        {/* The digest is what proves the file on screen is the one the
            framework was generated from. */}
        <div className="px-4 py-2 border-t border-border shrink-0">
          <span className="font-mono text-[10px] text-subtle break-all">sha256:{source.sha256}</span>
        </div>
      </div>
    </div>
  )
}
