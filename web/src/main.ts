import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { setLocale } from './i18n'

// Entry point. The initial locale comes from GET /api/v1/settings
// (router guards on /wizard handle the store bootstrap); the default
// before load is RU (decision Q11).
setLocale('ru')

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
