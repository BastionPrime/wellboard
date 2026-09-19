<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import { t, setLocale, getLocale } from './i18n'
import { useSettingsStore } from './stores/settings'
import { useApplyStore } from './stores/apply'
import { APIError } from './api'

// Shell: header + horizontal-scroll nav (works at 360px, NFR-7) +
// router view. Language toggle lives in the header for one-tap switch.
// The apply bar (FR-6.1 indicator + Apply/Rollback buttons) sits under
// the nav on every screen except the wizard.
const route = useRoute()
const settings = useSettingsStore()
const apply = useApplyStore()

const navItems = [
  { path: '/dashboard', key: 'nav.dashboard' },
  { path: '/sources', key: 'nav.sources' },
  { path: '/servers', key: 'nav.servers' },
  { path: '/routes', key: 'nav.routes' },
  { path: '/groups', key: 'nav.groups' },
  { path: '/templates', key: 'nav.templates' },
  { path: '/monitoring', key: 'nav.monitoring' },
  { path: '/logs', key: 'nav.logs' },
  { path: '/settings', key: 'nav.settings' },
]

const lang = computed(() => getLocale())

function toggleLang() {
  const next = lang.value === 'ru' ? 'en' : 'ru'
  setLocale(next)
  settings.setLang(next).catch(() => {}) // persist; failure keeps local locale
}

const isWizard = computed(() => route.path === '/wizard')

// FR-6 bar: pending indicator poll + explicit actions.
let poller: ReturnType<typeof setInterval> | null = null
onMounted(() => {
  apply.refreshPending()
  apply.loadHistory()
  poller = setInterval(() => apply.refreshPending(), 15000)
})
onUnmounted(() => {
  if (poller) clearInterval(poller)
})

const busy = computed(() => apply.applying || apply.rollingBack)

async function doApply() {
  try {
    await apply.apply()
  } catch {
    // The banner shows stage/error from the API body (APIError
    // message carries the flow error already).
  }
}

async function doRollback() {
  try {
    await apply.rollback()
  } catch (e) {
    apply.applyError = e instanceof APIError ? e.message : String(e)
  }
}

// resultBanner: the last apply outcome (success or failure detail).
const resultBanner = computed(() => {
  const r = apply.lastResult
  if (!r) return null
  if (r.ok) return { kind: 'ok', text: t('apply.appliedOk', { name: r.profile }) }
  return {
    kind: 'bad',
    text: t('apply.failed', { stage: r.stage ?? '?', error: r.error ?? '' }),
  }
})
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <div class="brand">
        <span class="brand-name">WellBoard</span>
      </div>
      <div class="spacer" />
      <button class="lang-btn" type="button" @click="toggleLang" :title="t('settings.language')">
        {{ lang === 'ru' ? 'EN' : 'RU' }}
      </button>
    </header>
    <nav v-if="!isWizard" class="nav" aria-label="main">
      <router-link v-for="item in navItems" :key="item.path" :to="item.path" class="nav-link">
        {{ t(item.key) }}
      </router-link>
    </nav>

    <!-- FR-6: unapplied-changes indicator + Apply / Rollback. -->
    <div v-if="!isWizard" class="applybar">
      <span class="pending" :class="{ changed: apply.pending === true }">
        <span class="dot" :class="{ changed: apply.pending === true }" />
        {{ apply.pending === true ? t('apply.pending') : apply.pending === false ? t('apply.applied') : '…' }}
      </span>
      <span class="spacer" />
      <button
        class="apply-btn"
        type="button"
        :disabled="busy"
        :title="t('apply.applyHint')"
        @click="doApply"
      >
        {{ apply.applying ? t('apply.applying') : t('apply.apply') }}
      </button>
      <button
        v-if="apply.canRollback"
        class="rollback-btn"
        type="button"
        :disabled="busy"
        :title="t('apply.rollbackHint')"
        @click="doRollback"
      >
        {{ apply.rollingBack ? t('apply.rollingBack') : t('apply.rollback') }}
      </button>
    </div>
    <div v-if="resultBanner" class="banner" :class="resultBanner.kind">
      {{ resultBanner.text }}
    </div>
    <div v-if="apply.applyError && !apply.lastResult" class="banner bad">
      {{ apply.applyError }}
    </div>

    <main class="content">
      <router-view />
    </main>
    <footer class="footer">WellBoard</footer>
  </div>
</template>

<style scoped>
.shell {
  max-width: 960px;
  margin: 0 auto;
  padding: 0 12px 24px;
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}
.topbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 2px 10px;
}
.brand-name {
  font-weight: 700;
  font-size: 1.15rem;
  letter-spacing: 0.02em;
}
.spacer {
  flex: 1;
}
.lang-btn {
  border: 1px solid #ccc;
  background: #f6f6f6;
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 0.85rem;
  cursor: pointer;
}
.lang-btn:active {
  background: #e8e8e8;
}
.nav {
  display: flex;
  flex-wrap: nowrap;
  overflow-x: auto;
  gap: 4px;
  border-bottom: 1px solid #e2e2e2;
  padding-bottom: 8px;
  -webkit-overflow-scrolling: touch;
}
.nav-link {
  text-decoration: none;
  color: #333;
  padding: 6px 10px;
  border-radius: 6px;
  white-space: nowrap;
  font-size: 0.95rem;
}
.nav-link.router-link-active {
  background: #1d4ed8;
  color: #fff;
}
.content {
  flex: 1;
  padding-top: 14px;
}
.applybar {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 2px;
  border-bottom: 1px solid #e2e2e2;
}
.pending {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 0.85rem;
  color: #555;
}
.pending .dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: #bbb;
  display: inline-block;
}
.pending .dot.changed {
  background: #f59e0b;
}
.pending.changed {
  color: #92400e;
  font-weight: 600;
}
.applybar .spacer {
  flex: 1;
}
.apply-btn {
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  border-radius: 6px;
  padding: 5px 14px;
  font-size: 0.85rem;
  cursor: pointer;
}
.apply-btn:disabled {
  opacity: 0.6;
  cursor: default;
}
.rollback-btn {
  border: 1px solid #ccc;
  background: #f6f6f6;
  color: #333;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 0.85rem;
  cursor: pointer;
}
.rollback-btn:disabled {
  opacity: 0.6;
  cursor: default;
}
.banner {
  border-radius: 8px;
  padding: 8px 12px;
  margin: 10px 0 0;
  font-size: 0.85rem;
  word-break: break-word;
}
.banner.ok {
  background: #f0fdf4;
  border: 1px solid #bbf7d0;
  color: #166534;
}
.banner.bad {
  background: #fef2f2;
  border: 1px solid #fecaca;
  color: #b91c1c;
}
.footer {
  text-align: center;
  color: #999;
  font-size: 0.75rem;
  padding-top: 18px;
}
@media (max-width: 360px) {
  .shell {
    padding: 0 8px 16px;
  }
  .nav-link {
    padding: 6px 8px;
    font-size: 0.9rem;
  }
  .brand-name {
    font-size: 1rem;
  }
}
</style>
