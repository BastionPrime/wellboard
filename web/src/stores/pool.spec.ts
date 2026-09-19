import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import {
  usePoolStore,
  serializeConditions,
  validateRouteInput,
  routePayload,
  type RouteInput,
} from './pool'

// Route form logic tests (TZ §8: "critical components — condition form"):
// condition serialization, validation and the exact POST/PATCH payload.
// The Go handler contract lives in internal/api/api.go (routeIn).

function mockFetch(status: number, body: unknown) {
  return vi.fn().mockResolvedValue({
    ok: status >= 200 && status < 300,
    status,
    statusText: 'status ' + status,
    text: () => Promise.resolve(JSON.stringify(body)),
  }) as unknown as typeof fetch
}

function baseInput(): RouteInput {
  return {
    name: ' TV route ',
    enabled: true,
    order: null,
    conditions: [
      { type: 'domain-suffix', value: ' netflix.com ' },
      { type: 'domain-suffix', value: 'netflix.com' }, // duplicate after trim
      { type: 'dst-port', value: '' }, // empty row dropped
    ],
    providers: [],
    target: { type: 'group', id: 'grp_1' },
    onUnavailable: 'block',
  }
}

describe('serializeConditions', () => {
  it('trims values, drops empty rows and deduplicates type+value pairs', () => {
    const out = serializeConditions([
      { type: 'domain-suffix', value: ' netflix.com ' },
      { type: 'domain-suffix', value: 'netflix.com' },
      { type: 'domain', value: '' },
      { type: 'domain', value: '   ' },
      { type: 'ip-cidr', value: '192.168.1.0/24' },
    ])
    expect(out).toEqual([
      { type: 'domain-suffix', value: 'netflix.com' },
      { type: 'ip-cidr', value: '192.168.1.0/24' },
    ])
  })

  it('same value with different types is NOT a duplicate', () => {
    const out = serializeConditions([
      { type: 'domain', value: 'x.com' },
      { type: 'domain-suffix', value: 'x.com' },
    ])
    expect(out).toHaveLength(2)
  })
})

describe('validateRouteInput', () => {
  it('accepts a well-formed route', () => {
    expect(validateRouteInput(baseInput())).toEqual([])
  })

  it('rejects a missing name', () => {
    const input = baseInput()
    input.name = '  '
    expect(validateRouteInput(input)).toContain('name required')
  })

  it('rejects empty conditions and providers', () => {
    const input = baseInput()
    input.conditions = []
    expect(validateRouteInput(input)).toContain('at least one condition or provider required')
  })

  it('providers alone satisfy the condition requirement', () => {
    const input = baseInput()
    input.conditions = []
    input.providers = ['ads']
    expect(validateRouteInput(input)).toEqual([])
  })

  it('flags a bad ip-cidr / src-device value', () => {
    const input = baseInput()
    input.conditions = [
      { type: 'ip-cidr', value: '1.2.3.4/99' },
      { type: 'src-device', value: 'not-an-ip' },
    ]
    const problems = validateRouteInput(input)
    expect(problems.some((p) => p.includes('ip-cidr'))).toBe(true)
    expect(problems.some((p) => p.includes('src-device'))).toBe(true)
  })

  it('flags a bad dst-port and reversed ranges', () => {
    const input = baseInput()
    input.conditions = [
      { type: 'dst-port', value: '99999' },
      { type: 'dst-port', value: '500-100' },
    ]
    const problems = validateRouteInput(input)
    expect(problems.filter((p) => p.includes('dst-port'))).toHaveLength(2)
  })

  it('flags domains with spaces or a leading dot', () => {
    const input = baseInput()
    input.conditions = [
      { type: 'domain', value: 'a b.com' },
      { type: 'domain-suffix', value: '.com' },
    ]
    expect(validateRouteInput(input).filter((p) => p.includes('bad domain'))).toHaveLength(2)
  })

  it('requires an id for server/group targets', () => {
    const input = baseInput()
    input.target = { type: 'server' }
    expect(validateRouteInput(input)).toContain('target id required for server/group targets')
  })

  it('direct/reject targets need no id', () => {
    const input = baseInput()
    input.target = { type: 'direct' }
    expect(validateRouteInput(input)).toEqual([])
  })
})

describe('routePayload', () => {
  it('serializes the exact API body (trimmed name, deduped conditions)', () => {
    const payload = routePayload(baseInput())
    expect(payload).toEqual({
      name: 'TV route',
      enabled: true,
      conditions: [{ type: 'domain-suffix', value: 'netflix.com' }],
      target: { type: 'group', id: 'grp_1' },
      on_unavailable: 'block',
    })
  })

  it('keeps providers only when non-empty and order only when set', () => {
    const input = baseInput()
    input.providers = [' ads ', '']
    input.order = 42
    const payload = routePayload(input)
    expect(payload.providers).toEqual(['ads'])
    expect(payload.order).toBe(42)
  })

  it('direct/reject targets serialize without an id', () => {
    const input = baseInput()
    input.target = { type: 'reject' }
    expect(routePayload(input).target).toEqual({ type: 'reject' })
  })
})

