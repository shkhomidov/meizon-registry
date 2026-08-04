// Plain-fetch client for the registry console/connect REST API. Every request
// carries the session cookie (credentials: 'include'); a 401 surfaces as an
// AuthError so the app can redirect to sign-in.

export class AuthError extends Error {}

async function request(path, { method = 'GET', body } = {}) {
  const res = await fetch('/api' + path, {
    method,
    credentials: 'include',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })

  if (res.status === 401) throw new AuthError('not authenticated')

  const text = await res.text()
  const data = text ? JSON.parse(text) : null
  if (!res.ok) throw new Error((data && data.error) || `request failed (${res.status})`)
  return data
}

export const api = {
  signIn: (email, password) => request('/connect/v1/signin', { method: 'POST', body: { email, password } }),
  signOut: () => request('/connect/v1/signout', { method: 'POST' }),
  viewer: () => request('/connect/v1/viewer'),

  listFrameworks: () => request('/console/v1/frameworks'),
  getFramework: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}`),
  createFramework: (payload) => request('/console/v1/frameworks', { method: 'POST', body: payload }),
  addControl: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/controls`, { method: 'POST', body: payload }),
  submit: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/submit`, { method: 'POST' }),
  approve: (ref, comment) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/approve`, { method: 'POST', body: { comment } }),
  publish: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/publish`, { method: 'POST' }),
  reject: (ref, comment) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/reject`, { method: 'POST', body: { comment } }),
  deprecate: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/deprecate`, { method: 'POST' }),
  audit: () => request('/console/v1/audit'),
  // Downloads the framework as a flat meizon-framework/v2 file. Goes through
  // fetch (not a bare link) so the session cookie and error handling apply.
  mappingsBetween: (source, target) =>
    request(`/console/v1/mappings?source=${encodeURIComponent(source)}&target=${encodeURIComponent(target || '')}`),
  updateMapping: (ref, id, body) =>
    request(`/console/v1/frameworks/${encodeURIComponent(ref)}/mappings/${encodeURIComponent(id)}`, { method: 'PATCH', body }),
  deleteMappingRow: (ref, id, nodeKind) =>
    request(`/console/v1/frameworks/${encodeURIComponent(ref)}/mappings/${encodeURIComponent(id)}/any?nodeKind=${encodeURIComponent(nodeKind)}`, { method: 'DELETE' }),

  listJobs: () => request('/console/v1/jobs'),

  autoMap: (ref, body) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/automap`, { method: 'POST', body }),
  mappingProposals: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/mapping-proposals`),
  decideProposals: (ref, ids, reject) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/mapping-proposals/decide`, { method: 'POST', body: { ids, reject } }),

  // The source file the framework was generated from. Fetched as a blob
  // because the endpoint is cookie-authenticated — an <iframe src> would not
  // carry the session.
  sourceDocumentBlob: async (ref) => {
    const res = await fetch(`/api/console/v1/frameworks/${encodeURIComponent(ref)}/source`, { credentials: 'include' })
    if (res.status === 401) throw new AuthError('not authenticated')
    if (res.status === 404) throw new Error('no source document was kept for this framework')
    if (!res.ok) throw new Error(`could not load the source document (${res.status})`)
    return res.blob()
  },
  downloadSourceDocument: async (ref) => {
    const res = await fetch(`/api/console/v1/frameworks/${encodeURIComponent(ref)}/source?download=1`, { credentials: 'include' })
    if (res.status === 401) throw new AuthError('not authenticated')
    if (res.status === 404) throw new Error('no source document was kept for this framework')
    if (!res.ok) throw new Error(`download failed (${res.status})`)
    const blob = await res.blob()
    const name = (res.headers.get('Content-Disposition') || '').match(/filename="?([^"]+)"?/)?.[1] || `${ref}.pdf`
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url; a.download = name; document.body.appendChild(a); a.click()
    a.remove(); URL.revokeObjectURL(url)
  },

  translations: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/translations`),
  addTranslation: (ref, language) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/translations`, { method: 'POST', body: { language } }),

  downloadFramework: async (ref, lang) => {
    const q = lang ? `?lang=${encodeURIComponent(lang)}` : ''
    const res = await fetch(`/api/console/v1/frameworks/${encodeURIComponent(ref)}/export${q}`, { credentials: 'include' })
    if (res.status === 401) throw new AuthError('not authenticated')
    if (!res.ok) {
      const text = await res.text()
      let msg = `export failed (${res.status})`
      try { msg = JSON.parse(text).error || msg } catch { /* not json */ }
      throw new Error(msg)
    }
    const blob = await res.blob()
    const name = (res.headers.get('Content-Disposition') || '').match(/filename="?([^"]+)"?/)?.[1] || `${ref}.json`
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url; a.download = name
    document.body.appendChild(a); a.click(); a.remove()
    URL.revokeObjectURL(url)
  },

  // Structure (universal hierarchy) + cross-mappings — all UI-managed.
  getStructure: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/structure`),
  addCategory: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/categories`, { method: 'POST', body: payload }),
  addRequirement: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/requirements`, { method: 'POST', body: payload }),
  deleteNode: (ref, level, code) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/structure/${level}/${encodeURIComponent(code)}`, { method: 'DELETE' }),
  addMapping: (ref, itemCode, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/items/${encodeURIComponent(itemCode)}/mappings`, { method: 'POST', body: payload }),
  removeMapping: (ref, id) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/mappings/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  importFramework: (doc) => request('/console/v1/frameworks/import', { method: 'POST', body: doc }),
  // Generate from document: uploads a file (multipart) or posts { text, brief }.
  generateFromFile: async (file, brief) => {
    const fd = new FormData()
    fd.append('document', file)
    if (brief) fd.append('brief', brief)
    const res = await fetch('/api/console/v1/frameworks/generate', { method: 'POST', credentials: 'include', body: fd })
    if (res.status === 401) throw new AuthError('not authenticated')
    const text = await res.text()
    const data = text ? JSON.parse(text) : null
    if (!res.ok) throw new Error((data && data.error) || `request failed (${res.status})`)
    return data
  },
  generateFromText: (text, brief) => request('/console/v1/frameworks/generate', { method: 'POST', body: { text, brief } }),
  // Staged generation runs as a job; poll generateStatus(jobId) until done.
  generateStatus: (jobId) => request(`/console/v1/frameworks/generate/status/${encodeURIComponent(jobId)}`),

  // QA audit templates.
  qaGenerate: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/qa/generate`, { method: 'POST' }),
  qaTemplate: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/qa-template`),
  qaEvaluate: (ref, questionId, answer) => request(
    `/console/v1/frameworks/${encodeURIComponent(ref)}/qa/evaluate`,
    { method: 'POST', body: { questionId, answer } }),
  qaUpdateQuestion: (templateId, qid, question) => request(
    `/console/v1/qa-templates/${encodeURIComponent(templateId)}/questions/${encodeURIComponent(qid)}`,
    { method: 'PATCH', body: question }),
  qaDeleteQuestion: (templateId, qid) => request(
    `/console/v1/qa-templates/${encodeURIComponent(templateId)}/questions/${encodeURIComponent(qid)}`,
    { method: 'DELETE' }),
  qaReorder: (templateId, order) => request(
    `/console/v1/qa-templates/${encodeURIComponent(templateId)}/reorder`, { method: 'POST', body: { order } }),
  // jobId links the new framework to the source file staged when it was
  // uploaded, so the document stays with what was generated from it.
  acceptGenerated: (doc, jobId) => request(
    `/console/v1/frameworks/generate/accept${jobId ? `?jobId=${encodeURIComponent(jobId)}` : ''}`,
    { method: 'POST', body: doc }),
  // New version of an existing framework: same document-first flow, diffed
  // against the current latest version.
  generateNextVersionFromFile: async (ref, file, brief, version) => {
    const fd = new FormData()
    fd.append('document', file)
    if (brief) fd.append('brief', brief)
    fd.append('version', version)
    const res = await fetch(`/api/console/v1/frameworks/${encodeURIComponent(ref)}/next-version/generate`, { method: 'POST', credentials: 'include', body: fd })
    if (res.status === 401) throw new AuthError('not authenticated')
    const text = await res.text()
    const data = text ? JSON.parse(text) : null
    if (!res.ok) throw new Error((data && data.error) || `request failed (${res.status})`)
    return data
  },
  generateNextVersionFromText: (ref, { text, brief, version }) =>
    request(`/console/v1/frameworks/${encodeURIComponent(ref)}/next-version/generate`, { method: 'POST', body: { text, brief, version } }),
  acceptNextVersion: (ref, version, doc) =>
    request(`/console/v1/frameworks/${encodeURIComponent(ref)}/next-version/accept`, { method: 'POST', body: { version, document: doc } }),
  coverage: (source, target) => request(`/console/v1/coverage?source=${encodeURIComponent(source)}&target=${encodeURIComponent(target || '')}`),
  adminUnresolvedMappings: () => request('/console/v1/admin/mappings/unresolved'),
  adminResolveMappings: () => request('/console/v1/admin/mappings/resolve', { method: 'POST' }),

  // Catalogs: control library, evidence guidance, policy templates.
  controlsLibrary: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/controls-library`),
  addControlEntry: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/controls-library`, { method: 'POST', body: payload }),
  deleteControlEntry: (ref, id) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/controls-library/${id}`, { method: 'DELETE' }),
  addEvidence: (ref, controlId, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/controls-library/${controlId}/evidence`, { method: 'POST', body: payload }),
  deleteEvidence: (ref, id) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/evidence/${id}`, { method: 'DELETE' }),
  policyTemplates: (ref) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/policy-templates`),
  upsertPolicyTemplate: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/policy-templates`, { method: 'POST', body: payload }),
  deletePolicyTemplate: (ref, id) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/policy-templates/${id}`, { method: 'DELETE' }),

  // AI-assisted authoring (the LLM proposes; the auditor decides).
  aiStatus: () => request('/console/v1/ai/status'),
  aiGenerate: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/ai/generate`, { method: 'POST', body: payload }),
  aiAccept: (ref, payload) => request(`/console/v1/frameworks/${encodeURIComponent(ref)}/ai/accept`, { method: 'POST', body: payload }),
  adminGetLLM: () => request('/console/v1/admin/settings/llm'),
  adminPutLLM: (payload) => request('/console/v1/admin/settings/llm', { method: 'PUT', body: payload }),
  adminTestLLM: () => request('/console/v1/admin/settings/llm/test', { method: 'POST' }),

  // Admin (superadmin-gated server-side).
  adminUsers: () => request('/console/v1/admin/users'),
  adminCreateUser: (payload) => request('/console/v1/admin/users', { method: 'POST', body: payload }),
  adminAssignRole: (payload) => request('/console/v1/admin/users/role', { method: 'POST', body: payload }),
  adminKeys: () => request('/console/v1/admin/keys'),
  adminGenerateKey: (keyId) => request('/console/v1/admin/keys', { method: 'POST', body: { keyId } }),
  adminTokens: () => request('/console/v1/admin/tokens'),
  adminIssueToken: (payload) => request('/console/v1/admin/tokens', { method: 'POST', body: payload }),
  adminOrganizations: (status) => request(`/console/v1/admin/organizations${status ? `?status=${encodeURIComponent(status)}` : ''}`),
  adminRegisterOrganization: (name) => request('/console/v1/admin/organizations', { method: 'POST', body: { name } }),
  adminApproveOrganization: (tenant) => request(`/console/v1/admin/organizations/${encodeURIComponent(tenant)}/approve`, { method: 'POST' }),
  adminSuspendOrganization: (tenant) => request(`/console/v1/admin/organizations/${encodeURIComponent(tenant)}/suspend`, { method: 'POST' }),
}
