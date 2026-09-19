import { defineStore } from 'pinia'
import { api, type ServerNode, type Group, type Route, type Template, type LANDevice } from '../api'

// Pool store: servers, groups, routes, templates, LAN devices — the
// read-mostly catalog used by several screens. Mutations go through the
// same store (route enable/order, template apply, group CRUD) so all
// screens stay consistent after an action.
export const usePoolStore = defineStore('pool', {
  state: () => ({
    servers: [] as ServerNode[],
    groups: [] as Group[],
    routes: [] as Route[],
    templates: [] as Template[],
    lanDevices: [] as LANDevice[] | null,
    lanUnavailable: false,
    loading: false,
    error: '' as string | null,
  }),
  getters: {
    // serversByName maps id → node for target resolution in the UI.
    serverName: (s) => (id: string) => s.servers.find((x) => x.id === id)?.name ?? id,
    groupName: (s) => (id: string) => s.groups.find((x) => x.id === id)?.name ?? id,
    // routesSorted is the route list in generation order (FR-4.5).
    routesSorted: (s) => [...s.routes].sort((a, b) => a.order - b.order),
    targets: (s) => {
      // Options for route/template target pickers (server/group/direct/reject).
      const out: { value: string; label: string }[] = [
        { value: 'direct', label: 'DIRECT' },
        { value: 'reject', label: 'REJECT' },
      ]
      for (const g of s.groups) out.push({ value: g.id, label: g.name + ' (group)' })
      for (const sv of s.servers) out.push({ value: sv.id, label: sv.name })
      return out
    },
    // groupMembers labels group members for the group screen.
    memberLabel: (s) => (id: string) => {
      const g = s.groups.find((x) => x.id === id)
      if (g) return g.name + ' (group)'
      return s.servers.find((x) => x.id === id)?.name ?? id
    },
  },
  actions: {
    async loadAll() {
      this.loading = true
      this.error = null
      try {
        const [sv, gr, rt, tp] = await Promise.all([
          api<{ servers: ServerNode[] }>('/servers'),
          api<{ groups: Group[] }>('/groups'),
          api<{ routes: Route[] }>('/routes'),
          api<{ templates: Template[] }>('/templates'),
        ])
        this.servers = sv.servers ?? []
        this.groups = gr.groups ?? []
        this.routes = rt.routes ?? []
        this.templates = tp.templates ?? []
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async loadLAN() {
      this.lanUnavailable = false
      try {
        const out = await api<{ devices: LANDevice[] }>('/lan-devices')
        this.lanDevices = out.devices ?? []
      } catch {
        this.lanDevices = null // endpoint unavailable (503 without leases)
        this.lanUnavailable = true
      }
    },
    // pinStatic requests a static lease for a LAN device (FR-4.4). The
    // dev stub answers 200 {status:"simulated", detail:...} — returned
    // so the view can surface the API's own warning.
    async pinStatic(dev: { mac: string; ip: string; hostname?: string }) {
      return api<{ status: string; detail?: string }>('/lan-devices/static', {
        method: 'POST',
        body: JSON.stringify({ mac: dev.mac, ip: dev.ip, hostname: dev.hostname ?? '' }),
      })
    },
    async setRouteEnabled(id: string, enabled: boolean) {
      // PATCH requires name + at least one of conditions/providers/target
      // (api.go handleRoutePatch); we send the unchanged ones back.
      const rt = this.routes.find((r) => r.id === id)
      if (!rt) throw new Error('route not found')
      const out = await api<Route>('/routes/' + id, {
        method: 'PATCH',
        body: JSON.stringify({
          name: rt.name,
          enabled,
          conditions: rt.conditions,
          target: rt.target,
        }),
      })
      const i = this.routes.findIndex((r) => r.id === id)
      if (i >= 0) this.routes[i] = out
      return out
    },
    async moveRoute(id: string, dir: -1 | 1) {
      // Reorder in generation order: swap the order value with the
      // neighbouring route, PATCH both (server re-validates).
      const sorted = this.routesSorted
      const idx = sorted.findIndex((r) => r.id === id)
      const j = idx + dir
      if (j < 0 || j >= sorted.length) return
      const a = sorted[idx]
      const b = sorted[j]
      const [ra, rb] = await Promise.all([
        api<Route>('/routes/' + a.id, {
          method: 'PATCH',
          body: JSON.stringify({ name: a.name, order: b.order, conditions: a.conditions, target: a.target }),
        }),
        api<Route>('/routes/' + b.id, {
          method: 'PATCH',
          body: JSON.stringify({ name: b.name, order: a.order, conditions: b.conditions, target: b.target }),
        }),
      ])
      this.routes = this.routes.map((r) => (r.id === ra.id ? ra : r.id === rb.id ? rb : r))
    },
    async removeRoute(id: string) {
      await api('/routes/' + id, { method: 'DELETE' })
      this.routes = this.routes.filter((r) => r.id !== id)
    },
    // createRoute POSTs the serialized form (FR-4.1-4.3, FR-4.7); the
    // server is the validator (duplicate/bad conditions → 400).
    async createRoute(input: RouteInput) {
      const out = await api<Route>('/routes', {
        method: 'POST',
        body: JSON.stringify(routePayload(input)),
      })
      this.routes.push(out)
      return out
    },
    // updateRoute PATCHes; conditions/providers/target must always be
    // present (api.go requires name + something to update), enabled and
    // order/on_unavailable ride along.
    async updateRoute(id: string, input: RouteInput) {
      const out = await api<Route>('/routes/' + id, {
        method: 'PATCH',
        body: JSON.stringify(routePayload(input)),
      })
      const i = this.routes.findIndex((r) => r.id === id)
      if (i >= 0) this.routes[i] = out
      return out
    },
    async applyTemplate(id: string, target: { type: string; id?: string }) {
      const out = await api<Route & { status?: string; mode?: string; template?: string }>(
        '/templates/' + id + '/apply',
        { method: 'POST', body: JSON.stringify({ target }) },
      )
      // "all-vpn" changes the default policy instead of creating a route;
      // everything else appends a route.
      if (out.id) this.routes.push(out)
      await this.loadAll() // re-sync (order/ids are server-minted)
      return out
    },
    async deleteServer(id: string) {
      await api('/servers/' + id, { method: 'DELETE' })
      this.servers = this.servers.filter((s) => s.id !== id)
    },
    // addManualServers pastes share links (vless://…) — POST /servers,
    // the server parses them with the mihomo converter (FR-1.3).
    async addManualServers(links: string) {
      const out = await api<{ servers: ServerNode[] }>('/servers', {
        method: 'POST',
        body: JSON.stringify({ links }),
      })
      // Re-sync: merge matched updated entries by stable ID (FR-3.4).
      for (const srv of out.servers ?? []) {
        const i = this.servers.findIndex((s) => s.id === srv.id)
        if (i >= 0) this.servers[i] = srv
        else this.servers.push(srv)
      }
      return out.servers ?? []
    },
    async createGroup(input: { name: string; type: Group['type']; members: string[] }) {
      const out = await api<Group>('/groups', {
        method: 'POST',
        body: JSON.stringify(input),
      })
      this.groups.push(out)
      return out
    },
    async deleteGroup(id: string) {
      await api('/groups/' + id, { method: 'DELETE' })
      this.groups = this.groups.filter((g) => g.id !== id)
    },
    async loadRoutes() {
      const rt = await api<{ routes: Route[] }>('/routes')
      this.routes = rt.routes ?? []
    },
  },
})

// ----------------------------------------------------------------------------
// Route form serialization (tested in pool.spec.ts)
// ----------------------------------------------------------------------------

// RouteInput is what the route form produces. Conditions arrive as raw
// {type, value} rows exactly as the user typed them.
export interface RouteInput {
  name: string
  enabled: boolean
  order: number | null
  conditions: { type: string; value: string }[]
  providers: string[]
  target: { type: string; id?: string }
  onUnavailable: 'block' | 'direct'
}

// serializeConditions trims values, drops empty rows, and deduplicates
// (type+value) pairs — the API rejects duplicates with 400 (FR-4.2).
export function serializeConditions(
  rows: { type: string; value: string }[],
): { type: string; value: string }[] {
  const seen = new Set<string>()
  const out: { type: string; value: string }[] = []
  for (const r of rows) {
    const value = r.value.trim()
    if (!value) continue
    const key = r.type + '\u0000' + value
    if (seen.has(key)) continue
    seen.add(key)
    out.push({ type: r.type, value })
  }
  return out
}

// validateRouteInput mirrors the cheap client-side checks; the server
// remains the authority (api.go validateRouteIn). Returns a list of
// problems (empty = valid).
export function validateRouteInput(input: RouteInput): string[] {
  const problems: string[] = []
  if (!input.name.trim()) problems.push('name required')
  const conds = serializeConditions(input.conditions)
  if (conds.length === 0 && input.providers.filter((p) => p.trim()).length === 0) {
    problems.push('at least one condition or provider required')
  }
  for (const c of conds) {
    if (c.type === 'ip-cidr' || c.type === 'src-device') {
      if (!isIpOrCidr(c.value)) problems.push(c.type + ' needs an IP or CIDR: ' + c.value)
    }
    if (c.type === 'dst-port') {
      if (!isPortOrRange(c.value)) problems.push('dst-port needs N or N-M: ' + c.value)
    }
    if (c.type === 'domain' || c.type === 'domain-suffix') {
      if (/\s/.test(c.value) || c.value.startsWith('.')) problems.push('bad domain: ' + c.value)
    }
  }
  const t = input.target
  if (t.type !== 'direct' && t.type !== 'reject' && !t.id) {
    problems.push('target id required for server/group targets')
  }
  return problems
}

// routePayload assembles the exact JSON body for POST/PATCH /routes.
export function routePayload(input: RouteInput): Record<string, unknown> {
  const target: Record<string, string> =
    input.target.type === 'direct' || input.target.type === 'reject'
      ? { type: input.target.type }
      : { type: input.target.type, id: input.target.id ?? '' }
  const body: Record<string, unknown> = {
    name: input.name.trim(),
    enabled: input.enabled,
    conditions: serializeConditions(input.conditions),
    target,
    on_unavailable: input.onUnavailable,
  }
  const provs = input.providers.map((p) => p.trim()).filter(Boolean)
  if (provs.length) body.providers = provs
  if (input.order != null) body.order = input.order
  return body
}

function isIpOrCidr(v: string): boolean {
  const [addr, prefix] = v.split('/', 2)
  const a = addr.split('.')
  let isV4 = a.length === 4
  if (isV4) {
    for (const part of a) {
      if (!/^\d{1,3}$/.test(part) || Number(part) > 255) isV4 = false
    }
  }
  if (!isV4 && !/^[0-9a-fA-F:]+$/.test(addr)) return false
  if (prefix === undefined) return true
  if (!/^\d{1,3}$/.test(prefix)) return false
  const bits = Number(prefix)
  return bits >= 0 && bits <= (isV4 ? 32 : 128)
}

function isPortOrRange(v: string): boolean {
  const parts = v.split('-', 2)
  for (const p of parts) {
    if (!/^\d{1,5}$/.test(p)) return false
    const n = Number(p)
    if (n < 1 || n > 65535) return false
  }
  if (parts.length === 2) return Number(parts[0]) <= Number(parts[1])
  return true
}
