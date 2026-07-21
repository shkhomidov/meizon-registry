import { useEffect, useState } from 'react'
import { KeyRound, Plus } from 'lucide-react'
import { api } from '../lib/api.js'
import {
  Page, Card, Button, Badge, Table, THead, TR, TH, TD, Dialog, Field, Input, EmptyState,
} from '../components/ui.jsx'

export default function AdminKeys() {
  const [keys, setKeys] = useState(null)
  const [error, setError] = useState('')
  const [creating, setCreating] = useState(false)

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
            <THead><TR><TH>Key id</TH><TH>Status</TH><TH>Public key (base64)</TH><TH>Created</TH></TR></THead>
            <tbody>
              {keys?.map((k) => (
                <TR key={k.keyId}>
                  <TD className="font-mono text-[12px] text-text">{k.keyId}</TD>
                  <TD>{k.active ? <Badge tone="green">active</Badge> : <Badge tone="grey">rotated</Badge>}</TD>
                  <TD className="font-mono text-[11px] text-muted max-w-[280px] truncate">{k.publicKey}</TD>
                  <TD className="text-muted">{new Date(k.createdAt).toLocaleString()}</TD>
                </TR>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {creating && <KeyDialog onClose={() => setCreating(false)} onDone={() => { setCreating(false); load() }} />}
    </Page>
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
