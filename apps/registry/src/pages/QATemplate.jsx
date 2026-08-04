// QATemplate — the standalone /frameworks/:ref/qa route. The audit template is
// normally authored from the framework's Audit tab now; this page renders the
// same QATab surface for direct links and bookmarks.
import { useCallback, useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import { ArrowLeft } from 'lucide-react'
import { api } from '../lib/api.js'
import { useSession, canAuthor } from '../context/SessionContext.jsx'
import { Page } from '../components/ui.jsx'
import QATab from '../components/QATab.jsx'

export default function QATemplate() {
  const { ref } = useParams()
  const navigate = useNavigate()
  const { viewer } = useSession()
  const [template, setTemplate] = useState(null)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try { setTemplate(await api.qaTemplate(ref)) } catch (e) {
      // A 404 (none generated yet) is the empty state QATab handles, not an error.
      if (/not found|resource/i.test(e.message)) setTemplate(null)
      else setError(e.message)
    }
  }, [ref])
  useEffect(() => { load() }, [load])

  const title = (
    <span className="flex items-center gap-3">
      <button onClick={() => navigate(`/frameworks/${ref}`)} className="text-muted hover:text-text"><ArrowLeft size={20} /></button>
      Audit template · {ref}
    </span>
  )

  return (
    <Page title={title} subtitle="AI-generated audit questions, keyed to requirements">
      {error && <div className="text-st-red text-[12.5px] mb-3">{error}</div>}
      <QATab refId={ref} template={template} editable={canAuthor(viewer)} onChanged={load} />
    </Page>
  )
}
