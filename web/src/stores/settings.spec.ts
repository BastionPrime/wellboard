import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSettingsStore } from './settings'

function mockFetchOnce(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: 'status ' + status,
    text: () => Promise.resolve(JSON.stringify(body)),
  }) as unknown as typeof fetch
}

describe('settings store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('load() applies settings.lang to the store locale', async () => {
    vi.stubGlobal(
      'fetch',
      mockFetchOnce(200, {
        ui_port: 8090,
        lang: 'en',
        geodata: 'runetfreedom',
        default_policy: { type: 'direct' },
        delay_test_interval_sec: 300,
      }),
    )
    const store = useSettingsStore()
    await store.load()
    expect(store.settings?.ui_port).toBe(8090)
    expect(store.lang).toBe('en')
    vi.unstubAllGlobals()
  })

  it('load() falls back to ru for unknown lang values', async () => {
    vi.stubGlobal(
      'fetch',
      mockFetchOnce(200, {
        ui_port: 8090,
        lang: 'de',
        geodata: 'metacubex',
        default_policy: { type: 'reject' },
        delay_test_interval_sec: 0,
      }),
    )
    const store = useSettingsStore()
    await store.load()
    expect(store.lang).toBe('ru')
    vi.unstubAllGlobals()
  })

  it('patch() sends the partial body and replaces settings', async () => {
    vi.stubGlobal(
      'fetch',
      mockFetchOnce(200, {
        ui_port: 8090,
        lang: 'ru',
        geodata: 'runetfreedom',
        default_policy: { type: 'direct' },
        delay_test_interval_sec: 0,
      }),
    )
    const store = useSettingsStore()
    await store.load()
    const patched = {
      ui_port: 8090,
      lang: 'en',
      geodata: 'runetfreedom',
      default_policy: { type: 'direct' },
      delay_test_interval_sec: 600,
    }
    const fetchMock = mockFetchOnce(200, patched)
    vi.stubGlobal('fetch', fetchMock)
    await store.patch({ delay_test_interval_sec: 600 })
    expect(store.settings?.delay_test_interval_sec).toBe(600)
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect((call[1] as RequestInit).method).toBe('PATCH')
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({ delay_test_interval_sec: 600 })
    vi.unstubAllGlobals()
  })

  it('patch() surfaces the server error and rethrows', async () => {
    vi.stubGlobal('fetch', mockFetchOnce(400, { error: 'ui_port must be 1-65535' }))
    const store = useSettingsStore()
    await expect(store.patch({ ui_port: 0 })).rejects.toThrow('ui_port must be 1-65535')
    expect(store.saveError).toBe('ui_port must be 1-65535')
    vi.unstubAllGlobals()
  })
})
