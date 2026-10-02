<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore, serializeConditions } from '../stores/pool'
import { geositeTags, type Target, type Template } from '../api'

// Templates (FR-5): catalog + apply with target selection.
// The "all-vpn" template sets the default policy (api.go special-case).
// B1: custom templates overlay — create/edit (overlay file),
// disable/enable (settings.disabled_templates), delete (overlay file
// only; deleting an override restores the builtin entry).
// B3: geosite condition rows get a datalist with known
// category tags from GET /api/v1/geodata/tags.
const pool = usePoolStore()
const selected = ref<Record<string, string>>({})
const applying = ref<string | null>(null)
const message = ref('')
const error = ref('')

// ---------------------------------------------------------------------------
// Template editor (create/edit)
// ---------------------------------------------------------------------------

const editorOpen = ref(false)
const editingId = ref<string | null>(null) // null = create
const editorError = ref('')
const editorSaving = ref(false)
const geositeTagList = ref<string[]>([])

const editor = reactive({
  id: '',
  name: '',
  description: '',
  list_source: '',
  typical_target: '',
  conditions: [{ type: 'geosite', value: '' }] as { type: string; value: string }[],
})

// Типичное направление: direct/reject — литералы; server/group — из пула.
const editorTargets = computed(() => [
  { value: '', label: '—' },
  { value: 'direct', label: 'DIRECT' },
  { value: 'reject', label: 'REJECT' },
  { value: 'server', label: t('templates.editor.targetServer') },
  { value: 'group', label: t('templates.editor.targetGroup') },
])

function openCreate() {
  editingId.value = null
  editor.id = ''
  editor.name = ''
  editor.description = ''
  editor.list_source = ''
  editor.typical_target = ''
  editor.conditions = [{ type: 'geosite', value: '' }]
  editorError.value = ''
  editorOpen.value = true
}

function openEdit(tpl: Template) {
  editingId.value = tpl.id
  editor.id = tpl.id
  editor.name = tpl.name
  editor.description = tpl.description ?? ''
  editor.list_source = tpl.list_source ?? ''
  editor.typical_target = tpl.typical_target ?? ''
  editor.conditions = tpl.conditions.length
    ? tpl.conditions.map((c) => ({ type: String(c.type), value: c.value }))
    : [{ type: 'geosite', value: '' }]
  editorError.value = ''
  editorOpen.value = true
}

function addCondition() {
  editor.conditions.push({ type: 'geosite', value: '' })
}
function removeCondition(i: number) {
  editor.conditions.splice(i, 1)
}

async function saveEditor() {
  editorError.value = ''
  if (editingId.value === null && !editor.id.trim()) {
    editorError.value = t('templates.editor.idRequired')
    return
  }
  if (!editor.name.trim()) {
    editorError.value = t('templates.editor.nameRequired')
    return
  }
  const conditions = serializeConditions(editor.conditions)
  const input = {
    id: editingId.value ?? editor.id.trim(),
    name: editor.name.trim(),
    description: editor.description.trim(),
    list_source: editor.list_source.trim(),
    typical_target: editor.typical_target,
    conditions,
  }
  editorSaving.value = true
  try {
    await pool.saveTemplate(input, editingId.value ?? undefined)
    editorOpen.value = false
    message.value = editingId.value ? t('templates.editor.saved') : t('templates.editor.created')
  } catch (e) {
    editorError.value = t('templates.editor.saveFailed', {
      msg: e instanceof Error ? e.message : String(e),
    })
  } finally {
    editorSaving.value = false
  }
}

