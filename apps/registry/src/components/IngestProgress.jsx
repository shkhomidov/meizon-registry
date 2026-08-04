// IngestProgress — the live step checklist shown while the staged generation
// pipeline runs (identify → extract i/n → merge → validate → QA). Driven by the
// job status the page polls; the current step spins, finished steps check off,
// and the extract step shows a chunk progress bar.
import { Check, Loader2, Circle } from 'lucide-react'

// Mirrors the pipeline in docs/DOCUMENT-TO-FRAMEWORK-V2-GUIDE.md. Steps that
// count work (extract per chunk, controls per batch) show a live counter.
const STEPS = [
  // OCR only appears for scanned uploads; for a text PDF the job starts at
  // 'chunk' and this step is simply never the active one.
  { key: 'ocr', label: 'Read scanned pages (OCR)', counted: true, unit: 'pages' },
  { key: 'chunk', label: 'Split the document', counted: true },
  { key: 'identify', label: 'Identify the standard' },
  { key: 'extract', label: 'Extract requirements', counted: true },
  { key: 'merge', label: 'Merge & de-duplicate' },
  { key: 'controls', label: 'Suggest controls', counted: true },
  { key: 'assemble', label: 'Assemble framework' },
  { key: 'validate', label: 'Validate' },
  { key: 'qa', label: 'Quality check' },
]

// steps defaults to the document pipeline; auto-mapping passes its own list so
// the same checklist renders for any staged job.
export default function IngestProgress({ status, steps = STEPS, caption = 'Structuring the framework in verifiable steps…' }) {
  const curIdx = steps.findIndex((s) => s.key === status?.step)
  const idx = curIdx < 0 ? 0 : curIdx
  const errored = status?.status === 'error'

  return (
    <div className="max-w-md mx-auto py-10">
      <div className="text-[13px] text-muted mb-5 text-center">{caption}</div>
      <div className="space-y-3">
        {steps.map((s, i) => {
          const state = errored && i === idx ? 'error' : i < idx ? 'done' : i === idx ? 'active' : 'todo'
          const isCounted = s.counted
          const total = status?.progress?.total || 0
          const current = status?.progress?.current || 0
          return (
            <div key={s.key} className="flex items-center gap-3">
              <span className="shrink-0">
                {state === 'done' && <Check size={16} className="text-sage" />}
                {state === 'active' && <Loader2 size={16} className="text-sage animate-spin" />}
                {state === 'error' && <Circle size={16} className="text-st-red" />}
                {state === 'todo' && <Circle size={16} className="text-border" />}
              </span>
              <div className="flex-1 min-w-0">
                <div className={`text-[13px] ${state === 'todo' ? 'text-muted' : 'text-text'}`}>
                  {s.label}
                  {/* The counter belongs to the ACTIVE step only. There is a
                      single job-wide progress value (whatever the current step is
                      counting), so showing it on finished steps too made one
                      number — e.g. "6/16" — bleed across OCR, Split and Extract
                      at once. */}
                  {isCounted && state === 'active' && total > 0 && (
                    <span className="text-muted font-mono text-[12px]"> · {Math.min(current, total)}/{total}{s.unit ? ` ${s.unit}` : ''}</span>
                  )}
                </div>
                {isCounted && state === 'active' && total > 0 && (
                  <div className="h-1 bg-inset rounded mt-1.5 overflow-hidden">
                    <div className="h-full bg-sage transition-[width] duration-300" style={{ width: `${Math.round((Math.min(current, total) / total) * 100)}%` }} />
                  </div>
                )}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
