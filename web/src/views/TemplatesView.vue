<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { t } from '../i18n'
import { usePoolStore } from '../stores/pool'
import type { Target } from '../api'

// Templates (FR-5): catalog + apply with target selection.
// The "all-vpn" template sets the default policy (api.go special-case).
const pool = usePoolStore()
const selected = ref<Record<string, string>>({})
const applying = ref<string | null>(null)
const message = ref('')
const error = ref('')

// Prefill the per-template target picker with the template's typical
// target (direct/reject literal, else the first group/server).
onMounted(() => {
  for (const tpl of pool.templates) {
    if (selected.value[tpl.id] !== undefined) continue
    selected.value[tpl.id] = initialTarget(tpl.typical_target)
  }
})

function initialTarget(typical: string): string {
  if (typical === 'direct' || typical === 'reject') return typical
  const opts = pool.targets.filter((o) => o.value !== 'direct' && o.value !== 'reject')
  return opts.length ? opts[0].value : 'direct'
}

function targetPayload(value: string): Target {
  if (value === 'direct' || value === 'reject') return { type: value }
  const g = pool.groups.find((x) => x.id === value)
  if (g) return { type: 'group', id: value }
  return { type: 'server', id: value }
}

async function apply(tplId: string, name: string) {
  error.value = ''
  message.value = ''
  applying.value = tplId
  try {
    const out = await pool.applyTemplate(tplId, targetPayload(selected.value[tplId] ?? 'direct'))
    if (out.mode === 'default-policy') {
      message.value = t('templates.appliedPolicy')
    } else {
      message.value = t('templates.applied', { name })
    }
  } catch (e) {
    error.value = t('templates.applyFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    applying.value = null
  }
}
</script>

<template>
  <section>
    <h1>{{ t('templates.title') }}</h1>
    <p class="muted">{{ t('templates.subtitle') }}</p>
    <p v-if="message" class="ok">{{ message }}</p>
    <p v-if="error" class="error">{{ error }}</p>
    <p v-if="pool.error && !pool.templates.length" class="error">{{ t('templates.unavailable') }}</p>

    <div class="tpl-list">
      <div v-for="tpl in pool.templates" :key="tpl.id" class="tpl">
        <div class="tpl-head">
          <strong>{{ tpl.name }}</strong>
          <span v-if="tpl.id === 'all-vpn'" class="badge">MATCH</span>
        </div>
        <p class="tpl-desc">{{ tpl.description }}</p>
        <p v-if="tpl.providers?.length" class="tpl-prov">
          {{ t('templates.providers', { list: tpl.providers.join(', ') }) }}
        </p>
        <label class="target-pick">
          <span>{{ t('templates.chooseTarget') }}</span>
          <select v-model="selected[tpl.id]">
            <option v-for="o in pool.targets" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
        </label>
        <button
          class="apply"
          :disabled="applying === tpl.id || !pool.templates.length"
          @click="apply(tpl.id, tpl.name)"
        >
          {{ applying === tpl.id ? t('templates.applying') : t('templates.apply') }}
        </button>
      </div>
    </div>
    <p v-if="!pool.templates.length && !pool.error" class="muted">{{ t('common.loading') }}</p>
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
}
.ok {
  color: #15803d;
  font-size: 0.9rem;
}
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.tpl-list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 10px;
  margin-top: 12px;
}
.tpl {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.tpl-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.badge {
  background: #ede9fe;
  color: #6d28d9;
  font-size: 0.7rem;
  padding: 2px 6px;
  border-radius: 4px;
}
.tpl-desc {
  margin: 0;
  font-size: 0.85rem;
  color: #555;
}
.tpl-prov {
  margin: 0;
  font-size: 0.78rem;
  color: #7c3aed;
}
.target-pick {
  display: grid;
  gap: 3px;
  font-size: 0.8rem;
  color: #444;
}
.target-pick select {
  padding: 6px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.9rem;
}
.apply {
  margin-top: 4px;
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.9rem;
}
.apply:disabled {
  opacity: 0.5;
}
@media (max-width: 360px) {
  .tpl-list {
    grid-template-columns: 1fr;
  }
}
</style>
