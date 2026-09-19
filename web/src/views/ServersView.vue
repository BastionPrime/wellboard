<script setup lang="ts">
import { ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore } from '../stores/pool'
import { useSourcesStore } from '../stores/sources'

// Servers (FR-3): read-only pool view with delay and stale flags.
// Honest about the API: no per-server latency-test endpoint exists in
// phase 3 (the scheduler runs periodic group tests); we show the last
// measured delay_ms and say so.
const pool = usePoolStore()
const sources = useSourcesStore()
const search = ref('')

const filtered = () => {
  const q = search.value.trim().toLowerCase()
  if (!q) return pool.servers
  return pool.servers.filter((s) => s.name.toLowerCase().includes(q))
}

function sourceName(id: string): string {
  return sources.sources.find((s) => s.id === id)?.name ?? id
}
</script>

<template>
  <section>
    <h1>{{ t('servers.title') }}</h1>
    <p class="muted">{{ t('servers.subtitle') }}</p>

    <p class="note">{{ t('servers.delayTestNote') }}</p>

    <input v-model="search" class="search" :placeholder="t('servers.search')" />

    <table v-if="pool.servers.length" class="table">
      <thead>
        <tr>
          <th>{{ t('sources.name') }}</th>
          <th>{{ t('servers.delay') }}</th>
          <th>{{ t('sources.kind.manual') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in filtered()" :key="s.id" :class="{ stale: s.stale }">
          <td>
            <div>{{ s.name }}</div>
            <div class="sub">{{ s.type }} · {{ t('servers.bySource', { name: sourceName(s.source_id) }) }}</div>
            <div v-if="s.stale" class="sub err">⚠ {{ t('servers.stale') }} — {{ t('servers.staleHint') }}</div>
          </td>
          <td>
            <template v-if="s.delay_ms">{{ s.delay_ms }} ms</template>
            <template v-else>{{ t('servers.delayNotTested') }}</template>
          </td>
          <td class="actions">
            <button class="danger" @click="pool.deleteServer(s.id).catch(() => {})">{{ t('common.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">{{ t('servers.noServers') }}</p>
  </section>
</template>

<style scoped>
h1 {
  margin: 0 0 4px;
  font-size: 1.3rem;
}
.muted {
  color: #666;
  font-size: 0.9rem;
  margin: 4px 0 8px;
}
.note {
  color: #888;
  font-size: 0.8rem;
  margin: 0 0 10px;
}
.search {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  width: 100%;
  max-width: 320px;
  margin-bottom: 12px;
  font-size: 0.95rem;
  box-sizing: border-box;
}
.table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.9rem;
}
.table th,
.table td {
  text-align: left;
  padding: 8px;
  border-bottom: 1px solid #eee;
  vertical-align: top;
}
tr.stale td {
  background: #fff7ed;
}
.sub {
  color: #888;
  font-size: 0.78rem;
}
.sub.err {
  color: #b45309;
}
.actions {
  text-align: right;
}
.danger {
  background: #fff;
  color: #b91c1c;
  border: 1px solid #b91c1c;
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 0.85rem;
  cursor: pointer;
}
@media (max-width: 360px) {
  .table th:nth-child(3),
  .table td:nth-child(3) {
    display: none;
  }
  .search {
    max-width: none;
  }
}
</style>
