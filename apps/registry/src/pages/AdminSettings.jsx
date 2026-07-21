import { useEffect, useState } from 'react'
import { Settings2, Sparkles, PlugZap, RotateCcw, ListOrdered, ChevronRight, ChevronDown, ScanText } from 'lucide-react'
import { api } from '../lib/api.js'
import { Page, Card, Button, Badge, Field, Input, Select, Textarea } from '../components/ui.jsx'

// Gemini leads: gemini-2.5-pro has the largest context window of the three,
// which is what ingesting a full compliance standard needs.
const DEFAULT_MODELS = { gemini: 'gemini-2.5-pro', anthropic: 'claude-sonnet-5', openai: 'gpt-4o' }
const DEFAULT_MAX_TOKENS = { gemini: 32768, anthropic: 16384, openai: 8192 }

export default function AdminSettings() {
  const [form, setForm] = useState({ provider: 'gemini', apiKey: '', model: '', baseUrl: '', maxTokens: DEFAULT_MAX_TOKENS.gemini })
  // OCR is a separate service with its own credential — a Mistral key must
  // never end up in the LLM provider field.
  const [ocr, setOcr] = useState({ provider: 'mistral', apiKey: '', model: '' })
  const [ocrConfigured, setOcrConfigured] = useState(false)
  const [ocrDefaultModel, setOcrDefaultModel] = useState('mistral-ocr-2505')
  const setOcrField = (k) => (e) => setOcr({ ...ocr, [k]: e.target.value })
  // One editable instruction per pipeline step, keyed by step (identify/extract/qa).
  const [steps, setSteps] = useState([])
  const [instructions, setInstructions] = useState({})
  const [showContract, setShowContract] = useState({})
  const [configured, setConfigured] = useState(false)
  const [notice, setNotice] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [testing, setTesting] = useState(false)
  const set = (k) => (e) => setForm({ ...form, [k]: e.target.value })

  async function load() {
    try {
      const s = await api.adminGetLLM()
      setConfigured(s.configured)
      setOcrConfigured(s.ocrConfigured)
      setOcrDefaultModel(s.ocrDefaultModel || 'mistral-ocr-2505')
      setOcr((o) => ({ ...o, provider: s.ocrProvider || 'mistral', model: s.ocrModel || '', apiKey: '' }))
      setSteps(s.steps || [])
      setInstructions(Object.fromEntries((s.steps || []).map((st) => [st.key, st.instruction])))
      setForm((f) => ({
        ...f,
        provider: s.provider || f.provider,
        apiKey: '',
        model: s.model || (s.provider ? '' : f.model),
        baseUrl: s.baseUrl || '',
        maxTokens: s.maxTokens || DEFAULT_MAX_TOKENS[s.provider || f.provider] || 8192,
      }))
    } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  async function save() {
    setError(''); setNotice(''); setBusy(true)
    try {
      await api.adminPutLLM({
        provider: form.provider,
        apiKey: form.apiKey,
        model: form.model || DEFAULT_MODELS[form.provider],
        baseUrl: form.baseUrl,
        maxTokens: Number(form.maxTokens) || DEFAULT_MAX_TOKENS[form.provider] || 8192,
        identifyInstruction: instructions.identify || '',
        controlsInstruction: instructions.controls || '',
        generationInstruction: instructions.extract || '',
        qaInstruction: instructions.qa || '',
        translateInstruction: instructions.translate || '',
        mappingInstruction: instructions.mapping || '',
        ocrProvider: ocr.provider,
        ocrApiKey: ocr.apiKey,
        ocrModel: ocr.model,
      })
      setNotice('Settings saved.')
      setForm({ ...form, apiKey: '' })
      setOcr({ ...ocr, apiKey: '' })
      await load()
    } catch (e) { setError(e.message) } finally { setBusy(false) }
  }

  async function test() {
    setError(''); setNotice(''); setTesting(true)
    try {
      const res = await api.adminTestLLM()
      setNotice(`Connection OK — provider replied: “${res.reply}”`)
    } catch (e) { setError(e.message) } finally { setTesting(false) }
  }

  return (
    <Page
      title="Settings"
      subtitle="Platform configuration. The LLM provider powers AI-assisted framework authoring: the model proposes content, auditors review and accept — nothing is applied without a human decision."
    >
      {notice && <div className="text-[12.5px] mb-3" style={{ color: 'var(--b-green)' }}>{notice}</div>}
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}

      <Card icon={Sparkles} title="AI assist — LLM provider"
        action={configured ? <Badge tone="green">configured</Badge> : <Badge tone="grey">not configured</Badge>}>
        <div className="flex flex-col gap-4 max-w-2xl">
          <div className="grid grid-cols-2 gap-4">
            <Field label="Provider">
              {/* Switching provider clears the Base URL: an override left from
                  another provider silently misroutes every request to an
                  endpoint that speaks a different wire format. */}
              <Select value={form.provider} onChange={(e) => setForm({
                  ...form, provider: e.target.value, model: '', baseUrl: '',
                  maxTokens: DEFAULT_MAX_TOKENS[e.target.value] || 8192,
                })}>
                <option value="gemini">Google Gemini — largest context, best for long standards</option>
                <option value="anthropic">Anthropic</option>
                <option value="openai">OpenAI</option>
              </Select>
            </Field>
            <Field label="Model" hint={`default: ${DEFAULT_MODELS[form.provider]}`}>
              <Input value={form.model} onChange={set('model')} placeholder={DEFAULT_MODELS[form.provider]} />
            </Field>
          </div>
          <Field label="API key" hint={configured ? 'A key is stored (encrypted). Leave empty to keep it.' : 'Stored AES-256-GCM encrypted; never shown again.'}>
            <Input type="password" value={form.apiKey} onChange={set('apiKey')} placeholder={configured ? '•••••••• (unchanged)' : 'sk-…'} />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Base URL (optional)" hint="Azure OpenAI, proxy or gateway endpoint">
              <Input value={form.baseUrl} onChange={set('baseUrl')} placeholder="https://…" />
            </Field>
            <Field label="Max output tokens" hint={`default for ${form.provider}: ${DEFAULT_MAX_TOKENS[form.provider] || 8192}`}>
              <Input type="number" value={form.maxTokens} onChange={set('maxTokens')} />
            </Field>
          </div>
          <div className="flex gap-2">
            <Button busy={busy} onClick={save} icon={Settings2}>Save settings</Button>
            <Button variant="ghost" busy={testing} onClick={test} icon={PlugZap} disabled={!configured && !form.apiKey}>Test connection</Button>
          </div>
        </div>
      </Card>

      <Card icon={ScanText} title="OCR — scanned documents" className="mt-5"
        action={ocrConfigured
          ? <Badge tone="green">key stored</Badge>
          : <Badge tone="grey">not configured</Badge>}>
        <div className="space-y-4">
          <p className="text-[12.5px] text-muted">
            Most PDFs carry a text layer and are read directly — free and exact. A scanned
            PDF has none, and without OCR it cannot be ingested at all. OCR runs
            <span className="text-text"> only as a fallback</span>, never on a document that
            already has text.
          </p>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Provider">
              <Select value={ocr.provider} onChange={setOcrField('provider')}>
                <option value="mistral">Mistral OCR</option>
              </Select>
            </Field>
            <Field label="Model" hint={`default: ${ocrDefaultModel}`}>
              <Input value={ocr.model} onChange={setOcrField('model')} placeholder={ocrDefaultModel} />
            </Field>
          </div>
          <Field label="OCR API key"
            hint={ocrConfigured
              ? 'A key is stored (encrypted). Leave empty to keep it.'
              : 'Separate from the LLM key above. Stored AES-256-GCM encrypted; never shown again.'}>
            <Input type="password" value={ocr.apiKey} onChange={setOcrField('apiKey')}
              placeholder={ocrConfigured ? '•••••••• (unchanged)' : 'Your Mistral API key'} />
          </Field>
          <p className="text-[12px] text-muted">
            A scanned document is uploaded to Mistral to be read, then deleted again. It
            leaves this server — do not configure OCR for documents that may not be sent to
            a third party.
          </p>
          <Button busy={busy} onClick={save} icon={Settings2}>Save settings</Button>
        </div>
      </Card>

      <Card icon={ListOrdered} title="Generation prompts — one per pipeline step"
        className="mt-5"
        action={<span className="text-[11.5px] text-muted">{steps.length} steps</span>}>
        <p className="text-[12.5px] text-muted mb-4 max-w-3xl">
          Generating a framework runs as several LLM requests. Each step below has its own editable
          instruction. The fixed contract shown under each one (JSON-only output, schema version,
          source provenance) is always applied on top, so editing an instruction can never break the
          output format. Changes take effect on the next generation.
        </p>

        <div className="space-y-5">
          {steps.map((st) => (
            <div key={st.key} className="border border-border rounded-md p-3.5 bg-surface">
              <div className="flex items-center justify-between gap-3 mb-1">
                <span className="text-[13px] font-medium text-text">{st.label}</span>
                <button type="button"
                  onClick={() => setInstructions({ ...instructions, [st.key]: st.default })}
                  className="inline-flex items-center gap-1 text-[11.5px] text-muted hover:text-sage shrink-0">
                  <RotateCcw size={12} /> Reset to default
                </button>
              </div>
              <div className="text-[11.5px] text-muted mb-2.5">{st.description}</div>

              <Textarea rows="6" value={instructions[st.key] ?? ''}
                onChange={(e) => setInstructions({ ...instructions, [st.key]: e.target.value })}
                placeholder={st.default} className="!text-[12.5px] leading-relaxed" />

              <button type="button"
                onClick={() => setShowContract({ ...showContract, [st.key]: !showContract[st.key] })}
                className="mt-2 inline-flex items-center gap-1 text-[11.5px] text-muted hover:text-text">
                {showContract[st.key] ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                Fixed contract (always sent, read-only)
              </button>
              {showContract[st.key] && (
                <pre className="mt-1.5 whitespace-pre-wrap bg-inset border border-border rounded p-2.5 text-[11.5px] text-text2 leading-relaxed">{st.contract}</pre>
              )}
            </div>
          ))}
        </div>

        <div className="flex gap-2 mt-5">
          <Button busy={busy} onClick={save} icon={Settings2}>Save prompts</Button>
        </div>
      </Card>
    </Page>
  )
}
