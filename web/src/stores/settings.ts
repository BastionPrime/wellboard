import { defineStore } from 'pinia'
import { api, type Settings } from '../api'

export type Lang = 'ru' | 'en'

// Settings store (FR-9.1): language (NFR-6), UI port, geodata source,
// default policy. The language is applied to the i18n composable via
// setLocale on change.
export const useSettingsStore = defineStore('settings', {
  state: () => ({
    settings: null as Settings | null,
    loading: false,
    error: '' as string | null,
    saveError: '' as string | null,
    lang: 'ru' as Lang, // mirrored UI locale (set from settings.lang on load)
  }),
  actions: {
    async load() {
      this.loading = true
      this.error = null
      try {
        const st = await api<Settings>('/settings')
        this.settings = st
        this.lang = st.lang === 'en' ? 'en' : 'ru'
      } catch (e) {
        this.error = e instanceof Error ? e.message : String(e)
      } finally {
        this.loading = false
      }
    },
    // patch sends the mutable subset; on success the full settings
    // object is replaced with the server response.
    async patch(fields: Partial<Settings>) {
      this.saveError = null
      try {
        const st = await api<Settings>('/settings', {
          method: 'PATCH',
          body: JSON.stringify(fields),
        })
        this.settings = st
        if (st.lang === 'en' || st.lang === 'ru') this.lang = st.lang
        return st
      } catch (e) {
        this.saveError = e instanceof Error ? e.message : String(e)
        throw e
      }
    },
    async setLang(lang: Lang) {
      this.lang = lang // apply immediately for instant UI feedback
      await this.patch({ lang })
    },
  },
})
