import { describe, it, expect } from 'vitest'
import { formatDelay } from './api'

// Per-server delay test (OPE-4115): the servers table formats measured
// latency through formatDelay — never inline.
describe('formatDelay', () => {
  it('renders a positive delay as "<ms> ms"', () => {
    expect(formatDelay(123)).toBe('123 ms')
    expect(formatDelay(1)).toBe('1 ms')
  })

  it('renders 0 / negative / nullish as empty (not tested)', () => {
    expect(formatDelay(0)).toBe('')
    expect(formatDelay(-5)).toBe('')
    expect(formatDelay(null)).toBe('')
    expect(formatDelay(undefined)).toBe('')
  })
})
