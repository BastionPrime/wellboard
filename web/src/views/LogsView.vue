<script setup lang="ts">
import { t } from '../i18n'

// Logs & diagnostics screen (FR-9.2/FR-9.4). HONEST STUB: the phase 3/4
// REST API (internal/api/api.go) has NO log or diagnostics endpoints —
// no GET /logs, no nikki/mihomo probes. Rather than fake buttons that
// call nothing, this screen states plainly that log viewing and the
// "check nikki / mihomo API / geodata" diagnostics arrive with the
// monitoring phase (TZ §7 "Фаза 5 — Мониторинг и применение", which
// owns the mihomo-facing runtime surface). Recorded in DECISIONS (D17).
</script>

<template>
  <section>
    <h1>{{ t('logs.title') }}</h1>
    <p class="muted">{{ t('logs.subtitle') }}</p>

    <div class="stub">
      <h2>{{ t('logs.phase5Title') }}</h2>
      <p>{{ t('logs.phase5Text') }}</p>
      <ul>
        <li>{{ t('logs.item.logs') }}</li>
        <li>{{ t('logs.item.checkNikki') }}</li>
        <li>{{ t('logs.item.checkMihomo') }}</li>
        <li>{{ t('logs.item.checkGeodata') }}</li>
      </ul>
      <p class="hint">{{ t('logs.hint') }}</p>
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
.stub {
  border: 1px dashed #d97706;
  border-radius: 8px;
  background: #fffbeb;
  padding: 14px;
  max-width: 520px;
}
.stub h2 {
  margin: 0 0 6px;
  font-size: 1rem;
  color: #92400e;
}
.stub p,
.stub li {
  color: #78350f;
  font-size: 0.88rem;
  margin: 4px 0;
}
.stub ul {
  margin: 6px 0;
  padding-left: 20px;
}
.hint {
  font-size: 0.78rem;
  color: #a16207;
}
@media (max-width: 360px) {
  .stub {
    padding: 10px;
  }
}
</style>
