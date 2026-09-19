<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { t, setLocale, getLocale } from './i18n'
import { useSettingsStore } from './stores/settings'

// Shell: header + horizontal-scroll nav (works at 360px, NFR-7) +
// router view. Language toggle lives in the header for one-tap switch.
const route = useRoute()
const settings = useSettingsStore()

const navItems = [
  { path: '/dashboard', key: 'nav.dashboard' },
  { path: '/sources', key: 'nav.sources' },
  { path: '/servers', key: 'nav.servers' },
  { path: '/routes', key: 'nav.routes' },
  { path: '/templates', key: 'nav.templates' },
  { path: '/settings', key: 'nav.settings' },
]

const lang = computed(() => getLocale())

function toggleLang() {
  const next = lang.value === 'ru' ? 'en' : 'ru'
  setLocale(next)
  settings.setLang(next).catch(() => {}) // persist; failure keeps local locale
}

const isWizard = computed(() => route.path === '/wizard')
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
