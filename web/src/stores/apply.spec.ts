import { describe, it, expect, vi, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useApplyStore } from './apply'
import { APIError } from '../api'

// Mock the api module: /pending, /applied, /apply, /rollback.
vi.mock('../api', async (importOriginal) => {
  const orig = await importOriginal<typeof import('../api')>()
  const fake = vi.fn()
  return {
    ...orig,
    api: fake,
  }
})

// Re-import after mock so the store uses the fake api.
import { api } from '../api'

describe('apply store (FR-6)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(api).mockReset()
  })

  it('refreshPending maps the /pending payload', async () => {
    vi.mocked(api).mockResolvedValueOnce({ pending: true, hash: 'abc' })
    const store = useApplyStore()
    await store.refreshPending()
    expect(store.pending).toBe(true)
    expect(store.hash).toBe('abc')
    expect(vi.mocked(api)).toHaveBeenCalledWith('/pending')
  })

  it('refreshPending swallows errors (endpoint not wired)', async () => {
    vi.mocked(api).mockRejectedValueOnce(new Error('down'))
    const store = useApplyStore()
    store.pending = false
    await store.refreshPending()
    expect(store.pending).toBe(false) // unchanged
  })

  it('loadHistory stores the applied list', async () => {
    vi.mocked(api).mockResolvedValueOnce({ applied: [{ profile: 'p1', state_hash: 'h', applied_at: '2026-09-20T00:00:00Z' }] })
    const store = useApplyStore()
    await store.loadHistory()
    expect(store.history).toHaveLength(1)
    expect(store.history[0].profile).toBe('p1')
    expect(store.canRollback).toBe(false)
  })

  it('canRollback requires two history records', async () => {
    vi.mocked(api).mockResolvedValueOnce({
      applied: [
        { profile: 'p1', state_hash: 'h', applied_at: '2026-09-20T00:00:00Z' },
        { profile: 'p2', state_hash: 'h', applied_at: '2026-09-20T01:00:00Z' },
      ],
    })
    const store = useApplyStore()
    await store.loadHistory()
    expect(store.canRollback).toBe(true)
  })

  it('apply stores the success result and refreshes pending+history', async () => {
    vi.mocked(api)
      // /apply
      .mockResolvedValueOnce({ ok: true, profile: 'p9', applied_at: '2026-09-20T02:00:00Z' })
      // /pending (finally)
      .mockResolvedValueOnce({ pending: false, hash: 'x' })
      // /applied (finally)
      .mockResolvedValueOnce({ applied: [] })
    const store = useApplyStore()
    await store.apply()
    expect(store.applying).toBe(false)
    expect(store.lastResult?.ok).toBe(true)
    expect(store.lastResult?.profile).toBe('p9')
    expect(store.pending).toBe(false)
  })

  it('apply failure (409 flow result) records the error message', async () => {
    vi.mocked(api)
      // /apply → reject with APIError like the real fetch wrapper
      .mockRejectedValueOnce(new APIError(409, 'mihomo -t failed: proxy [x] not found'))
      // /pending
      .mockResolvedValueOnce({ pending: true, hash: 'x' })
      // /applied
      .mockResolvedValueOnce({ applied: [] })
    const store = useApplyStore()
    await expect(store.apply()).rejects.toBeInstanceOf(APIError)
    expect(store.applying).toBe(false)
    expect(store.applyError).toContain('proxy [x] not found')
    // The pending indicator stays true: nothing was applied.
    expect(store.pending).toBe(true)
  })

  it('rollback posts and refreshes', async () => {
    vi.mocked(api)
      .mockResolvedValueOnce({ status: 'rolled back', profile: 'p1' })
      .mockResolvedValueOnce({ pending: true, hash: 'y' })
      .mockResolvedValueOnce({ applied: [] })
    const store = useApplyStore()
    await store.rollback()
    expect(vi.mocked(api)).toHaveBeenCalledWith('/rollback', { method: 'POST' })
    expect(store.rollingBack).toBe(false)
  })
})
