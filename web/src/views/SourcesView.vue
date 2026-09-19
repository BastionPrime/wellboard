<script setup lang="ts">
import { reactive, ref } from 'vue'
import { t } from '../i18n'
import { useSourcesStore } from '../stores/sources'

// Sources (FR-1): list + add subscription + enable toggle + delete.
// Honest about the API: there is no "refresh subscription now" endpoint
// (scheduler owns updates) — the refresh button re-reads the list only.
const sources = useSourcesStore()
const form = reactive({ name: '', url: '', interval: 0 })
const addError = ref('')
const busy = ref(false)

async function add() {
  addError.value = ''
  busy.value = true
  try {
    await sources.create({
      kind: 'subscription',
      name: form.name,
      url: form.url,
      update_interval_sec: form.interval || undefined,
    })
    form.name = ''
    form.url = ''
    form.interval = 0
  } catch (e) {
    addError.value = t('sources.addFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    busy.value = false
  }
}

async function remove(id: string) {
  try {
    await sources.remove(id)
  } catch (e) {
    addError.value = t('sources.deleteConflict')
  }
}

async function toggle(id: string, enabled: boolean) {
  await sources.setEnabled(id, enabled).catch(() => {})
}

function lastUpdate(s: { last_update?: string }): string {
  if (!s.last_update) return t('sources.never')
  return s.last_update
}
</script>

<template>
  <section>
    <h1>{{ t('sources.title') }}</h1>
    <p class="muted">{{ t('sources.subtitle') }}</p>

    <form class="add-form" @submit.prevent="add">
      <label>
        <span>{{ t('sources.name') }}</span>
        <input v-model="form.name" required maxlength="64" autocomplete="off" />
      </label>
      <label>
        <span>{{ t('sources.url') }}</span>
        <input v-model="form.url" required placeholder="https://…" autocomplete="off" />
      </label>
      <label>
        <span>{{ t('sources.interval') }}</span>
        <input v-model.number="form.interval" type="number" min="0" step="60" />
        <small>{{ t('sources.intervalHint') }}</small>
      </label>
      <button type="submit" :disabled="busy || !form.name || !form.url">{{ t('common.add') }}</button>
    </form>
    <p v-if="addError" class="error">{{ addError }}</p>

    <table v-if="sources.sources.length" class="table">
      <thead>
        <tr>
          <th>{{ t('sources.name') }}</th>
          <th>{{ t('sources.lastUpdate') }}</th>
          <th>{{ t('sources.hwid') }}</th>
          <th></th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="s in sources.sources" :key="s.id" :class="{ off: !s.enabled }">
          <td>
            <div>{{ s.name }}</div>
            <div class="sub">{{ t('sources.kind.' + s.kind) }}</div>
            <div v-if="s.last_error" class="sub err">{{ s.last_error }}</div>
          </td>
          <td>{{ lastUpdate(s) }}</td>
          <td>
            <template v-if="s.hwid_status?.limit_reached">⚠ {{ t('sources.hwidLimit') }}</template>
            <template v-else-if="s.hwid_status?.not_supported">{{ t('sources.hwidNotSupported') }}</template>
            <template v-else-if="s.hwid_status?.active">{{ t('common.yes') }}</template>
            <template v-else>{{ t('common.none') }}</template>
          </td>
          <td class="actions">
            <label class="switch">
              <input type="checkbox" :checked="s.enabled" @change="toggle(s.id, ($event.target as HTMLInputElement).checked)" />
              <span>{{ s.enabled ? t('common.on') : t('common.off') }}</span>
            </label>
            <button class="danger" @click="remove(s.id)">{{ t('common.delete') }}</button>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">{{ t('sources.noSources') }}</p>

    <p class="note">{{ t('sources.refreshNote') }}</p>
    <button class="secondary" @click="sources.load()">{{ t('common.refresh') }}</button>
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
  margin: 4px 0 12px;
}
.add-form {
  display: grid;
  gap: 8px;
  max-width: 480px;
  margin-bottom: 16px;
}
.add-form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.add-form input {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.95rem;
}
.add-form small {
  color: #888;
}
button {
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.95rem;
}
button:disabled {
  opacity: 0.5;
}
.secondary {
  background: #f6f6f6;
  color: #333;
  border-color: #ccc;
}
.danger {
  background: #fff;
  color: #b91c1c;
  border-color: #b91c1c;
  padding: 4px 10px;
  font-size: 0.85rem;
}
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.note {
  color: #888;
  font-size: 0.8rem;
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
tr.off td {
  color: #999;
}
.sub {
  color: #888;
  font-size: 0.78rem;
}
.sub.err {
  color: #b91c1c;
}
.actions {
  text-align: right;
  white-space: nowrap;
}
.switch {
  margin-right: 10px;
  font-size: 0.8rem;
  color: #555;
}
@media (max-width: 360px) {
  .table th:nth-child(2) {
    display: none;
  }
  .table td:nth-child(2) {
    display: none;
  }
  .actions {
    display: flex;
    flex-direction: column;
    gap: 4px;
    align-items: flex-start;
  }
}
</style>
