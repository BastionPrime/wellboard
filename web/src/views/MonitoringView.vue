<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { t } from '../i18n'

// Monitoring tab (FR-8): the vendored-by-script metacubexd dist is
// served by the backend at /ui/metacubexd/ (auth-gated in prod, open
// in dev — DECISIONS D18/D19). The SPA embeds it in an iframe.
//
// The mihomo API is NOT exposed to the browser directly: WellBoard
// proxies /api/mihomo/* → 127.0.0.1:<controller> with the secret
// server-side (initial TZ 5.9). metacubexd's backend URL therefore
// points at the WELLBOARD origin path /api/mihomo — the dist's
// endpoint form accepts a full URL, so we prefill via config.js
// served by the backend (config.js is the dist's own hook:
// window.__METACUBEXD_CONFIG__.defaultBackendURL).
//
// If the dist is absent the tab explains how to fetch it (no dead
// iframe).
const distPresent = ref<boolean | null>(null)
const iframeSrc = '/ui/metacubexd/'

onMounted(async () => {
  try {
    const res = await fetch('/ui/metacubexd/', { method: 'HEAD' })
    distPresent.value = res.ok
  } catch {
    distPresent.value = false
  }
})

const loading = computed(() => distPresent.value === null)
</script>

<template>
  <section>
    <h1>{{ t('monitoring.title') }}</h1>
    <p class="muted">{{ t('monitoring.subtitle') }}</p>

    <div v-if="loading" class="muted">{{ t('common.loading') }}</div>

    <div v-else-if="distPresent" class="frame-wrap">
      <iframe
        :src="iframeSrc"
        class="frame"
        :title="t('monitoring.title')"
        referrerpolicy="same-origin"
      />
    </div>

    <div v-else class="stub">
      <h2>{{ t('monitoring.missingTitle') }}</h2>
      <p>{{ t('monitoring.missingText') }}</p>
      <pre class="cmd">scripts/fetch-metacubexd.sh</pre>
      <p class="hint">{{ t('monitoring.hint') }}</p>
    </div>
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
.frame-wrap {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  overflow: hidden;
  background: #fff;
}
.frame {
  width: 100%;
  height: 72vh;
  min-height: 420px;
  border: 0;
  display: block;
}
.stub {
  border: 1px dashed #d97706;
  border-radius: 8px;
  background: #fffbeb;
  padding: 14px;
  max-width: 560px;
}
.stub h2 {
  margin: 0 0 6px;
  font-size: 1rem;
  color: #92400e;
}
.stub p {
  color: #78350f;
  font-size: 0.88rem;
  margin: 4px 0;
}
.cmd {
  background: #fef3c7;
  border-radius: 6px;
  padding: 8px 10px;
  font-size: 0.85rem;
  color: #78350f;
  overflow-x: auto;
}
.hint {
  font-size: 0.78rem;
  color: #a16207;
}
</style>