describe('pool store route actions', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('createRoute() POSTs the serialized form to /api/v1/routes', async () => {
    const created = {
      id: 'rt_9',
      name: 'TV route',
      enabled: true,
      order: 10,
      conditions: [{ type: 'domain-suffix', value: 'netflix.com' }],
      target: { type: 'group', id: 'grp_1' },
      on_unavailable: 'block',
    }
    const fetchMock = mockFetch(201, created)
    vi.stubGlobal('fetch', fetchMock)
    const store = usePoolStore()
    await store.createRoute(baseInput())
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/routes')
    expect((call[1] as RequestInit).method).toBe('POST')
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({
      name: 'TV route',
      enabled: true,
      conditions: [{ type: 'domain-suffix', value: 'netflix.com' }],
      target: { type: 'group', id: 'grp_1' },
      on_unavailable: 'block',
    })
    expect(store.routes).toHaveLength(1)
    vi.unstubAllGlobals()
  })

  it('updateRoute() PATCHes /api/v1/routes/{id} and replaces the route', async () => {
    const store = usePoolStore()
    store.routes = [
      {
        id: 'rt_1',
        name: 'Old',
        enabled: true,
        order: 10,
        conditions: [],
        target: { type: 'direct' },
        on_unavailable: 'block',
      },
    ]
    const updated = {
      id: 'rt_1',
      name: 'TV route',
      enabled: false,
      order: 20,
      conditions: [{ type: 'domain-suffix', value: 'netflix.com' }],
      target: { type: 'direct' },
      on_unavailable: 'direct',
    }
    const fetchMock = mockFetch(200, updated)
    vi.stubGlobal('fetch', fetchMock)
    await store.updateRoute('rt_1', baseInput())
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/routes/rt_1')
    expect((call[1] as RequestInit).method).toBe('PATCH')
    expect(store.routes[0].name).toBe('TV route')
    vi.unstubAllGlobals()
  })

  it('createGroup() POSTs name/type/members and deleteGroup() DELETEs', async () => {
    const store = usePoolStore()
    const group = { id: 'grp_9', name: 'NL pool', type: 'url-test', members: ['srv_1'] }
    const fetchMock = mockFetch(201, group)
    vi.stubGlobal('fetch', fetchMock)
    await store.createGroup({ name: 'NL pool', type: 'url-test', members: ['srv_1'] })
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/groups')
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({
      name: 'NL pool',
      type: 'url-test',
      members: ['srv_1'],
    })
    vi.stubGlobal('fetch', mockFetch(200, { status: 'deleted' }))
    await store.deleteGroup('grp_9')
    expect(store.groups).toHaveLength(0)
    vi.unstubAllGlobals()
  })

  it('addManualServers() POSTs links to /api/v1/servers (FR-1.3)', async () => {
    const store = usePoolStore()
    const fetchMock = mockFetch(201, {
      servers: [{ id: 'srv_abc', source_id: 'sub_3', name: 'ManualSS', type: 'ss' }],
    })
    vi.stubGlobal('fetch', fetchMock)
    const out = await store.addManualServers('ss://…@1.2.3.4:8388#ManualSS')
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/servers')
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({
      links: 'ss://…@1.2.3.4:8388#ManualSS',
    })
    expect(out).toHaveLength(1)
    expect(store.servers[0].name).toBe('ManualSS')
    vi.unstubAllGlobals()
  })

  it('loadLAN() sets lanUnavailable on failure and fills devices on success', async () => {
    const store = usePoolStore()
    vi.stubGlobal('fetch', mockFetch(503, { error: 'LAN device listing unavailable' }))
    await store.loadLAN()
    expect(store.lanDevices).toBeNull()
    expect(store.lanUnavailable).toBe(true)
    vi.stubGlobal(
      'fetch',
      mockFetch(200, {
        devices: [{ mac: 'aa:bb:cc:dd:ee:ff', ip: '192.168.1.50', hostname: 'tv', static: false }],
      }),
    )
    await store.loadLAN()
    expect(store.lanDevices).toHaveLength(1)
    expect(store.lanUnavailable).toBe(false)
    vi.unstubAllGlobals()
  })

  it('pinStatic() POSTs mac/ip/hostname to /lan-devices/static', async () => {
    const fetchMock = mockFetch(200, {
      status: 'simulated',
      detail: 'lan: static lease not applied in dev mode',
    })
    vi.stubGlobal('fetch', fetchMock)
    const store = usePoolStore()
    const out = await store.pinStatic({ mac: 'aa:bb:cc:dd:ee:ff', ip: '192.168.1.50', hostname: 'tv' })
    const call = (fetchMock as unknown as { mock: { calls: unknown[][] } }).mock.calls[0]
    expect(call[0]).toBe('/api/v1/lan-devices/static')
    expect((call[1] as RequestInit).method).toBe('POST')
    expect(JSON.parse((call[1] as RequestInit).body as string)).toEqual({
      mac: 'aa:bb:cc:dd:ee:ff',
      ip: '192.168.1.50',
      hostname: 'tv',
    })
    expect(out.status).toBe('simulated')
    vi.unstubAllGlobals()
  })
})
