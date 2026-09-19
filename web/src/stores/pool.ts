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
      try {
        const out = await api<{ devices: LANDevice[] }>('/lan-devices')
        this.lanDevices = out.devices ?? []
      } catch (e) {
        this.lanDevices = null // endpoint unavailable (503 without leases)
      }
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
    async loadRoutes() {
      const rt = await api<{ routes: Route[] }>('/routes')
      this.routes = rt.routes ?? []
    },
  },
})
