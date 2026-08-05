import { useEffect, useState } from 'react'
import { KeyRound, Plus, Copy, Check } from 'lucide-react'
import { api } from '../lib/api.js'
import {
  Page, Card, Button, Badge, Table, THead, TR, TH, TD, Dialog, Field, Input, EmptyState,
} from '../components/ui.jsx'

// pinnedForm is the "keyId:base64" string a consumer pins to verify bundles —
// exactly what registryctl --pubkey and the sync client's public-keys config
// accept. Built here so the value shown is the value pasted, with no reassembly.
function pinnedForm(k) {
  return `${k.keyId}:${k.publicKey}`
}

// CopyButton copies text to the clipboard and briefly confirms, so an operator
// gets feedback instead of wondering whether the click registered. With a label
// it renders as a labelled button ("Copy key" → "Copied"); without one it is a
// compact icon for inline use next to a value.
function CopyButton({ text, title = 'Copy', label, className = '' }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1200)
  }

  if (label) {
    return (
      <button
        type="button" title={title} onClick={copy}
        className={`inline-flex items-center gap-1.5 rounded-btn border border-border px-2.5 py-1.5 text-[12px] font-medium text-text hover:bg-surface ${className}`}
      >
        {copied ? <Check size={14} className="text-sage" /> : <Copy size={14} />}
        {copied ? 'Copied' : label}
      </button>
    )
  }

  return (
    <button
      type="button" title={title} onClick={copy}
      className={`text-muted hover:text-sage shrink-0 ${className}`}
    >
      {copied ? <Check size={15} className="text-sage" /> : <Copy size={15} />}
    </button>
  )
}

export default function AdminKeys() {
  const [keys, setKeys] = useState(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)
  const [viewing, setViewing] = useState(null)

  async function load() {
    try { setKeys(await api.adminKeys()) } catch (e) { setError(e.message) }
  }
  useEffect(() => { load() }, [])

  return (
    <Page
      title="Signing keys"
      subtitle="ed25519 keys used to sign published versions. GRC instances pin the public key to verify bundles. Generating a new key rotates the active one."
      action={<Button icon={Plus} onClick={() => setCreating(true)}>Generate key</Button>}
    >
      {error && <div className="text-[12.5px] text-st-red mb-3">{error}</div>}
      <Card icon={KeyRound} title="Keys">
        {keys && keys.length === 0 ? (
          <EmptyState icon={KeyRound} title="No signing keys" hint="Generate a key before publishing." />
        ) : (
          <Table>
            <THead><TR><TH>Key id</TH><TH>Status</TH><TH>Public key (base64)</TH><TH>Created</TH><TH> </TH></TR></THead>
            <tbody>
              {keys?.map((k) => (
                <TR key={k.keyId}>
                  <TD className="font-mono text-[12px] text-text">{k.keyId}</TD>
                  <TD>{k.active ? <Badge tone="green">active</Badge> : <Badge tone="grey">rotated</Badge>}</TD>
                  <TD>
                    <div className="font-mono text-[11px] text-muted truncate max-w-[300px]">{k.publicKey}</div>
                  </TD>
                  <TD className="text-muted">{new Date(k.createdAt).toLocaleString()}</TD>
                  <TD>
                    <div className="flex items-center justify-end gap-1.5">
                      <CopyButton text={pinnedForm(k)} label="Copy key" title="Copy keyId:base64 (pin form)" />
                      <Button variant="ghost" size="sm" onClick={() => setViewing(k)}>View</Button>
                    </div>
                  </TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {creating && <KeyDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
      {viewing && <KeyDetails k={viewing} onClose={() => setViewing(null)} />}
    </Page>
  )
}

// CopyRow shows one full field with its own copy button: a label, the complete
// value in a wrapping monospace box (never truncated), and a copy control.
function CopyRow({ label, value, hint }) {
  return (
    <div className="space-y-1">
      <div className="text-[11px] uppercase tracking-wide text-muted">{label}</div>
      <div className="flex items-start gap-2">
        <code className="flex-1 font-mono text-[12px] text-text bg-inset rounded px-2 py-1.5 break-all">{value}</code>
        <CopyButton text={value} className="mt-1.5" />
      </div>
      {hint && <div className="text-[11px] text-muted">{hint}</div>}
    </div>
  )
}

// KeyDetails is the structured, fully-copyable view of one key — every field in
// full, so an operator can read or copy the exact public key and its pin form
// without leaving the console or querying the database.
function KeyDetails({ k, onClose }) {
  return (
    <Dialog title={`Signing key · ${k.keyId}`} onClose={onClose}
      footer={<Button onClick={onClose}>Close</Button>}>
      <div className="space-y-4">
        <div className="flex items-center gap-4 text-[12.5px]">
          <span className="text-muted">Algorithm <span className="text-text font-mono">ed25519</span></span>
          <span>{k.active ? <Badge tone="green">active</Badge> : <Badge tone="grey">rotated</Badge>}</span>
        </div>
        <CopyRow label="Key id" value={k.keyId} />
        <CopyRow label="Public key (base64)" value={k.publicKey} />
        <CopyRow
          label="Pin form (keyId:base64)"
          value={pinnedForm(k)}
          hint="Paste into a consumer's public-keys / registryctl --pubkey to verify this key's bundles."
        />
        <div className="text-[11.5px] text-muted">
          Created {new Date(k.createdAt).toLocaleString()}
          {k.rotatedAt ? ` · rotated ${new Date(k.rotatedAt).toLocaleString()}` : ''}
        </div>
      </div>
    </Dialog>
  )
}

function KeyDialog({ onClose, onDone }) {
  const [keyId, setKeyId] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  async function submit() {
    setError(''); setBusy(true)
    try { await api.adminGenerateKey(keyId); onDone() } catch (err) { setError(err.message) } finally { setBusy(false) }
  }

  return (
    <Dialog title="Generate signing key" onClose={onClose}
      footer={<><Button variant="ghost" onClick={onClose}>Cancel</Button><Button busy={busy} onClick={submit}>Generate</Button></>}>
      <Field label="Key id" hint="A stable identifier, e.g. reg-2026. The previous active key is rotated.">
        <Input value={keyId} onChange={(e) => setKeyId(e.target.value)} placeholder="reg-2026" />
      </Field>
      {error && <div className="text-[12.5px] text-st-red">{error}</div>}
    </Dialog>
  )
}
