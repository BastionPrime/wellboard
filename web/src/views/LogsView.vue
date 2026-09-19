<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { t } from '../i18n'
import { api, type Logs, type Diagnostics, type DiagCheck } from '../api'

// Logs & diagnostics screen (FR-9.2 / FR-9.4): the app log tail
// (GET /api/v1/logs — last 500 lines) and the three diagnostics
// checks (nikki, mihomo API, geodata) with a manual "run" button.
// The log view auto-refreshes (5 s) while mounted.
const lines = ref<string[]>([])
const logError = ref('')
const logsUnavailable = ref(false)
const checks = ref<DiagCheck[]>([])
const checking = ref(false)
const diagError = ref('')
const auto = ref(true)

let timer: ReturnType<typeof setInterval> | null = null

async function loadLogs() {
  try {
    const res = await api<Logs>('/logs')
    lines.value = res.lines ?? []
    logError.value = ''
    logsUnavailable.value = false
  } catch (e) {
    logsUnavailable.value = true
    logError.value = e instanceof Error ? e.message : String(e)
  }
}

async function runChecks() {
  checking.value = true
  diagError.value = ''
  try {
    const res = await api<Diagnostics>('/diagnostics')
    checks.value = res.checks ?? []
  } catch (e) {
    diagError.value = e instanceof Error ? e.message : String(e)
  } finally {
    checking.value = false
  }
}

onMounted(() => {
  loadLogs()
  runChecks()
  timer = setInterval(() => {
    if (auto.value) loadLogs()
  }, 5000)
})

onUnmounted(() => {
  if (timer) clearInterval(timer)
})

// checkLabel maps the backend check names to locale keys.
const checkLabel = (name: string) => {
  switch (name) {
    case 'nikki':
      return t('logs.item.checkNikki')
    case 'mihomo':
      return t('logs.item.checkMihomo')
    case 'geodata':
      return t('logs.item.checkGeodata')
    default:
      return name
  }
}
</script>

<template>
  <section>
    <h1>{{ t('logs.title') }}</h1>
    <p class="muted">{{ t('logs.subtitle') }}</p>

    <h2>{{ t('logs.diagnTitle') }}</h2>
    <div class="diag">
      <button class="btn" type="button" :disabled="checking" @click="runChecks">
        {{ checking ? t('logs.checking') : t('common.refresh') }}
      </button>
      <p v-if="diagError" class="err">{{ diagError }}</p>
      <div v-for="c in checks" :key="c.name" class="check" :class="c.ok ? 'ok' : 'bad'">
        <div class="check-head">
          <span class="dot" :class="c.ok ? 'ok' : 'bad'" />
          <strong>{{ checkLabel(c.name) }}</strong>
          <span v-if="c.measure" class="measure">{{ c.measure }}</span>
        </div>
        <div v-if="c.detail" class="detail">{{ c.detail }}</div>
      </div>
    </div>

    <h2>{{ t('logs.appTitle') }}</h2>
    <div class="log-controls">
      <label class="toggle">
        <input v-model="auto" type="checkbox" />
        {{ t('logs.autoRefresh') }}
      </label>
      <button class="btn" type="button" @click="loadLogs">{{ t('common.refresh') }}</button>
    </div>
    <p v-if="logsUnavailable" class="err">{{ t('logs.unavailable') }}: {{ logError }}</p>
    <pre v-else class="log">{{ lines.length ? lines.join('\n') : t('logs.empty') }}</pre>
  </section>
</template>

<style scoped>
h1 {
  margin: 0 0 4px;
  font-size: 1.3rem;
}
h2 {
  margin: 18px 0 8px;
  font-size: 1.05rem;
}
.muted {
  color: #666;
  font-size: 0.9rem;
  margin: 4px 0 12px;
}
.diag {
  max-width: 560px;
}
.check {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 8px 10px;
  margin: 6px 0;
  font-size: 0.88rem;
}
.check.ok {
  border-color: #bbf7d0;
  background: #f0fdf4;
}
.check.bad {
  border-color: #fecaca;
  background: #fef2f2;
}
.check-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  display: inline-block;
}
.dot.ok {
  background: #16a34a;
}
.dot.bad {
  background: #dc2626;
}
.measure {
  color: #888;
  font-size: 0.78rem;
  margin-left: auto;
}
.detail {
  color: #555;
  font-size: 0.8rem;
  margin-top: 4px;
  word-break: break-all;
}
.log-controls {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 8px 0;
}
.toggle {
  font-size: 0.85rem;
  color: #444;
  display: flex;
  align-items: center;
  gap: 6px;
}
.btn {
  border: 1px solid #ccc;
  background: #f6f6f6;
  border-radius: 6px;
  padding: 5px 12px;
  font-size: 0.85rem;
  cursor: pointer;
}
.btn:active {
  background: #e8e8e8;
}
.btn:disabled {
  opacity: 0.6;
  cursor: default;
}
.log {
  background: #1e1e1e;
  color: #d4d4d4;
  border-radius: 8px;
  padding: 10px 12px;
  font-size: 0.78rem;
  line-height: 1.45;
  max-height: 420px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
  margin: 0;
}
.err {
  color: #b91c1c;
  font-size: 0.85rem;
}
</style>
