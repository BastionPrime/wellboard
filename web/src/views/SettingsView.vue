<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { t } from '../i18n'
import { useSettingsStore } from '../stores/settings'
import { useSourcesStore } from '../stores/sources'
import { usePoolStore } from '../stores/pool'
import { importNikkiSources, type Target, type TargetType, type GeodataSource } from '../api'

// Settings (FR-9.1): language, UI port, geodata, delay interval, default
// policy + read-only profile preview (FR-4.9, GET /api/v1/profile → YAML)
// + state export/import (FR-6.5, Phase 7).
// the first-run wizard lives here as an explicit button
// (auto-redirect on empty state removed from router.ts).
// B2: geodata is split per kind (geosite/geoip), each with
// its own custom URL option, plus the geodata additions editor.
const router = useRouter()
const settings = useSettingsStore()
const sourcesStore = useSourcesStore()
const pool = usePoolStore()
const message = ref('')
const error = ref('')
const profileYaml = ref('')
const importMsg = ref('')

// A3: masked sources list + nikki import. The full URL never
// reaches this screen (masked_url only, secret rule).
const sourcesSummary = ref<{ id: string; name: string; kind: string; masked_url: string; enabled: boolean }[]>([])
const importResult = ref('')
const importError = ref('')
const importing = ref(false)

async function loadSourcesSummary() {
  // GET /settings now carries sources_summary; the store reload also
  // refreshes the settings form state.
  await settings.load().then(initForm).catch(() => {})
  sourcesSummary.value = settings.settings?.sources_summary ?? []
}

