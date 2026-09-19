import { reactive } from 'vue'
import ru from './locales/ru.json'
import en from './locales/en.json'

// Minimal i18n (NFR-6): flat key lookup in two locale dictionaries,
// no external i18n library (dependency policy, DECISIONS D2/D13).
// t('key', {n: 5}) substitutes {n} placeholders.

export type Lang = 'ru' | 'en'

const messages: Record<Lang, Record<string, string>> = { ru, en }

const state = reactive({ lang: 'ru' as Lang })

export function setLocale(lang: Lang) {
  state.lang = lang
  document.documentElement.lang = lang
}

export function getLocale(): Lang {
  return state.lang
}

export function t(key: string, params?: Record<string, string | number>): string {
  const dict = messages[state.lang] ?? messages.ru
  let s = dict[key] ?? messages.ru[key] ?? key
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      s = s.split('{' + k + '}').join(String(v))
    }
  }
  return s
}

export function fmtBytes(n: number): string {
  // 0/absent quota → "—" (Remnawave panels often omit userinfo).
  if (!n || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  const unit = u === 0 ? t('common.bytes') : units[u]
  return `${v.toFixed(v >= 100 || u === 0 ? 0 : 1)} ${unit}`
}

export function fmtDate(unix: number): string {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleDateString()
}

export default t
