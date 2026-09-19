import { defineStore } from 'pinia'
import { api, type Source } from '../api'

// Sources store: the subscription/manual source list (FR-1).
// Note: the phase 3 API has no "refresh now" endpoint (the scheduler
// refreshes in the background); the UI refresh button re-fetches the list.
export const useSourcesStore = defineStore('sources', {
  state: () => ({
    sources: [] as Source[],
    loading: false,
    error: '' as string | null,
  }),
  getters: {
    subscriptionCount: (s) => s.sources.filter((x) => x.kind === 'subscription').length,
    errorCount: (s) => s.sources.filter((x) => !!x.last_error).length,
  },
  actions: {
    async load() {
      this.loading = true
      this.error = null
      try {
        const out = await api<{ sources: Source[] }>('/sources')
        this.sources = out.sources ?? []
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    async create(input: { kind: string; name: string; url?: string; update_interval_sec?: number }) {
      const src = await api<Source>('/sources', { method: 'POST', body: JSON.stringify(input) })
      this.sources.push(src)
      return src
    },
    async remove(id: string) {
      // 409 when routes depend on the source — surfaced to the caller.
      await api('/sources/' + id, { method: 'DELETE' })
      this.sources = this.sources.filter((s) => s.id !== id)
    },
    async setEnabled(id: string, enabled: boolean) {
      const src = await api<Source>('/sources/' + id, {
        method: 'PATCH',
        body: JSON.stringify({ enabled }),
      })
      const i = this.sources.findIndex((s) => s.id === id)
      if (i >= 0) this.sources[i] = src
      return src
    },
  },
})
