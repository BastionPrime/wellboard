<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore, type RouteInput } from '../stores/pool'
import type { LANDevice, Route } from '../api'

// Routes (FR-4): ordered list + create/edit form (FR-4.1-4.3, FR-4.7).
// The form covers name, enabled, order, conditions (type+value; for
// src-device a LAN device picker with a static-lease offer, FR-4.4),
// target (server/group/DIRECT/REJECT) and on_unavailable. The server is
// the validator; client-side checks only guard obvious mistakes.

const CONDITION_TYPES = [
  'domain',
  'domain-suffix',
  'domain-keyword',
  'geosite',
  'geoip',
  'ip-cidr',
  'src-device',
  'dst-port',
] as const

const pool = usePoolStore()
const error = ref('')
const formError = ref('')

// ---------------------------------------------------------------------------
// Form state
// ---------------------------------------------------------------------------

const showForm = ref(false)
const editingId = ref<string | null>(null) // null = creating
const lanOpen = ref(false)
const lanMsg = ref('')
const saving = ref(false)
// The condition row index the LAN picker was opened from.
const lanIndex = ref(0)

const form = reactive({
  name: '',
  enabled: true,
  order: '' as string, // '' = auto (server: max+10)
  conditions: [] as { type: string; value: string }[],
  providers: '' as string, // comma-separated
  targetValue: 'direct',
  onUnavailable: 'block' as 'block' | 'direct',
})

function targetLabel(target: { type: string; id?: string }): string {
  if (target.type === 'direct') return 'DIRECT'
  if (target.type === 'reject') return 'REJECT'
  if (target.type === 'server') return pool.serverName(target.id ?? '')
  return pool.groupName(target.id ?? '')
}

function formInput(): RouteInput {
  return {
    name: form.name,
    enabled: form.enabled,
    order: form.order === '' ? null : Number(form.order),
    conditions: form.conditions.map((c) => ({ type: c.type, value: c.value })),
    providers: form.providers.split(',').map((p) => p.trim()).filter(Boolean),
    target: targetOf(form.targetValue),
    onUnavailable: form.onUnavailable,
  }
}

// targetOf maps the single select value to a Target: direct/reject
// literals, otherwise group-or-server by pool lookup.
function targetOf(value: string): { type: string; id?: string } {
  if (value === 'direct' || value === 'reject') return { type: value }
  if (pool.groups.some((g) => g.id === value)) return { type: 'group', id: value }
  return { type: 'server', id: value }
}

// ---------------------------------------------------------------------------
// Create / edit
// ---------------------------------------------------------------------------

function startCreate() {
  editingId.value = null
  form.name = ''
  form.enabled = true
  form.order = ''
  form.conditions = [{ type: 'domain-suffix', value: '' }]
  form.providers = ''
  form.targetValue = 'direct'
  form.onUnavailable = 'block'
  formError.value = ''
  lanMsg.value = ''
  showForm.value = true
}

function startEdit(rt: Route) {
  editingId.value = rt.id
  form.name = rt.name
  form.enabled = rt.enabled
  form.order = String(rt.order)
  form.conditions = (rt.conditions ?? []).map((c) => ({ type: c.type, value: c.value }))
  if (!form.conditions.length) form.conditions = [{ type: 'domain-suffix', value: '' }]
  form.providers = (rt.providers ?? []).join(', ')
  form.targetValue = rt.target.type === 'direct' || rt.target.type === 'reject'
    ? rt.target.type
    : (rt.target.id ?? '')
  form.onUnavailable = rt.on_unavailable
  formError.value = ''
  lanMsg.value = ''
  showForm.value = true
}

function cancelForm() {
  showForm.value = false
  editingId.value = null
}

