<script setup lang="ts">
import { reactive, ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore } from '../stores/pool'
import { useSourcesStore } from '../stores/sources'

// Servers (FR-3): read-only pool view with delay and stale flags, plus
// manual server entry (FR-1.3): paste one or more share links
// (vless://, trojan://, ss://, vmess://, hysteria2://, tuic://) — the
// server parses them with the mihomo converter (POST /api/v1/servers).
// Honest about the API: no per-server latency-test endpoint exists in
// phase 3 (the scheduler runs periodic group tests); we show the last
// measured delay_ms and say so.
const pool = usePoolStore()
const sources = useSourcesStore()
const search = ref('')
const addError = ref('')
const addOk = ref('')
const adding = ref(false)

const form = reactive({ links: '' })

const filtered = () => {
  const q = search.value.trim().toLowerCase()
  if (!q) return pool.servers
  return pool.servers.filter((s) => s.name.toLowerCase().includes(q))
}

function sourceName(id: string): string {
  return sources.sources.find((s) => s.id === id)?.name ?? id
}

async function addManual() {
  addError.value = ''
  addOk.value = ''
  const value = form.links.trim()
  if (!value) return
  adding.value = true
  try {
    const created = await pool.addManualServers(value)
    addOk.value = t('servers.manualAdded', { n: created.length })
    form.links = ''
  } catch (e) {
    addError.value = t('servers.manualFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    adding.value = false
  }
}
</script>

<template>
  <section>
    <h1>{{ t('servers.title') }}</h1>
    <p class="muted">{{ t('servers.subtitle') }}</p>

    <p class="note">{{ t('servers.delayTestNote') }}</p>

    <form class="manual-form" @submit.prevent="addManual">
      <label>
        <span>{{ t('servers.manualTitle') }}</span>
        <textarea
          v-model="form.links"
          rows="3"
          :placeholder="t('servers.manualPlaceholder')"
          class="links"
        ></textarea>
        <small>{{ t('servers.manualHint') }}</small>
      </label>
      <button type="submit" class="add" :disabled="adding || !form.links.trim()">
        {{ adding ? t('common.loading') : t('common.add') }}
      </button>
    </form>
    <p v-if="addOk" class="ok">{{ addOk }}</p>
    <p v-if="addError" class="error">{{ addError }}</p>

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
.manual-form {
  display: grid;
  gap: 6px;
  max-width: 480px;
  margin: 0 0 14px;
  padding: 10px;
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  background: #fafafa;
}
.manual-form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.manual-form small {
  color: #888;
  font-size: 0.75rem;
}
.links {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.85rem;
  font-family: monospace;
  word-break: break-all;
  resize: vertical;
  box-sizing: border-box;
  width: 100%;
}
.add {
  justify-self: start;
  padding: 6px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.9rem;
}
.add:disabled {
  opacity: 0.5;
}
.ok {
  color: #15803d;
  font-size: 0.88rem;
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
