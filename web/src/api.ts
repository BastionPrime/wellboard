// TypeScript mirrors of the Go API contract (internal/api/api.go,
// internal/model/model.go). Only the fields the SPA reads are declared.

export interface UserInfo {
  upload: number
  download: number
  total: number
  expire: number // unix seconds; 0 = unknown
}

export interface HWIDStatus {
  active: boolean
  limit_reached: boolean
  not_supported: boolean
}

export interface Source {
  id: string
  kind: 'subscription' | 'manual'
  name: string
  url?: string
  enabled?: boolean
  update_interval_sec?: number
  last_update?: string
  last_error?: string | null
  userinfo?: UserInfo | null
  hwid_status?: HWIDStatus | null
  announce?: string | null
}

export interface ServerNode {
  id: string
  source_id: string
  name: string
  type: string
  delay_ms?: number
  stale?: boolean
}

export interface Group {
  id: string
  name: string
  type: 'select' | 'url-test' | 'fallback' | 'load-balance'
  members: string[]
}

export type TargetType = 'server' | 'group' | 'direct' | 'reject'

export interface Target {
  type: TargetType
  id?: string
}

export interface RouteCondition {
  type: string
  value: string
  label?: string
}

export interface Route {
  id: string
  name: string
  enabled: boolean
  order: number
  conditions: RouteCondition[]
  target: Target
  on_unavailable: 'block' | 'direct'
  providers?: string[]
}

export interface Settings {
  ui_port: number
  lang: string
  geosite_source: GeodataSource
  geoip_source: GeodataSource
  geosite_custom_url?: string
  geoip_custom_url?: string
  geodata_additions?: Record<string, string[]>
  default_policy: Target
  delay_test_interval_sec: number
  disabled_templates?: string[]
  // OPE-3045 A3: masked sources list for the settings page (the full
  // URL is a secret and is not sent to this screen).
  sources_summary?: SourceSummary[]
}

// GeodataSource: the per-kind geodata source (OPE-3045 B2).
export type GeodataSource = 'runetfreedom' | 'metacubex' | 'custom'

// SourceSummary is one masked sources row on the settings page
// (masked_url: first chars + length, never the full URL).
export interface SourceSummary {
  id: string
  name: string
  kind: 'subscription' | 'manual' | string
  masked_url: string
  enabled: boolean
}

export interface Template {
  id: string
  name: string
  description: string
  list_source: string
  conditions: RouteCondition[]
  typical_target: string
  providers?: string[]
  // OPE-3045 B1: origin ("builtin"|"custom") + override/disabled flags.
  origin: 'builtin' | 'custom'
  overridden?: boolean
  disabled?: boolean
}

export interface LANDevice {
  mac: string
  ip: string
  hostname: string
  static: boolean
}

// external nikki rules (read-only view of the active config).
export interface ExternalRule {
  index: number
  type: string
  value?: string
  target: string
  no_resolve?: boolean
  raw: string
  importable: boolean
}

export interface ExternalRulesView {
  source: string
  count: number
  targets: string[]
  rules: ExternalRule[]
  importable_types?: Record<string, string>
  warning?: string
}

// ExternalImportAll is the bulk-import result (OPE-3401: the running
// nikki rules become DISABLED WellBoard routes).
export interface ExternalImportAll {
  imported: number
  skipped: number
  total: number
  skipped_rules?: { index: number; raw: string; reason: string }[]
}

export interface Health {
  status: string
  app: string
  version: string
}

// Phase 5: apply flow (FR-6).
export interface ApplyResult {
  profile: string
  ok: boolean
  stage?: string
  error?: string
  rolled_back?: boolean
  last_good_profile?: string
  applied_at: string
}

export interface AppliedRecord {
  profile: string
  state_hash: string
  applied_at: string
}

export interface Pending {
  pending: boolean
  hash: string
}

// Phase 5: diagnostics (FR-9.4).
export interface DiagCheck {
  name: string
  ok: boolean
  detail?: string
  measure?: string
}

export interface Diagnostics {
  checks: DiagCheck[]
}

// Phase 5: logs (FR-9.2).
export interface Logs {
  lines: string[]
}

// APIError is the JSON error body returned by writeError ({error: "..."}).
export class APIError extends Error {
  status: number
  problems?: string[]
  routes?: string[]

  constructor(status: number, message: string, problems?: string[], routes?: string[]) {
    super(message)
    this.status = status
    this.problems = problems
    this.routes = routes
  }
}

// api fetches a WellBoard API endpoint (same origin in production; the
// Vite dev server proxies /api to localhost:8090). JSON in/out.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch('/api/v1' + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })
  if (res.status === 204) return undefined as T
  const text = await res.text()
  const body = text ? JSON.parse(text) : {}
  if (!res.ok) {
    throw new APIError(res.status, body.error ?? res.statusText, body.problems, body.routes)
  }
  return body as T
}

// importNikkiSources (OPE-3045 A3): discover subscription URLs in the
// nikki mihomo config (read-only scan) and create sources for the new
// ones. Returns the number imported and found.
export async function importNikkiSources(name?: string): Promise<{
  imported: number
  found: number
  sources: Source[]
}> {
  return api('/sources/import-nikki', {
    method: 'POST',
    body: JSON.stringify(name ? { name } : {}),
  })
}

// ----------------------------------------------------------------------------
// OPE-3045 B1: custom templates CRUD + toggle; B3: geosite tags
// ----------------------------------------------------------------------------

// TemplateInput is the template editor payload (create/edit).
export interface TemplateInput {
  id?: string // required on create; the path id wins on update
  name: string
  description?: string
  list_source?: string
  typical_target?: string
  conditions: { type: string; value: string }[]
  providers?: string[]
}

// createTemplate POSTs a new custom template (or a builtin override —
// writing a builtin id IS editing the shipped set).
export async function createTemplate(input: TemplateInput): Promise<Template> {
  return api('/templates', { method: 'POST', body: JSON.stringify(input) })
}

// updateTemplate PUTs the overlay file for an existing id (builtin
// ids write an override file).
export async function updateTemplate(id: string, input: TemplateInput): Promise<Template> {
  return api('/templates/' + id, { method: 'PUT', body: JSON.stringify(input) })
}

// deleteTemplate removes the overlay file (builtin re-appears when an
// override is deleted).
export async function deleteTemplate(id: string): Promise<{ status: string }> {
  return api('/templates/' + id, { method: 'DELETE' })
}

// toggleTemplate enables/disables a template id (builtin included).
export async function toggleTemplate(id: string, disabled: boolean): Promise<Template> {
  return api('/templates/' + id + '/toggle', {
    method: 'POST',
    body: JSON.stringify({ disabled }),
  })
}

// geositeTags fetches the known geosite categories for the template
// editor datalist (B3).
export async function geositeTags(): Promise<string[]> {
  const out = await api<{ tags: string[] }>('/geodata/tags?kind=geosite')
  return out.tags ?? []
}
