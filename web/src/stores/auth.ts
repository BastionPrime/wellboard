import { defineStore } from 'pinia'
import { api, setCSRFToken } from '../api'

// Auth store (OPE-3402): the router's own root password, verified by
// the daemon through ubus. `required` mirrors the deployment setting
// (UCI wellboard.main.auth / --no-auth): a stand without a login never
// shows the login screen, so the board stays usable in dev.
export interface AuthSession {
  required: boolean
  authenticated: boolean
  username?: string
  csrf?: string
  expires_in_sec?: number
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    required: false,
    authenticated: false,
    username: '',
    checked: false,
    error: '' as string | null,
  }),
  actions: {
    async probe() {
      try {
        const s = await api<AuthSession>('/auth/session')
        this.apply(s)
      } catch {
        // The API is unreachable or too old to answer: keep the login
        // screen reachable rather than showing a board that cannot load.
        this.required = true
        this.authenticated = false
      } finally {
        this.checked = true
      }
      return this.authenticated
    },
    async login(password: string) {
      this.error = null
      try {
        const s = await api<AuthSession>('/auth/login', {
          method: 'POST',
          body: JSON.stringify({ password }),
        })
        this.apply(s)
        return true
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
        throw e
      }
    },
    async logout() {
      try {
        await api('/auth/logout', { method: 'POST' })
      } catch {
        // The session may already be gone; clearing locally is enough.
      }
      this.clear()
    },
    clear() {
      setCSRFToken('')
      this.authenticated = false
      this.username = ''
    },
    apply(s: AuthSession) {
      this.required = s.required
      this.authenticated = s.authenticated
      this.username = s.username ?? ''
      setCSRFToken(s.csrf ?? '')
    },
  },
})