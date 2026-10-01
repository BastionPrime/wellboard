import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { installUnauthorizedRedirect, router } from './router'
import { setLocale } from './i18n'

// Entry point. The initial locale comes from GET /api/v1/settings
// (router guards on /wizard handle the store bootstrap); the default
// before load is RU (decision Q11).
setLocale('ru')

// A 401 anywhere (expired session, daemon restart) sends the browser
// back to the login screen instead of leaving a half-empty board.
installUnauthorizedRedirect()

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
