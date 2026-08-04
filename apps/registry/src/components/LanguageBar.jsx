// LanguageBar — a framework's available languages, and adding one.
//
// English is canonical: the structure (refs, control links, cross-mappings)
// lives once and every other language is a text overlay keyed by ref. So
// switching language here changes what you download, never what the framework
// IS. That is why this is a chip row and not an editor.
import { useEffect, useState, useCallback } from 'react'
import { Languages, Plus, Download } from 'lucide-react'
import { api } from '../lib/api.js'
import { Badge, Button, Dialog, Field, Select } from './ui.jsx'

// The languages worth offering first; anything else can be typed.
const COMMON = [
  ['en', 'English'], ['ru', 'Русский'], ['uz', 'Oʻzbekcha'], ['kk', 'Қазақша'],
  ['tr', 'Türkçe'], ['de', 'Deutsch'], ['fr', 'Français'], ['es', 'Español'],
  ['ar', 'العربية'], ['zh', '中文'], ['ja', '日本語'],
]
const LABEL = Object.fromEntries(COMMON)
const label = (code) => LABEL[code] || code

export default function LanguageBar({ refId, editable, aiConfigured, selected = '', onSelect }) {
  const [view, setView] = useState(null)
  const [adding, setAdding] = useState(false)
  const [job, setJob] = useState(null)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try { setView(await api.translations(refId)) } catch (e) { setError(e.message) }
  }, [refId])
  useEffect(() => { load() }, [load])

  // Poll while a translation runs; the job reports the same status shape as
  // document ingestion.
  useEffect(() => {
    if (!job) return undefined
    const t = setInterval(async () => {
      try {
        const st = await api.generateStatus(job)
        if (st.status === 'done') { setJob(null); load() }
        if (st.status === 'error') { setJob(null); setError(st.error || 'translation failed') }
      } catch (e) { setJob(null); setError(e.message) }
    }, 1500)
    return () => clearInterval(t)
  }, [job, load])

  if (!view) return null

  const source = view.sourceLanguage || ''
  const existing = new Set([source, ...view.languages.map((l) => l.language)].filter(Boolean))

  // Canonical first, then the source language, then the rest.
  const rows = [...view.languages.map((l) => ({ ...l, isSource: l.language === source }))]
  if (source && !rows.some((l) => l.language === source)) {
    rows.push({ language: source, nodes: 0, isSource: true })
  }
  const ordered = rows.sort((a, b) => {
    if (a.language === view.canonical) return -1
    if (b.language === view.canonical) return 1
    if (a.isSource !== b.isSource) return a.isSource ? -1 : 1
    return a.language.localeCompare(b.language)
  })

  async function download(lang) {
    setError('')
    try { await api.downloadFramework(refId, lang) } catch (e) { setError(e.message) }
  }

  // A chip's view value: the source language shows the un-overlaid record (""),
  // every other language shows its translation overlay.
  const viewValue = (l) => (l.isSource ? '' : l.language)
  const pick = (l) => onSelect && onSelect(viewValue(l))

  return (
    <div className="flex flex-wrap items-center gap-2 mb-4">
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-muted flex items-center gap-1">
        <Languages size={12} /> languages
      </span>

      {/* Clicking a chip switches the whole framework view (structure, audit,
          …) into that language; the source chip returns to the canonical record. */}
      {ordered.map((l) => {
        const active = viewValue(l) === selected
        return (
          <button key={l.language} onClick={() => pick(l)}
            title={active ? 'Currently viewing' : `View in ${label(l.language)}${l.isSource ? '' : ` (${l.nodes} nodes)`}`}
            className={`inline-flex items-center gap-1 rounded-badge transition-shadow ${active ? 'ring-1 ring-sage' : 'hover:opacity-80'}`}>
            <Badge tone={l.language === view.canonical ? 'blue' : l.isSource ? 'green' : 'grey'}>
              {label(l.language)}
              {l.language === view.canonical ? ' · canonical' : ''}
              {l.isSource && l.language !== view.canonical ? ' · source' : ''}
            </Badge>
          </button>
        )
      })}

      {ordered.length === 0 && (
        <span className="text-[12px] text-muted">source language unknown — only the stored text is available</span>
      )}

      {editable && aiConfigured && (
        <Button size="sm" variant="ghost" icon={Plus} onClick={() => setAdding(true)} disabled={!!job}>
          {job ? 'Translating…' : 'Add language'}
        </Button>
      )}
      <button onClick={() => download(selected)} title={`Download the ${selected ? label(selected) : 'canonical'} JSON`}
        className="p-1 rounded text-muted hover:text-sage hover:bg-inset"><Download size={14} /></button>

      {error && <span className="text-[12px] text-st-red">{error}</span>}

      {adding && (
        <AddLanguageDialog existing={existing} canonical={view.canonical}
          onClose={() => setAdding(false)}
          onStart={async (lang) => {
            setError('')
            try {
              const { jobId } = await api.addTranslation(refId, lang)
              setAdding(false)
              setJob(jobId)
            } catch (e) { setError(e.message) }
          }} />
      )}
    </div>
  )
}

function AddLanguageDialog({ existing, canonical, onClose, onStart }) {
  const [lang, setLang] = useState(existing.has(canonical) ? '' : canonical)
  const [busy, setBusy] = useState(false)
  const available = COMMON.filter(([code]) => !existing.has(code))

  return (
    <Dialog title="Add a language" onClose={onClose}
      footer={<>
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button busy={busy} disabled={!lang} onClick={async () => { setBusy(true); await onStart(lang); setBusy(false) }}>
          Translate
        </Button>
      </>}>
      <Field label="Target language"
        hint="Only names and descriptions are translated. Refs, control links and cross-mappings are shared across every language and never change.">
        <Select value={lang} onChange={(e) => setLang(e.target.value)}>
          <option value="">Choose…</option>
          {available.map(([code, name]) => <option key={code} value={code}>{name} ({code})</option>)}
        </Select>
      </Field>
      {!existing.has(canonical) && (
        <div className="text-[12.5px] text-muted">
          <span className="text-text">English is the canonical language.</span> Cross-framework mapping
          runs on it, so a framework without an English version cannot be auto-mapped.
        </div>
      )}
    </Dialog>
  )
}
