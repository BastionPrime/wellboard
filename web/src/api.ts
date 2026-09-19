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
  geodata: 'runetfreedom' | 'metacubex'
  default_policy: Target
  delay_test_interval_sec: number
}

export interface Template {
  id: string
  name: string
  description: string
  conditions: RouteCondition[]
  typical_target: string
  providers?: string[]
}

export interface LANDevice {
  mac: string
  ip: string
  hostname: string
  static: boolean
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