async function importFromNikki() {
  importResult.value = ''
  importError.value = ''
  importing.value = true
  try {
    const res = await importNikkiSources()
    importResult.value = t('settings.importedCount', { n: res.imported, total: res.found })
    await loadSourcesSummary()
    await sourcesStore.load().catch(() => {})
  } catch (e) {
    importError.value = t('settings.importFailedMsg', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    importing.value = false
  }
}

async function toggleSource(row: { id: string; enabled: boolean }) {
  try {
    await sourcesStore.setEnabled(row.id, !row.enabled)
    row.enabled = !row.enabled
  } catch (e) {
    importError.value = t('settings.importFailedMsg', { msg: e instanceof Error ? e.message : String(e) })
  }
}

loadSourcesSummary()

const form = reactive({
  ui_port: 0,
  geosite_source: 'runetfreedom' as GeodataSource,
  geoip_source: 'runetfreedom' as GeodataSource,
  geosite_custom_url: '',
  geoip_custom_url: '',
  delay_test_interval_sec: 0,
  default_policy: 'direct',
})

// Additions rows: {key: "geosite:<cat>"|"geoip:<cat>", values: text}.
const additionRows = ref<{ key: string; values: string }[]>([])

function initForm() {
  if (!settings.settings) return
  form.ui_port = settings.settings.ui_port
  form.geosite_source = settings.settings.geosite_source ?? 'runetfreedom'
  form.geoip_source = settings.settings.geoip_source ?? 'runetfreedom'
  form.geosite_custom_url = settings.settings.geosite_custom_url ?? ''
  form.geoip_custom_url = settings.settings.geoip_custom_url ?? ''
  form.delay_test_interval_sec = settings.settings.delay_test_interval_sec
  const dp = settings.settings.default_policy
  form.default_policy = dp.id ?? dp.type
  // Additions map → editable rows (sorted for stable display).
  const additions = settings.settings.geodata_additions ?? {}
  additionRows.value = Object.keys(additions)
    .sort()
    .map((key) => ({ key, values: additions[key].join('\n') }))
}

function addAdditionRow() {
  additionRows.value.push({ key: 'geosite:', values: '' })
}
function removeAdditionRow(i: number) {
  additionRows.value.splice(i, 1)
}

// buildAdditions serializes the rows back into the map the API expects
// (entries split by newline, blank lines dropped; keys kept verbatim —
// the server validates them).
function buildAdditions(): Record<string, string[]> | undefined {
  const out: Record<string, string[]> = {}
  for (const row of additionRows.value) {
    const key = row.key.trim()
    if (!key) continue
    const entries = row.values
      .split('\n')
      .map((v) => v.trim())
      .filter(Boolean)
    if (entries.length) out[key] = entries
  }
  return out
}
initForm()
if (!settings.settings) settings.load().then(initForm)

function policyValue(): Target {
  if (form.default_policy === 'direct' || form.default_policy === 'reject') {
    return { type: form.default_policy as TargetType }
  }
  const g = pool.groups.find((x) => x.id === form.default_policy)
  if (g) return { type: 'group', id: form.default_policy }
  return { type: 'server', id: form.default_policy }
}

async function save() {
  message.value = ''
  error.value = ''
  try {
    await settings.patch({
      ui_port: form.ui_port,
      geosite_source: form.geosite_source,
      geoip_source: form.geoip_source,
      geosite_custom_url: form.geosite_custom_url.trim(),
      geoip_custom_url: form.geoip_custom_url.trim(),
      geodata_additions: buildAdditions(),
      delay_test_interval_sec: form.delay_test_interval_sec,
      default_policy: policyValue(),
    })
    message.value = t('settings.saved')
  } catch (e) {
    error.value = t('settings.saveFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

async function loadProfile() {
  profileYaml.value = t('common.loading')
  try {
    const res = await fetch('/api/v1/profile')
    profileYaml.value = res.ok ? await res.text() : 'HTTP ' + res.status + ': ' + (await res.text())
  } catch (e) {
    profileYaml.value = String(e)
  }
}

// Import (FR-6.5): POST the chosen file to /api/v1/import and reload
// the whole app state (the backend validates before replacing).
async function importState(ev: Event) {
  const file = (ev.target as HTMLInputElement).files?.[0]
  if (!file) return
  importMsg.value = t('common.loading')
  try {
    const body = await file.text()
    const res = await fetch('/api/v1/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body,
    })
    const text = await res.text()
    if (!res.ok) throw new Error(text || 'HTTP ' + res.status)
    importMsg.value = t('settings.importDone')
    settings.load().then(initForm)
    pool.loadAll()
  } catch (e) {
    importMsg.value = t('settings.importFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
  ;(ev.target as HTMLInputElement).value = ''
}
</script>

<template>
  <section>
    <h1>{{ t('settings.title') }}</h1>
    <p class="muted">{{ t('settings.subtitle') }}</p>
    <p v-if="message" class="ok">{{ message }}</p>
    <p v-if="error" class="error">{{ error }}</p>

    <h2>{{ t('settings.wizardTitle') }}</h2>
    <p class="muted">{{ t('settings.wizardHint') }}</p>
    <button class="secondary" @click="router.push('/wizard')">{{ t('settings.wizardButton') }}</button>

    <form v-if="settings.settings" class="form" @submit.prevent="save">
      <label>
        <span>{{ t('settings.language') }}</span>
        <select :value="settings.lang" @change="settings.setLang(($event.target as HTMLSelectElement).value as 'ru' | 'en')">
          <option value="ru">Русский</option>
          <option value="en">English</option>
        </select>
      </label>
      <label>
        <span>{{ t('settings.uiPort') }} ({{ settings.settings.ui_port }})</span>
        <input v-model.number="form.ui_port" type="number" min="1" max="65535" />
        <small>{{ t('settings.uiPortHint') }}</small>
      </label>
      <label>
        <span>{{ t('settings.geositeSource') }}</span>
        <select v-model="form.geosite_source">
          <option value="runetfreedom">{{ t('settings.geodata.runetfreedom') }}</option>
          <option value="metacubex">{{ t('settings.geodata.metacubex') }}</option>
          <option value="custom">{{ t('settings.geodata.custom') }}</option>
        </select>
      </label>
      <label v-if="form.geosite_source === 'custom'">
        <span>{{ t('settings.geositeCustomUrl') }}</span>
        <input v-model="form.geosite_custom_url" placeholder="https://…" autocomplete="off" />
      </label>
      <label>
        <span>{{ t('settings.geoipSource') }}</span>
        <select v-model="form.geoip_source">
          <option value="runetfreedom">{{ t('settings.geodata.runetfreedom') }}</option>
          <option value="metacubex">{{ t('settings.geodata.metacubex') }}</option>
          <option value="custom">{{ t('settings.geodata.custom') }}</option>
        </select>
      </label>
      <label v-if="form.geoip_source === 'custom'">
        <span>{{ t('settings.geoipCustomUrl') }}</span>
        <input v-model="form.geoip_custom_url" placeholder="https://…" autocomplete="off" />
      </label>
      <label>
        <span>{{ t('settings.delayInterval') }}</span>
        <input v-model.number="form.delay_test_interval_sec" type="number" min="0" step="60" />
        <small>{{ t('settings.delayIntervalHint') }}</small>
      </label>
      <label>
        <span>{{ t('settings.defaultPolicy') }}</span>
        <select v-model="form.default_policy">
          <option value="direct">DIRECT</option>
          <option value="reject">REJECT</option>
          <option v-for="g in pool.groups" :key="g.id" :value="g.id">{{ g.name }} (group)</option>
          <option v-for="s in pool.servers" :key="s.id" :value="s.id">{{ s.name }}</option>
        </select>
        <small>{{ t('settings.defaultPolicyHint') }}</small>
      </label>
      <button type="submit">{{ t('common.save') }}</button>
    </form>
    <p v-else class="muted">{{ t('common.loading') }}</p>

    <!-- B2: geodata additions (appended domains/CIDRs per
         category; the generator emits them after the matching rule). -->
    <div class="additions">
      <h2>{{ t('settings.additionsTitle') }}</h2>
      <p class="muted">{{ t('settings.additionsHint') }}</p>
      <div v-for="(row, i) in additionRows" :key="i" class="add-row">
        <input v-model="row.key" class="add-key" placeholder="geosite:youtube" autocomplete="off" />
        <textarea
          v-model="row.values"
          class="add-values"
          rows="3"
          :placeholder="t('settings.additionsPlaceholder')"
        ></textarea>
        <button type="button" class="secondary add-del" @click="removeAdditionRow(i)">×</button>
      </div>
      <button type="button" class="secondary" @click="addAdditionRow">
        {{ t('settings.addAdditionRow') }}
      </button>
      <p class="muted small-hint">{{ t('settings.additionsNote') }}</p>
    </div>

    <h2>{{ t('settings.sourcesTitle') }}</h2>
    <p class="muted">{{ t('settings.sourcesHint') }}</p>
    <button class="secondary" :disabled="importing" @click="importFromNikki">
      {{ importing ? t('common.loading') : t('settings.importFromNikki') }}
    </button>
    <p v-if="importResult" class="ok">{{ importResult }}</p>
    <p v-if="importError" class="error">{{ importError }}</p>
    <table v-if="sourcesSummary.length" class="sources-table">
      <thead>
        <tr>
          <th>{{ t('sources.name') }}</th>
          <th>{{ t('sources.url') }}</th>
          <th>{{ t('common.enabled') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="row in sourcesSummary" :key="row.id">
          <td>{{ row.name }}</td>
          <td class="masked">{{ row.masked_url }}</td>
          <td>
            <label class="switch">
              <input type="checkbox" :checked="row.enabled" @change="toggleSource(row)" />
              <span>{{ row.enabled ? t('common.on') : t('common.off') }}</span>
            </label>
          </td>
        </tr>
      </tbody>
    </table>
    <p v-else class="muted">{{ t('settings.noSources') }}</p>

    <h2>{{ t('settings.profile') }}</h2>
    <p class="muted">{{ t('settings.profileHint') }}</p>
    <button class="secondary" @click="loadProfile">{{ t('common.refresh') }}</button>
    <pre v-if="profileYaml" class="profile">{{ profileYaml }}</pre>

    <h2>{{ t('settings.backup') }}</h2>
    <p class="muted">{{ t('settings.backupHint') }}</p>
    <a class="button" href="/api/v1/export" download>{{ t('settings.exportState') }}</a>
    <label class="import">
      <span>{{ t('settings.importState') }}</span>
      <input type="file" accept="application/json" @change="importState" />
      <small v-if="importMsg">{{ importMsg }}</small>
    </label>
  </section>
</template>

<style scoped>
h1 {
  margin: 0 0 4px;
  font-size: 1.3rem;
}
h2 {
  margin: 22px 0 6px;
  font-size: 1.05rem;
}
.muted {
  color: #666;
  font-size: 0.9rem;
}
.ok {
  color: #15803d;
  font-size: 0.9rem;
}
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.form {
  display: grid;
  gap: 10px;
  max-width: 420px;
}
.form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.form input,
.form select {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.95rem;
  background: #fff;
}
.form small {
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
.secondary {
  background: #f6f6f6;
  color: #333;
  border-color: #ccc;
}
.button {
  display: inline-block;
  margin-top: 4px;
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.95rem;
  text-decoration: none;
}
.import {
  display: grid;
  gap: 3px;
  margin-top: 10px;
  max-width: 420px;
  font-size: 0.85rem;
  color: #444;
}
.import small {
  color: #15803d;
  word-break: break-all;
}
.sources-table {
  margin-top: 10px;
  border-collapse: collapse;
  max-width: 560px;
  font-size: 0.9rem;
}
.sources-table th,
.sources-table td {
  text-align: left;
  padding: 6px 10px;
  border-bottom: 1px solid #e2e2e2;
}
.sources-table th {
  color: #666;
  font-weight: 600;
  font-size: 0.8rem;
}
.sources-table .masked {
  color: #555;
  font-family: monospace;
  font-size: 0.8rem;
}
.switch {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  font-size: 0.85rem;
  color: #444;
}
.switch input {
  cursor: pointer;
}
.additions {
  margin-top: 6px;
  max-width: 560px;
}
.add-row {
  display: grid;
  grid-template-columns: 160px 1fr 34px;
  gap: 6px;
  margin-bottom: 8px;
  align-items: start;
}
.add-key {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.85rem;
  font-family: monospace;
}
.add-values {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.85rem;
  font-family: monospace;
  resize: vertical;
}
.add-del {
  padding: 8px 0;
}
.small-hint {
  margin-top: 6px;
  font-size: 0.8rem;
}
.profile {
  margin-top: 10px;
  background: #f8f8f8;
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 10px;
  font-size: 0.75rem;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>

