<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore } from '../stores/pool'
import type { Group } from '../api'

// Groups screen (FR-3.3, TZ screen list "Группы"): list, create
// (name + type select/url-test/fallback/load-balance + members from the
// server/group pool), delete. PATCH exists in the API but the first
// iteration ships create/delete — the member list is editable by
// deleting and recreating (pinned in DECISIONS D16).
const pool = usePoolStore()
const error = ref('')
const formError = ref('')
const saving = ref(false)
const showForm = ref(false)

const GROUP_TYPES: Group['type'][] = ['select', 'url-test', 'fallback', 'load-balance']

const form = reactive({
  name: '',
  type: 'select' as Group['type'],
  members: [] as string[],
})

// Options: all servers + other groups (self excluded on create — there
// is no self yet). The server rejects members that are neither.
const memberOptions = computed(() => {
  const out: { value: string; label: string }[] = []
  for (const g of pool.groups) out.push({ value: g.id, label: g.name + ' (group)' })
  for (const sv of pool.servers) out.push({ value: sv.id, label: sv.name })
  return out
})

function openForm() {
  form.name = ''
  form.type = 'select'
  form.members = []
  formError.value = ''
  showForm.value = true
}

function toggleMember(id: string) {
  const i = form.members.indexOf(id)
  if (i >= 0) form.members.splice(i, 1)
  else form.members.push(id)
}

async function save() {
  formError.value = ''
  if (!form.name.trim()) {
    formError.value = t('groups.nameRequired')
    return
  }
  if (!form.members.length) {
    formError.value = t('groups.membersRequired')
    return
  }
  saving.value = true
  try {
    await pool.createGroup({ name: form.name.trim(), type: form.type, members: [...form.members] })
    showForm.value = false
  } catch (e) {
    formError.value = t('groups.saveFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    saving.value = false
  }
}

async function remove(id: string) {
  error.value = ''
  try {
    await pool.deleteGroup(id)
  } catch (e) {
    error.value = t('groups.deleteFailed', { msg: e instanceof Error ? e.message : String(e) })
  }
}

onMounted(() => {
  if (!pool.groups.length) pool.loadAll().catch(() => {})
})
</script>

<template>
  <section>
    <h1>{{ t('groups.title') }}</h1>
    <p class="muted">{{ t('groups.subtitle') }}</p>
    <p v-if="error" class="error">{{ error }}</p>

    <button v-if="!showForm" class="primary" @click="openForm">{{ t('groups.add') }}</button>

    <form v-if="showForm" class="group-form" @submit.prevent="save">
      <h2>{{ t('groups.addTitle') }}</h2>
      <p v-if="formError" class="error">{{ formError }}</p>

      <label>
        <span>{{ t('sources.name') }}</span>
        <input v-model="form.name" required maxlength="64" autocomplete="off" />
      </label>

      <label>
        <span>{{ t('groups.type') }}</span>
        <select v-model="form.type">
          <option v-for="gt in GROUP_TYPES" :key="gt" :value="gt">{{ t('groups.type.' + gt) }}</option>
        </select>
      </label>

      <fieldset>
        <legend>{{ t('groups.members') }}</legend>
        <p class="hint">{{ t('groups.membersHint') }}</p>
        <label v-for="o in memberOptions" :key="o.value" class="member-row">
          <input
            type="checkbox"
            :checked="form.members.includes(o.value)"
            @change="toggleMember(o.value)"
          />
          <span>{{ o.label }}</span>
        </label>
        <p v-if="!memberOptions.length" class="muted">{{ t('groups.noPool') }}</p>
      </fieldset>

      <div class="form-actions">
        <button type="submit" class="primary" :disabled="saving || !form.name.trim() || !form.members.length">
          {{ saving ? t('common.loading') : t('common.save') }}
        </button>
        <button type="button" class="secondary" @click="showForm = false">{{ t('common.cancel') }}</button>
      </div>
    </form>

    <div v-if="pool.groups.length" class="group-list">
      <div v-for="g in pool.groups" :key="g.id" class="group">
        <div class="head">
          <strong>{{ g.name }}</strong>
          <span class="type">{{ t('groups.type.' + g.type) }}</span>
        </div>
        <div class="members">
          <span v-for="m in g.members" :key="m" class="member">{{ pool.memberLabel(m) }}</span>
        </div>
        <button class="danger" @click="remove(g.id)">{{ t('common.delete') }}</button>
      </div>
    </div>
    <p v-else class="muted">{{ t('groups.noGroups') }}</p>
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
.group-form {
  display: grid;
  gap: 10px;
  max-width: 480px;
  margin: 14px 0;
  padding: 14px;
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  background: #fafafa;
}
.group-form h2 {
  margin: 0;
  font-size: 1.05rem;
}
.group-form label {
  display: grid;
  gap: 3px;
  font-size: 0.85rem;
  color: #444;
}
.group-form input,
.group-form select {
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
  gap: 6px;
}
legend {
  font-size: 0.85rem;
  color: #444;
  padding: 0 4px;
}
.hint {
  margin: 0 0 4px;
  font-size: 0.78rem;
  color: #888;
}
.member-row {
  display: flex !important;
  flex-direction: row !important;
  gap: 8px;
  align-items: center;
  font-size: 0.85rem;
  cursor: pointer;
}
.group-list {
  display: grid;
  gap: 10px;
  margin-top: 14px;
}
.group {
  border: 1px solid #e2e2e2;
  border-radius: 8px;
  padding: 10px 12px;
  display: grid;
  gap: 6px;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}
.type {
  font-size: 0.75rem;
  background: #ede9fe;
  color: #6d28d9;
  padding: 2px 6px;
  border-radius: 4px;
}
.members {
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}
.member {
  font-size: 0.8rem;
  border: 1px solid #e2e2e2;
  border-radius: 4px;
  padding: 2px 6px;
  color: #555;
}
.danger {
  justify-self: start;
  background: #fff;
  color: #b91c1c;
  border: 1px solid #b91c1c;
  border-radius: 6px;
  padding: 4px 10px;
  font-size: 0.85rem;
  cursor: pointer;
}
.form-actions {
  display: flex;
  gap: 8px;
}
@media (max-width: 360px) {
  .group-form {
    max-width: none;
    padding: 10px;
  }
  .head {
    flex-direction: column;
    align-items: flex-start;
  }
}
</style>
