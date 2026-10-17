import { describe, expect, it } from 'vitest'
import { retryIn } from './retry-time'

describe('retryIn', () => {
  it('formats the remaining retry duration', () => {
    const now = Date.parse('2026-10-17T12:00:00Z')
    expect(retryIn('2026-10-17T12:02:05Z', now)).toBe('2m 5s')
    expect(retryIn('2026-10-17T12:00:05Z', now)).toBe('5s')
  })

  it('never displays a negative duration', () => {
    expect(retryIn('2026-10-17T11:59:00Z', Date.parse('2026-10-17T12:00:00Z'))).toBe('0s')
  })
})