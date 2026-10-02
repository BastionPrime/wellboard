import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

// Hash history: the embedded SPA is served by the Go binary from /, and
// hash routing needs no server-side fallback for deep links.
export const routes: RouteRecordRaw[] = [
  { path: '/', redirect: '/dashboard' },
  { path: '/wizard', component: () => import('./views/WizardView.vue') },
  { path: '/dashboard', component: () => import('./views/DashboardView.vue') },
  { path: '/sources', component: () => import('./views/SourcesView.vue') },
  { path: '/servers', component: () => import('./views/ServersView.vue') },
  { path: '/routes', component: () => import('./views/RoutesView.vue') },
  { path: '/groups', component: () => import('./views/GroupsView.vue') },
  { path: '/templates', component: () => import('./views/TemplatesView.vue') },
  { path: '/monitoring', component: () => import('./views/MonitoringView.vue') },
  { path: '/logs', component: () => import('./views/LogsView.vue') },
  { path: '/settings', component: () => import('./views/SettingsView.vue') },
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes,
})

// Bootstrap: on the first navigation read settings (locale), sources
// and the pool before any view mounts. the first-run
// auto-redirect to /wizard is REMOVED — the wizard is reachable only
// by explicit navigation (the button in SettingsView); an empty state
// is handled by the per-screen empty-state hints.
let bootstrapped = false
router.beforeEach(async () => {
  if (bootstrapped) return true
  bootstrapped = true
  const { useSettingsStore } = await import('./stores/settings')
  const { useSourcesStore } = await import('./stores/sources')
  const { usePoolStore } = await import('./stores/pool')
  const settings = useSettingsStore()
  await settings.load().catch(() => {})
  const { setLocale } = await import('./i18n')
  setLocale(settings.lang)
  const sources = useSourcesStore()
  await sources.load().catch(() => {})
  const pool = usePoolStore()
  await pool.loadAll().catch(() => {})
  return true
})
