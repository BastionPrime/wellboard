<script setup lang="ts">
import { reactive, ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { t } from '../i18n'
import { useSourcesStore } from '../stores/sources'
import { useSettingsStore } from '../stores/settings'
import { usePoolStore } from '../stores/pool'
import type { Target } from '../api'

// First-run wizard (initial TZ 7 phase 4): subscription → default policy
// → first template. Honest simplification: 3 steps, no client-side URL
// validation (server validates); the hint says so. Skippable; the router
// sends here only when the state has no sources.
const router = useRouter()
const sources = useSourcesStore()
const settings = useSettingsStore()
const pool = usePoolStore()

const step = ref(1)
const error = ref('')
const done = ref(false)

// OPE-3045 B1: disabled templates are not offered in the wizard.
const offeredTemplates = computed(() => pool.templates.filter((tpl) => !tpl.disabled))

const form = reactive({
  name: '',
  url: '',
  policy: 'direct' as string,
  template: '',
})

function next() {
  error.value = ''
  step.value = Math.min(3, step.value + 1)
}
function back() {
  error.value = ''
  step.value = Math.max(1, step.value - 1)
}

async function addSubscription(): Promise<boolean> {
  if (!form.name || !form.url) return true // skip: nothing entered
  try {
    await sources.create({ kind: 'subscription', name: form.name, url: form.url })
    return true
  } catch (e) {
    error.value = t('wizard.addFailed', { msg: e instanceof Error ? e.message : String(e) })
    return false
  }
}

async function applyPolicy(): Promise<boolean> {
  try {
    const payload: Target =
      form.policy === 'direct' || form.policy === 'reject'
        ? { type: form.policy }
        : { type: 'group', id: form.policy }
    await settings.patch({ default_policy: payload })
    return true
  } catch (e) {
    error.value = String(e instanceof Error ? e.message : e)
    return false
  }
}

async function applyTemplate(): Promise<boolean> {
  if (!form.template) return true // skip
  try {
    const tpl = pool.templates.find((x) => x.id === form.template)
    const typical = tpl?.typical_target
    let target: Target
    if (form.template === 'all-vpn') {
      // The MATCH-policy template: direct is a safe default here.
      target = { type: 'direct' }
    } else if (typical === 'direct' || typical === 'reject') {
      target = { type: typical }
    } else if (pool.groups.length) {
      target = { type: 'group', id: pool.groups[0].id }
    } else {
      target = { type: 'direct' }
    }
    await pool.applyTemplate(form.template, target)
    return true
  } catch (e) {
    error.value = t('wizard.applyFailed', { msg: e instanceof Error ? e.message : String(e) })
    return false
  }
}

async function finish() {
  error.value = ''
  const ok1 = await addSubscription()
  if (!ok1) return
  const ok2 = await applyPolicy()
  if (!ok2) return
  const ok3 = await applyTemplate()
  if (!ok3) return
  done.value = true
}
</script>

<template>
  <section class="wizard">
    <h1>{{ t('wizard.title') }}</h1>
    <p class="muted" v-if="!done">{{ t('wizard.step', { n: step }) }} · {{ t('wizard.hint') }}</p>

    <template v-if="!done">
      <div v-if="step === 1" class="step">
        <h2>{{ t('wizard.step1.title') }}</h2>
        <p class="muted">{{ t('wizard.step1.text') }}</p>
        <label>
          <span>{{ t('wizard.name') }}</span>
          <input v-model="form.name" autocomplete="off" />
        </label>
        <label>
          <span>{{ t('wizard.url') }}</span>
          <input v-model="form.url" placeholder="https://…" autocomplete="off" />
        </label>
      </div>

      <div v-if="step === 2" class="step">
        <h2>{{ t('wizard.step2.title') }}</h2>
        <p class="muted">{{ t('wizard.step2.text') }}</p>
        <label>
          <span>{{ t('wizard.defaultPolicy') }}</span>
          <select v-model="form.policy">
            <option value="direct">{{ t('wizard.policy.direct') }}</option>
            <option value="reject">{{ t('wizard.policy.reject') }}</option>
          </select>
        </label>
      </div>

      <div v-if="step === 3" class="step">
        <h2>{{ t('wizard.step3.title') }}</h2>
        <p class="muted">{{ t('wizard.step3.text') }}</p>
        <label>
          <span>{{ t('templates.title') }}</span>
          <select v-model="form.template">
            <option value="">{{ t('wizard.skip') }}</option>
            <option v-for="tpl in offeredTemplates" :key="tpl.id" :value="tpl.id">{{ tpl.name }}</option>
          </select>
        </label>
        <div v-if="offeredTemplates.length" class="tpl-cards">
          <div
            v-for="tpl in offeredTemplates"
            :key="tpl.id"
            class="tpl-card"
            :class="{ selected: form.template === tpl.id }"
            @click="form.template = form.template === tpl.id ? '' : tpl.id"
          >
            <div class="tpl-card-head">
              <strong>{{ tpl.name }}</strong>
              <span v-if="tpl.id === 'all-vpn'" class="badge">MATCH</span>
            </div>
            <p v-if="tpl.description" class="tpl-card-desc">{{ tpl.description }}</p>
            <p v-if="tpl.list_source" class="tpl-card-src">
              {{ t('templates.listSource', { text: tpl.list_source }) }}
            </p>
          </div>
        </div>
        <p v-else-if="pool.error" class="error">{{ t('templates.unavailable') }}</p>
      </div>

      <p v-if="error" class="error">{{ error }}</p>

      <div class="buttons">
        <button v-if="step > 1" class="secondary" @click="back">{{ t('wizard.back') }}</button>
        <button v-if="step < 3" @click="next">{{ t('wizard.next') }}</button>
        <button v-if="step === 3" @click="finish">{{ t('wizard.finish') }}</button>
        <button class="ghost" @click="router.push('/dashboard')">{{ t('wizard.later') }}</button>
      </div>
    </template>

    <template v-else>
      <h2>{{ t('wizard.doneTitle') }}</h2>
      <p class="muted">{{ t('wizard.doneText') }}</p>
      <button @click="router.push('/dashboard')">{{ t('wizard.goDashboard') }}</button>
    </template>
  </section>
</template>

<style scoped>
.wizard {
  max-width: 480px;
  margin: 24px auto;
  padding: 20px;
  border: 1px solid #e2e2e2;
  border-radius: 10px;
  background: #fff;
}
h1 {
  margin: 0 0 8px;
  font-size: 1.3rem;
}
h2 {
  margin: 8px 0 6px;
  font-size: 1.05rem;
}
.muted {
  color: #666;
  font-size: 0.9rem;
}
.step {
  display: grid;
  gap: 10px;
  margin: 14px 0;
}
.step label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.step input,
.step select {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.95rem;
}
.tpl-cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 10px;
  margin-top: 8px;
}
.tpl-card {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 12px;
  display: flex;
  flex-direction: column;
  gap: 6px;
  cursor: pointer;
}
.tpl-card.selected {
  border-color: #1d4ed8;
  background: #f5f8ff;
}
.tpl-card-head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.tpl-card .badge {
  background: #ede9fe;
  color: #6d28d9;
  font-size: 0.7rem;
  padding: 2px 6px;
  border-radius: 4px;
}
.tpl-card-desc {
  margin: 0;
  font-size: 0.85rem;
  color: #555;
}
.tpl-card-src {
  margin: 0;
  font-size: 0.78rem;
  color: #7c3aed;
}
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.buttons {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 12px;
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
.ghost {
  background: transparent;
  color: #666;
  border-color: transparent;
  text-decoration: underline;
}
@media (max-width: 360px) {
  .wizard {
    padding: 14px;
    margin: 12px auto;
  }
}
</style>
