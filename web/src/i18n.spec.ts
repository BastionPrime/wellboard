import { describe, it, expect } from 'vitest'
import ru from './locales/ru.json'
import en from './locales/en.json'

// i18n completeness (flat keys, no i18n library — NFR-6, DECISIONS D2/D13):
// the SPA is RU/EN and a key that exists in only one locale (or resolves to
// an empty string) silently renders blank in the other language. This spec
// fails the build the moment the two dictionaries drift apart.

const locales: Record<string, Record<string, unknown>> = { ru, en }

describe('i18n locale completeness (ru/en)', () => {
  const ruKeys = Object.keys(ru).sort()
  const enKeys = Object.keys(en).sort()

  it('en.json has every key from ru.json', () => {
    expect(enKeys.filter((k) => !(k in ru))).toEqual([])
  })

  it('ru.json has every key from en.json', () => {
    expect(ruKeys.filter((k) => !(k in en))).toEqual([])
  })

  it('both locales have identical key sets', () => {
    expect(enKeys).toEqual(ruKeys)
  })

  it('no key maps to an empty or whitespace-only value', () => {
    for (const [lang, dict] of Object.entries(locales)) {
      for (const [key, value] of Object.entries(dict)) {
        expect(
          typeof value === 'string' && value.trim().length > 0,
          `${lang}: "${key}" is missing, non-string or empty`,
        ).toBe(true)
      }
    }
  })

  it('every value is a flat string (nested objects are not supported by t())', () => {
    for (const [lang, dict] of Object.entries(locales)) {
      for (const [key, value] of Object.entries(dict)) {
        expect(typeof value, `${lang}: "${key}" must be a string, not nested`).toBe('string')
      }
    }
  })
})
