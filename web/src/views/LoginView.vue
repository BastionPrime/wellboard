<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { t } from '../i18n'
import { APIError } from '../api'
import { useAuthStore } from '../stores/auth'

// Login screen (OPE-3402): the router's own root password, verified by
// the daemon through ubus — no second password to remember.
const auth = useAuthStore()
const router = useRouter()
const password = ref('')
const busy = ref(false)
const error = ref('')

async function submit() {
  if (!password.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await auth.login(password.value)
    password.value = ''
    await router.push('/dashboard')
  } catch (e) {
    error.value = e instanceof APIError ? e.message : String(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="login">
    <h1>{{ t('auth.title') }}</h1>
    <p class="hint">{{ t('auth.subtitle') }}</p>
    <form class="form" @submit.prevent="submit">
      <label class="label" for="wb-password">{{ t('auth.password') }}</label>
      <input
        id="wb-password"
        v-model="password"
        class="input"
        type="password"
        name="password"
        autocomplete="current-password"
        :placeholder="t('auth.password')"
        autofocus
      />
      <button class="submit" type="submit" :disabled="busy || !password">
        {{ busy ? t('auth.signingIn') : t('auth.signIn') }}
      </button>
    </form>
    <p v-if="error" class="error">{{ t('auth.failed', { msg: error }) }}</p>
  </section>
</template>

<style scoped>
.login {
  max-width: 360px;
  margin: 3rem auto;
  padding: 1.25rem;
  border: 1px solid #e2e6ec;
  border-radius: 10px;
  background: #fff;
}
h1 {
  margin: 0 0 0.5rem;
  font-size: 1.2rem;
}
.hint {
  margin: 0 0 1rem;
  color: #5a6472;
  font-size: 0.9rem;
}
.form {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.label {
  font-size: 0.85rem;
  color: #3c4654;
}
.input {
  padding: 0.5rem 0.6rem;
  border: 1px solid #ccd3dc;
  border-radius: 6px;
  font: inherit;
}
.submit {
  margin-top: 0.25rem;
  padding: 0.55rem 0.75rem;
  border: 0;
  border-radius: 6px;
  background: #2563eb;
  color: #fff;
  font: inherit;
  cursor: pointer;
}
.submit:disabled {
  opacity: 0.6;
  cursor: default;
}
.error {
  margin-top: 0.75rem;
  color: #b91c1c;
  font-size: 0.9rem;
}
</style>