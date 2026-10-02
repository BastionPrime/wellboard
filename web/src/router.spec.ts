import { describe, it, expect, beforeEach, vi } from 'vitest'
import type { Router } from 'vue-router'

// A1: the router guard no longer auto-redirects to /wizard
// when the state has no sources — the wizard is reachable only via the
// explicit SettingsView button. The stores are module-mocked so the
// guard's bootstrap imports resolve to fakes (sources EMPTY: the old
// guard would have redirected to /wizard). The router module is
// re-imported fresh per test so the module-level "bootstrapped" flag
// does not leak between cases.

// Minimal DOM shims for vue-router's web history (vitest node env has
// no window; createWebHashHistory reads location/history at creation).
const fakeWindow = {
  location: { protocol: 'http:', host: 'localhost', hash: '', href: 'http://localhost/', pathname: '/', search: '' },
  history: { replaceState() {}, pushState() {}, state: null, go() {} },
  addEventListener() {},
  removeEventListener() {},
}
globalThis.window = fakeWindow as unknown as Window & typeof globalThis
globalThis.location = fakeWindow.location as unknown as Location
globalThis.history = fakeWindow.history as unknown as History
globalThis.document = {
  documentElement: { setAttribute() {} },
  createElement: () => ({ setAttribute() {}, appendChild() {}, style: {} }),
  addEventListener() {},
  removeEventListener() {},
  querySelector: () => null,
} as unknown as Document

vi.mock('./stores/settings', () => ({
  useSettingsStore: () => ({
    settings: { ui_port: 8090, lang: 'ru', geodata: 'runetfreedom' },
    lang: 'ru',
    load: () => Promise.resolve(),
  }),
}))
vi.mock('./stores/sources', () => ({
  useSourcesStore: () => ({
    sources: [], // EMPTY — the pre- guard redirected to /wizard
    load: () => Promise.resolve(),
  }),
}))
vi.mock('./stores/pool', () => ({
  usePoolStore: () => ({
    loadAll: () => Promise.resolve(),
  }),
}))
vi.mock('./i18n', () => ({
  setLocale: () => {},
}))

// the guard now asks the auth store first. Required+verified
// is the deployment default assumption here; the login cases below flip
// the stub to required+unauthenticated.
const authStub = {
  required: false,
  authenticated: true,
  username: '',
  checked: true,
  probe: () => Promise.resolve(true),
  clear: () => {},
}
vi.mock('./stores/auth', () => ({ useAuthStore: () => authStub }))

async function freshRouter(): Promise<Router> {
  const mod = await import('./router')
  return mod.router
}

describe('router first-run behavior (A1)', () => {
  beforeEach(() => {
    vi.resetModules()
    authStub.required = false
    authStub.authenticated = true
  })

  it('does not redirect /dashboard to /wizard on empty sources', async () => {
    const router = await freshRouter()
    await router.push('/dashboard')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })

  it('lets /wizard be reached directly (explicit navigation only)', async () => {
    const router = await freshRouter()
    await router.push('/wizard')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/wizard')
  })

  it('redirects / to /dashboard (unchanged default)', async () => {
    const router = await freshRouter()
    await router.push('/')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })
})

describe('router login gate ()', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('sends an unauthenticated visitor to /login when login is required', async () => {
    authStub.required = true
    authStub.authenticated = false
    const router = await freshRouter()
    await router.push('/settings')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('lets /login render when login is required', async () => {
    authStub.required = true
    authStub.authenticated = false
    const router = await freshRouter()
    await router.push('/login')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/login')
  })

  it('skips the login screen when the deployment has no login', async () => {
    authStub.required = false
    authStub.authenticated = true
    const router = await freshRouter()
    await router.push('/login')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/dashboard')
  })
})
