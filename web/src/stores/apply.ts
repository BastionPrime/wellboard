import { defineStore } from 'pinia'
import { api, type ApplyResult, type AppliedRecord, type Pending } from '../api'

// Apply store (FR-6): pending-changes indicator, the Apply button,
// applied history and the Rollback button. The indicator polls
// /pending (30 s default) so the user sees unapplied edits without
// reloading; Apply/Rollback are explicit actions (never auto-apply).
export const useApplyStore = defineStore('apply', {
  state: () => ({
    pending: false as boolean | null, // null = unknown (not loaded)
    hash: '',
    applying: false,
    rollingBack: false,
    history: [] as AppliedRecord[],
    historyError: '' as string | null,
    lastResult: null as ApplyResult | null,
    applyError: '' as string | null,
  }),
  getters: {
    canRollback: (s) => s.history.length >= 2,
  },
  actions: {
    async refreshPending() {
      try {
        const p = await api<Pending>('/pending')
        this.pending = p.pending
        this.hash = p.hash
      } catch {
        // Endpoint not wired (e.g. fresh dev server): leave as-is.
      }
    },
    async loadHistory() {
      this.historyError = null
      try {
        const res = await api<{ applied: AppliedRecord[] }>('/applied')
        this.history = res.applied ?? []
      } catch (e) {
        this.historyError = e instanceof Error ? e.message : String(e)
      }
    },
    // apply runs the FR-6 flow; the result (including failures with
    // stage/error/rollback info) is stored for the banner. On 409/503
    // the error body IS the result — keep both.
    async apply() {
      this.applying = true
      this.lastResult = null
      try {
        this.lastResult = await api<ApplyResult>('/apply', { method: 'POST' })
      } catch (e) {
        // Flow failures surface their body as the last result so the
        // banner can show stage/error; the throw lets the view add a
        // toast. APIError.message already carries the flow error.
        if (e instanceof Error) this.applyError = e.message
        throw e
      } finally {
        this.applying = false
        this.refreshPending()
        this.loadHistory()
      }
    },
    async rollback() {
      this.rollingBack = true
      try {
        await api<{ status: string }>('/rollback', { method: 'POST' })
        this.lastResult = null
      } finally {
        this.rollingBack = false
        this.refreshPending()
        this.loadHistory()
      }
    },
  },
})
