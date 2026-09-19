<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { t, fmtBytes, fmtDate } from '../i18n'
import { api, type Health } from '../api'
import { useSourcesStore } from '../stores/sources'
import { usePoolStore } from '../stores/pool'
import { useSettingsStore } from '../stores/settings'

// Dashboard (initial TZ 7 phase 4 "Главная"): health, counts, quotas and
// source errors, all from existing endpoints (/sources, /settings, /health).
const sources = useSourcesStore()
const pool = usePoolStore()
const settings = useSettingsStore()
const health = ref<Health | null>(null)
const healthError = ref('')

onMounted(async () => {
  if (!sources.sources.length) await sources.load()
  if (!pool.routes.length) await pool.loadAll()
  try {
    health.value = await api<Health>('/health')
  } catch {
    healthError.value = t('health.down')
  }
})

function targetLabel(target: { type: string; id?: string }): string {
  if (target.type === 'direct') return 'DIRECT'
  if (target.type === 'reject') return 'REJECT'
  if (target.type === 'server') return pool.serverName(target.id ?? '')
  return pool.groupName(target.id ?? '')
}
</script>

<template>
  <section>
    <h1>{{ t('dashboard.title') }}</h1>

    <div class="cards">
      <div class="card">
        <div class="card-label">{{ t('dashboard.health') }}</div>
        <div class="card-value" :class="{ ok: health?.status === 'ok', bad: !!healthError }">
          {{ healthError || health?.status || t('health.unknown') }}
        </div>
        <div class="card-sub" v-if="health?.version">{{ t('dashboard.version') }}: {{ health.version }}</div>
      </div>
      <div class="card">
        <div class="card-label">{{ t('dashboard.subscriptions') }}</div>
        <div class="card-value">{{ sources.subscriptionCount }}</div>
      </div>
      <div class="card">
        <div class="card-label">{{ t('dashboard.servers') }}</div>
        <div class="card-value">{{ pool.servers.length }}</div>
      </div>
      <div class="card">
        <div class="card-label">{{ t('dashboard.groups') }}</div>
        <div class="card-value">{{ pool.groups.length }}</div>
      </div>
      <div class="card">
        <div class="card-label">{{ t('dashboard.routes') }}</div>
        <div class="card-value">
          {{ pool.routes.length }}
          <span class="card-sub">{{ pool.routes.filter((r) => r.enabled).length }} {{ t('dashboard.activeRoutes') }}</span>
        </div>
      </div>
    </div>

    <h2>{{ t('dashboard.quotas') }}</h2>
    <table v-if="sources.sources.length" class="table">
      <thead>
        <tr>
          <th>{{ t('dashboard.source') }}</th>
          <th>{{ t('dashboard.traffic') }}</th>
          <th>{{ t('dashboard.used') }}</th>
          <th>{{ t('dashboard.expires') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in sources.sources" :key="s.id">
          <td>{{ s.name }}</td>
          <td>
            <template v-if="s.userinfo">
              {{ fmtBytes(s.userinfo.upload + s.userinfo.download) }} / {{ fmtBytes(s.userinfo.total) }}
            </template>
            <template v-else>{{ t('common.none') }}</template>
          </td>
          <td>
            <template v-if="s.userinfo && s.userinfo.total">
              {{ Math.round(((s.userinfo.upload + s.userinfo.download) / s.userinfo.total) * 100) }}%
            </template>
            <template v-else>{{ t('common.none') }}</template>
          </td>
          <td>{{ s.userinfo ? fmtDate(s.userinfo.expire) : t('common.none') }}</td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">{{ t('sources.noSources') }}</p>

    <h2>{{ t('dashboard.errors') }}</h2>
    <p v-if="sources.errorCount === 0" class="muted">{{ t('dashboard.noErrors') }}</p>
    <ul v-else class="error-list">
      <li v-for="s in sources.sources.filter((x) => x.last_error)" :key="s.id">
        <strong>{{ s.name }}:</strong> {{ s.last_error }}
      </li>
    </ul>

    <template v-if="settings.settings">
      <h2>{{ t('settings.defaultPolicy') }}</h2>
      <p class="muted">
        {{ t('settings.defaultPolicyHint') }}: <strong>{{ targetLabel(settings.settings.default_policy) }}</strong>
      </p>
    </template>
  </section>
</template>

<style scoped>
h1 {
  margin: 0 0 12px;
  font-size: 1.3rem;
}
h2 {
  margin: 20px 0 8px;
  font-size: 1.05rem;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 8px;
}
.card {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 10px 12px;
}
.card-label {
  color: #666;
  font-size: 0.8rem;
  margin-bottom: 4px;
}
.card-value {
  font-size: 1.2rem;
  font-weight: 600;
}
.card-value.ok {
  color: #15803d;
}
.card-value.bad {
  color: #b91c1c;
}
.card-sub {
  color: #777;
  font-size: 0.75rem;
  margin-top: 2px;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}
.table th,
.table td {
  text-align: left;
  padding: 6px 8px;
  border-bottom: 1px solid #eee;
}
.muted {
  color: #666;
  font-size: 0.9rem;
}
.error-list {
  margin: 0;
  padding-left: 18px;
  font-size: 0.9rem;
  color: #b91c1c;
}
@media (max-width: 360px) {
  .cards {
    grid-template-columns: repeat(2, 1fr);
  }
  .card-value {
    font-size: 1.05rem;
  }
}
</style>