async function save() {
  formError.value = ''
  const input = formInput()
  if (input.order != null && (Number.isNaN(input.order) || input.order < 0)) {
    formError.value = t('routes.orderInvalid')
    return
  }
  saving.value = true
  try {
    if (editingId.value) await pool.updateRoute(editingId.value, input)
    else await pool.createRoute(input)
    showForm.value = false
    editingId.value = null
  } catch (e) {
    formError.value = t('routes.saveFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    saving.value = false
  }
}

// ---------------------------------------------------------------------------
// Conditions
// ---------------------------------------------------------------------------

function addCondition() {
  form.conditions.push({ type: 'domain-suffix', value: '' })
}

function removeCondition(i: number) {
  form.conditions.splice(i, 1)
}

// src-device values are LAN device IPs; the picker offers devices from
// GET /lan-devices and suggests pinning a static lease (FR-4.4).
const lanDevices = computed<LANDevice[]>(() => pool.lanDevices ?? [])

function openLan(i: number) {
  lanIndex.value = i
  lanOpen.value = true
  lanMsg.value = ''
  if (pool.lanDevices == null) pool.loadLAN().catch(() => {})
}

function pickDevice(dev: LANDevice) {
  if (form.conditions[lanIndex.value]) {
    form.conditions[lanIndex.value].value = dev.ip
  }
  lanOpen.value = false
}

async function pinLease(dev: LANDevice) {
  lanMsg.value = ''
  try {
    const out = await pool.pinStatic(dev)
    if (out.status === 'simulated') {
      // Dev-stub answer: surface the API's own warning verbatim.
      lanMsg.value = t('routes.lanSimulated', { detail: out.detail ?? '' })
    } else {
      lanMsg.value = t('routes.lanPinned', { ip: dev.ip })
    }
    await pool.loadLAN()
  } catch (e) {
    lanMsg.value = t('routes.lanPinFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

// ---------------------------------------------------------------------------
// List actions
// ---------------------------------------------------------------------------

async function toggle(id: string, enabled: boolean) {
  error.value = ''
  try {
    await pool.setRouteEnabled(id, enabled)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

async function move(id: string, dir: -1 | 1) {
  error.value = ''
  try {
    await pool.moveRoute(id, dir)
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}

async function remove(id: string) {
  error.value = ''
  try {
    await pool.removeRoute(id)
  } catch (e) {
    error.value = t('routes.deleteFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

onMounted(() => {
  if (!pool.routes.length) pool.loadAll().catch(() => {})
})
</script>

<template>
  <section>
    <h1>{{ t('routes.title') }}</h1>
    <p class="muted">{{ t('routes.subtitle') }}</p>
    <p v-if="error" class="error">{{ error }}</p>

    <button v-if="!showForm" class="primary" @click="startCreate">{{ t('routes.add') }}</button>

    <form v-if="showForm" class="route-form" @submit.prevent="save">
      <h2>{{ editingId ? t('routes.editTitle') : t('routes.addTitle') }}</h2>
      <p v-if="formError" class="error">{{ formError }}</p>

      <label>
        <span>{{ t('sources.name') }}</span>
        <input v-model="form.name" required maxlength="64" autocomplete="off" />
      </label>

      <label>
        <span>{{ t('routes.enabled') }}</span>
        <label class="switch">
          <input type="checkbox" v-model="form.enabled" />
          <span>{{ form.enabled ? t('common.on') : t('common.off') }}</span>
        </label>
      </label>

      <label>
        <span>{{ t('routes.order') }}</span>
        <input v-model="form.order" type="number" min="0" :placeholder="t('routes.orderAuto')" />
      </label>

      <fieldset>
        <legend>{{ t('routes.conditions') }}</legend>
        <p class="hint">{{ t('routes.conditionsHint') }}</p>
        <div v-for="(c, i) in form.conditions" :key="i" class="cond-row">
          <select v-model="c.type">
            <option v-for="ct in CONDITION_TYPES" :key="ct" :value="ct">{{ ct }}</option>
          </select>
          <input
            v-model="c.value"
            :placeholder="c.type === 'src-device' ? '192.168.1.50' : c.type === 'dst-port' ? '443' : '…'"
            autocomplete="off"
          />
          <button
            v-if="c.type === 'src-device'"
            type="button"
            class="small secondary"
            @click="openLan(i)"
          >
            {{ t('routes.pickDevice') }}
          </button>
          <button type="button" class="small danger" @click="removeCondition(i)">×</button>
        </div>
        <button type="button" class="small secondary" @click="addCondition">{{ t('routes.addCondition') }}</button>

        <div v-if="lanOpen" class="lan-picker">
          <h3>{{ t('routes.lanTitle') }}</h3>
          <p v-if="lanMsg" :class="lanMsg.includes('dev') || lanMsg.includes('simulat') ? 'warn' : 'ok'">{{ lanMsg }}</p>
          <p v-if="pool.lanUnavailable" class="warn">{{ t('routes.lanUnavailable') }}</p>
          <template v-else-if="lanDevices.length">
            <div v-for="d in lanDevices" :key="d.mac" class="lan-dev">
              <button type="button" class="small" @click="pickDevice(d)">
                {{ d.hostname || d.mac }} · {{ d.ip }}
              </button>
              <span v-if="d.static" class="badge">{{ t('routes.lanStatic') }}</span>
              <button v-else type="button" class="small secondary" @click="pinLease(d)">
                {{ t('routes.lanPin') }}
              </button>
            </div>
          </template>
          <p v-else class="muted">{{ t('routes.lanEmpty') }}</p>
          <button type="button" class="small" @click="lanOpen = false">{{ t('common.close') }}</button>
        </div>
      </fieldset>

      <label>
        <span>{{ t('routes.providersLabel') }}</span>
        <input v-model="form.providers" :placeholder="t('routes.providersPlaceholder')" autocomplete="off" />
      </label>

      <label>
        <span>{{ t('routes.target') }}</span>
        <select v-model="form.targetValue">
          <option v-for="o in pool.targets" :key="o.value" :value="o.value">{{ o.label }}</option>
        </select>
      </label>

      <label>
        <span>{{ t('routes.onUnavailable') }}</span>
        <select v-model="form.onUnavailable">
          <option value="block">{{ t('routes.onUnavailable.block') }}</option>
          <option value="direct">{{ t('routes.onUnavailable.direct') }}</option>
        </select>
      </label>

      <div class="form-actions">
        <button type="submit" class="primary" :disabled="saving || !form.name.trim()">
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
        <button type="button" class="secondary" @click="cancelForm">{{ t('common.cancel') }}</button>
      </div>
    </form>

    <ol v-if="pool.routesSorted.length" class="route-list">
      <li v-for="rt in pool.routesSorted" :key="rt.id" :class="{ off: !rt.enabled }">
        <div class="row1">
          <label class="switch">
            <input
              type="checkbox"
              :checked="rt.enabled"
              @change="toggle(rt.id, ($event.target as HTMLInputElement).checked)"
            />
            <strong>{{ rt.name }}</strong>
          </label>
          <span class="target">{{ t('routes.target') }}: {{ targetLabel(rt.target) }}</span>
        </div>
        <div class="row2">
          <span class="conds">
            {{ rt.conditions.map((c) => c.type + ' ' + c.value).join(' ∧ ') }}
          </span>
          <span v-if="rt.providers?.length" class="prov">{{ t('routes.providers') }}: {{ rt.providers.join(', ') }}</span>
        </div>
        <div class="row3">
          <button class="small" :disabled="pool.routesSorted[0].id === rt.id" @click="move(rt.id, -1)">↑ {{ t('routes.moveUp') }}</button>
          <button
            class="small"
            :disabled="pool.routesSorted[pool.routesSorted.length - 1].id === rt.id"
            @click="move(rt.id, 1)"
          >
            ↓ {{ t('routes.moveDown') }}
          </button>
          <button class="small" @click="startEdit(rt)">{{ t('routes.edit') }}</button>
          <button class="small danger" @click="remove(rt.id)">{{ t('routes.remove') }}</button>
        </div>
      </li>
    </ol>
    <p v-else class="muted">{{ t('routes.noRoutes') }}</p>
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
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.route-list {
  list-style: none;
  margin: 14px 0 0;
  padding: 0;
  display: grid;
  gap: 10px;
}
.route-list li {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 10px 12px;
}
.route-list li.off {
  opacity: 0.6;
}
.row1 {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}
.target {
  font-size: 0.85rem;
  color: #1d4ed8;
  word-break: break-word;
}
.row2 {
  margin-top: 4px;
  font-size: 0.82rem;
  color: #555;
  word-break: break-word;
}
.prov {
  color: #7c3aed;
  margin-left: 6px;
}
.row3 {
  margin-top: 8px;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.small {
  padding: 3px 10px;
  font-size: 0.8rem;
  border-radius: 6px;
  border: 1px solid #ccc;
  background: #f6f6f6;
  color: #333;
  cursor: pointer;
}
.small:disabled {
  opacity: 0.4;
}
.small.danger {
  color: #b91c1c;
  border-color: #b91c1c;
  background: #fff;
}
.small.secondary {
  color: #1d4ed8;
  border-color: #1d4ed8;
  background: #fff;
}
.switch {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  cursor: pointer;
}
.primary {
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.95rem;
}
.primary:disabled {
  opacity: 0.5;
}
.secondary {
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #ccc;
  background: #f6f6f6;
  color: #333;
  cursor: pointer;
  font-size: 0.95rem;
}
.route-form {
  display: grid;
  gap: 10px;
  max-width: 480px;
  margin: 14px 0;
  padding: 14px;
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  background: #fafafa;
}
.route-form h2 {
  margin: 0;
  font-size: 1.05rem;
}
.route-form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.route-form input,
.route-form select {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.95rem;
}
fieldset {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 10px;
  display: grid;
  gap: 8px;
}
legend {
  font-size: 0.85rem;
  color: #444;
  padding: 0 4px;
}
.hint {
  margin: 0;
  font-size: 0.78rem;
  color: #888;
}
.cond-row {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.cond-row select {
  padding: 6px;
  font-size: 0.85rem;
}
.cond-row input {
  flex: 1;
  min-width: 120px;
  padding: 6px;
  font-size: 0.85rem;
}
.lan-picker {
  border: 1px dashed #ccc;
  border-radius: 8px;
  padding: 10px;
  display: grid;
  gap: 6px;
  background: #fff;
}
.lan-picker h3 {
  margin: 0;
  font-size: 0.9rem;
}
.lan-dev {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
  font-size: 0.85rem;
}
.badge {
  background: #ede9fe;
  color: #6d28d9;
  font-size: 0.7rem;
  padding: 2px 6px;
  border-radius: 4px;
}
.warn {
  color: #b45309;
  font-size: 0.82rem;
  word-break: break-word;
}
.ok {
  color: #15803d;
  font-size: 0.82rem;
}
.form-actions {
  display: flex;
  gap: 8px;
}
@media (max-width: 360px) {
  .row1 {
    flex-direction: column;
    align-items: flex-start;
  }
  .row3 {
    flex-wrap: wrap;
  }
  .route-form {
    max-width: none;
    padding: 10px;
  }
  .cond-row input {
    min-width: 100px;
  }
}
</style>
