<script setup lang="ts">
import { ref } from 'vue'
import { t } from '../i18n'
import { usePoolStore } from '../stores/pool'

// Routes (FR-4): ordered list with enable toggle and up/down reorder.
const pool = usePoolStore()
const error = ref('')

function targetLabel(target: { type: string; id?: string }): string {
  if (target.type === 'direct') return 'DIRECT'
  if (target.type === 'reject') return 'REJECT'
  if (target.type === 'server') return pool.serverName(target.id ?? '')
  return pool.groupName(target.id ?? '')
}

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
</script>

<template>
  <section>
    <h1>{{ t('routes.title') }}</h1>
    <p class="muted">{{ t('routes.subtitle') }}</p>
    <p v-if="error" class="error">{{ error }}</p>

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
            {{ rt.conditions.map((c) => c.type + ' ' + c.value).join(' ∧ ') || (rt.providers?.length ? '' : '') }}
            <span v-if="rt.providers?.length" class="prov">{{ t('routes.providers') }}: {{ rt.providers.join(', ') }}</span>
          </span>
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
  margin: 0;
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
}
.row3 {
  margin-top: 8px;
  display: flex;
  gap: 6px;
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
.switch {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  cursor: pointer;
}
@media (max-width: 360px) {
  .row1 {
    flex-direction: column;
    align-items: flex-start;
  }
  .row3 {
    flex-wrap: wrap;
  }
}
</style>
