import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSourcesStore } from './sources'

// Mock the global fetch used by api() — store tests run in node env.
function mockFetchOnce(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: 'status ' + status,
    text: () => Promise.resolve(JSON.stringify(body)),
  }) as unknown as typeof fetch
}

describe('sources store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('load() populates sources from GET /api/v1/sources', async () => {
    const fetchMock = mockFetchOnce(200, {
      sources: [
        { id: 'sub_1', kind: 'subscription', name: 'Main', url: 'https://panel.example/sub', enabled: true },
        { id: 'sub_2', kind: 'manual', name: 'Manual' },
      ],
    })
    vi.stubGlobal('fetch', fetchMock)
    const store = useSourcesStore()
    await store.load()
    expect(store.sources).toHaveLength(2)
    expect(store.sources[0].id).toBe('sub_1')
    expect(store.subscriptionCount).toBe(1)
    expect(store.error).toBeNull()
    expect((fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0][0]).toBe('/api/v1/sources')
    vi.unstubAllGlobals()
  })

  it('load() records the error message on failure', async () => {
    vi.stubGlobal('fetch', mockFetchOnce(500, { error: 'boom' }))
    const store = useSourcesStore()
    await store.load()
    expect(store.sources).toHaveLength(0)
    expect(store.error).toBe('boom')
    vi.unstubAllGlobals()
  })

  it('create() POSTs the payload and appends the created source', async () => {
    const created = { id: 'sub_9', kind: 'subscription', name: 'New', url: 'https://x.example/s' }
    const fetchMock = mockFetchOnce(201, created)
    vi.stubGlobal('fetch', fetchMock)
    const store = useSourcesStore()
    const out = await store.create({ kind: 'subscription', name: 'New', url: 'https://x.example/s' })
    expect(out.id).toBe('sub_9')
    expect(store.sources).toHaveLength(1)
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/sources')
    expect((call[1] as RequestInit).method).toBe('POST')
    expect(JSON.parse((call[1] as RequestInit).body as string).name).toBe('New')
    vi.unstubAllGlobals()
  })

  it('remove() drops the source after a successful DELETE', async () => {
    vi.stubGlobal('fetch', mockFetchOnce(200, { sources: [{ id: 'sub_1', kind: 'manual', name: 'M' }] }))
    const store = useSourcesStore()
    await store.load()
    vi.stubGlobal('fetch', mockFetchOnce(200, { status: 'deleted' }))
    await store.remove('sub_1')
    expect(store.sources).toHaveLength(0)
    vi.unstubAllGlobals()
  })
})
