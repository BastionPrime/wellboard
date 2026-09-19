<script setup lang="ts">
import { reactive, ref } from 'vue'
import { t } from '../i18n'
import { useSettingsStore } from '../stores/settings'
import { usePoolStore } from '../stores/pool'
import type { Target, TargetType } from '../api'

// Settings (FR-9.1): language, UI port, geodata, delay interval, default
// policy + read-only profile preview (FR-4.9, GET /api/v1/profile → YAML).
const settings = useSettingsStore()
const pool = usePoolStore()
const message = ref('')
const error = ref('')
const profileYaml = ref('')

const form = reactive({
  ui_port: 0,
  geodata: 'runetfreedom' as 'runetfreedom' | 'metacubex',
  delay_test_interval_sec: 0,
  default_policy: 'direct',
})

function initForm() {
  if (!settings.settings) return
  form.ui_port = settings.settings.ui_port
  form.geodata = settings.settings.geodata
  form.delay_test_interval_sec = settings.settings.delay_test_interval_sec
  const dp = settings.settings.default_policy
  form.default_policy = dp.id ?? dp.type
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
      geodata: form.geodata,
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
</script>

<template>
  <section>
    <h1>{{ t('settings.title') }}</h1>
    <p class="muted">{{ t('settings.subtitle') }}</p>
    <p v-if="message" class="ok">{{ message }}</p>
    <p v-if="error" class="error">{{ error }}</p>

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
        <span>{{ t('settings.geodata') }}</span>
        <select v-model="form.geodata">
          <option value="runetfreedom">{{ t('settings.geodata.runetfreedom') }}</option>
          <option value="metacubex">{{ t('settings.geodata.metacubex') }}</option>
        </select>
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

    <h2>{{ t('settings.profile') }}</h2>
    <p class="muted">{{ t('settings.profileHint') }}</p>
    <button class="secondary" @click="loadProfile">{{ t('common.refresh') }}</button>
    <pre v-if="profileYaml" class="profile">{{ profileYaml }}</pre>
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