async function removeTemplate(tpl: Template) {
  if (!confirm(t('templates.confirmDelete', { name: tpl.name }))) return
  error.value = ''
  try {
    await pool.removeTemplate(tpl.id)
    message.value = t('templates.deleted')
  } catch (e) {
    error.value = t('templates.deleteFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

async function toggleDisabled(tpl: Template) {
  error.value = ''
  try {
    await pool.toggleTemplateDisabled(tpl.id, !tpl.disabled)
  } catch (e) {
    error.value = t('templates.toggleFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

// B3: load the geosite tag list for the datalist once per mount.
onMounted(async () => {
  for (const tpl of pool.templates) {
    if (selected.value[tpl.id] !== undefined) continue
    selected.value[tpl.id] = initialTarget(tpl.typical_target)
  }
  try {
    geositeTagList.value = await geositeTags()
  } catch {
    geositeTagList.value = [] // datalist is optional sugar
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

    <!-- Warnings: broken overlay files are skipped, not fatal (B1). -->
    <div v-if="pool.templateWarnings.length" class="warnings">
      <p v-for="w in pool.templateWarnings" :key="w" class="warn">{{ w }}</p>
    </div>

    <button class="create" @click="openCreate">{{ t('templates.create') }}</button>

    <div class="tpl-list">
      <div
        v-for="tpl in pool.templates"
        :key="tpl.id"
        class="tpl"
        :class="{ 'tpl-disabled': tpl.disabled }"
      >
        <div class="tpl-head">
          <strong>{{ tpl.name }}</strong>
          <span v-if="tpl.id === 'all-vpn'" class="badge">MATCH</span>
          <span v-if="tpl.origin === 'custom'" class="badge badge-custom">
            {{ tpl.overridden ? t('templates.originCustomOverride') : t('templates.originCustom') }}
          </span>
          <span v-else-if="tpl.overridden" class="badge badge-overridden">
            {{ t('templates.originOverridden') }}
          </span>
          <span v-if="tpl.disabled" class="badge badge-off">{{ t('templates.disabledBadge') }}</span>
        </div>
        <p class="tpl-desc">{{ tpl.description }}</p>
        <p v-if="tpl.list_source" class="tpl-src">
          {{ t('templates.listSource', { text: tpl.list_source }) }}
        </p>
        <p v-if="tpl.providers?.length" class="tpl-prov">
          {{ t('templates.providers', { list: tpl.providers.join(', ') }) }}
        </p>

        <div class="tpl-actions">
          <button class="small" @click="openEdit(tpl)">{{ t('common.edit') }}</button>
          <button class="small" @click="toggleDisabled(tpl)">
            {{ tpl.disabled ? t('templates.enable') : t('templates.disable') }}
          </button>
          <button
            v-if="tpl.origin === 'custom' || tpl.overridden"
            class="small danger"
            @click="removeTemplate(tpl)"
          >
            {{ t('common.delete') }}
          </button>
        </div>

        <label class="target-pick" :class="{ 'pick-disabled': tpl.disabled }">
          <span>{{ t('templates.chooseTarget') }}</span>
          <select v-model="selected[tpl.id]" :disabled="tpl.disabled">
            <option v-for="o in pool.targets" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
        </label>
        <button
          class="apply"
          :disabled="applying === tpl.id || !pool.templates.length || tpl.disabled"
          @click="apply(tpl.id, tpl.name)"
        >
          {{ applying === tpl.id ? t('templates.applying') : t('templates.apply') }}
        </button>
      </div>
    </div>
    <p v-if="!pool.templates.length && !pool.error" class="muted">{{ t('common.loading') }}</p>

    <!-- Editor dialog (B1) -->
    <div v-if="editorOpen" class="editor-backdrop" @click.self="editorOpen = false">
      <div class="editor">
        <h2>{{ editingId ? t('templates.edit', { name: editor.name }) : t('templates.create') }}</h2>
        <p v-if="editorError" class="error">{{ editorError }}</p>
        <form class="editor-form" @submit.prevent="saveEditor">
          <label v-if="editingId === null">
            <span>{{ t('templates.editor.id') }}</span>
            <input v-model="editor.id" placeholder="my-template" autocomplete="off" />
            <small>{{ t('templates.editor.idHint') }}</small>
          </label>
          <label v-else>
            <span>{{ t('templates.editor.id') }}</span>
            <input :value="editingId" disabled />
          </label>
          <label>
            <span>{{ t('templates.editor.name') }}</span>
            <input v-model="editor.name" autocomplete="off" />
          </label>
          <label>
            <span>{{ t('templates.editor.description') }}</span>
            <input v-model="editor.description" autocomplete="off" />
          </label>
          <label>
            <span>{{ t('templates.editor.listSource') }}</span>
            <input v-model="editor.list_source" autocomplete="off" />
          </label>
          <label>
            <span>{{ t('templates.editor.typicalTarget') }}</span>
            <select v-model="editor.typical_target">
              <option v-for="o in editorTargets" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </label>

          <div class="conditions">
            <span class="cond-title">{{ t('templates.editor.conditions') }}</span>
            <div v-for="(c, i) in editor.conditions" :key="i" class="cond-row">
              <select v-model="c.type">
                <option value="geosite">geosite</option>
                <option value="geoip">geoip</option>
                <option value="domain">domain</option>
                <option value="domain-suffix">domain-suffix</option>
                <option value="domain-keyword">domain-keyword</option>
                <option value="ip-cidr">ip-cidr</option>
                <option value="dst-port">dst-port</option>
              </select>
              <!-- B3: geosite rows get the category datalist. -->
              <input
                v-if="c.type === 'geosite'"
                v-model="c.value"
                list="geosite-tags"
                :placeholder="t('templates.editor.geositePlaceholder')"
              />
              <input v-else v-model="c.value" />
              <button type="button" class="small danger" @click="removeCondition(i)">×</button>
            </div>
            <button type="button" class="small" @click="addCondition">
              {{ t('templates.editor.addCondition') }}
            </button>
            <small>{{ t('templates.editor.conditionsHint') }}</small>
          </div>

          <div class="editor-buttons">
            <button type="submit" :disabled="editorSaving">
              {{ editorSaving ? t('common.loading') : t('common.save') }}
            </button>
            <button type="button" class="secondary" @click="editorOpen = false">
              {{ t('common.cancel') }}
            </button>
          </div>
        </form>
        <datalist id="geosite-tags">
          <option v-for="tag in geositeTagList" :key="tag" :value="tag" />
        </datalist>
      </div>
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
}
.ok {
  color: #15803d;
  font-size: 0.9rem;
}
.error {
  color: #b91c1c;
  font-size: 0.9rem;
}
.warnings {
  margin: 8px 0;
}
.warn {
  margin: 2px 0;
  color: #92400e;
  font-size: 0.8rem;
  background: #fef3c7;
  border-radius: 6px;
  padding: 6px 8px;
}
.create {
  margin: 8px 0 4px;
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
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
.tpl-disabled {
  opacity: 0.55;
  border-style: dashed;
}
.tpl-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.badge {
  background: #ede9fe;
  color: #6d28d9;
  font-size: 0.7rem;
  padding: 2px 6px;
  border-radius: 4px;
}
.badge-custom {
  background: #dcfce7;
  color: #15803d;
}
.badge-overridden {
  background: #ffedd5;
  color: #c2410c;
}
.badge-off {
  background: #f3f4f6;
  color: #6b7280;
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
.tpl-src {
  margin: 0;
  font-size: 0.78rem;
  color: #7c3aed;
}
.tpl-actions {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.small {
  padding: 4px 10px;
  border-radius: 6px;
  border: 1px solid #ccc;
  background: #f6f6f6;
  color: #333;
  cursor: pointer;
  font-size: 0.8rem;
}
.small.danger {
  border-color: #b91c1c;
  color: #b91c1c;
  background: #fef2f2;
}
.target-pick {
  display: grid;
  gap: 3px;
  font-size: 0.8rem;
  color: #444;
}
.pick-disabled {
  opacity: 0.6;
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
.editor-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.35);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: 40px 12px;
  overflow-y: auto;
  z-index: 20;
}
.editor {
  background: #fff;
  border-radius: 10px;
  padding: 18px;
  width: 100%;
  max-width: 480px;
}
.editor h2 {
  margin: 0 0 10px;
  font-size: 1.1rem;
}
.editor-form {
  display: grid;
  gap: 10px;
}
.editor-form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.editor-form input,
.editor-form select {
  padding: 8px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.95rem;
}
.editor-form small {
  color: #888;
}
.conditions {
  display: grid;
  gap: 6px;
}
.cond-title {
  font-size: 0.85rem;
  color: #444;
}
.cond-row {
  display: grid;
  grid-template-columns: 130px 1fr 34px;
  gap: 6px;
}
.cond-row select,
.cond-row input {
  padding: 6px;
  border: 1px solid #ccc;
  border-radius: 6px;
  font-size: 0.85rem;
}
.editor-buttons {
  display: flex;
  gap: 8px;
}
.editor-buttons button {
  padding: 8px 14px;
  border-radius: 6px;
  border: 1px solid #1d4ed8;
  background: #1d4ed8;
  color: #fff;
  cursor: pointer;
  font-size: 0.9rem;
}
.editor-buttons .secondary {
  background: #f6f6f6;
  color: #333;
  border-color: #ccc;
}
@media (max-width: 360px) {
  .tpl-list {
    grid-template-columns: 1fr;
  }
  .cond-row {
    grid-template-columns: 1fr;
  }
}
</style>
