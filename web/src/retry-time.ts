export function retryIn(value: string, now = Date.now()): string {
  const remaining = Math.max(0, Math.ceil((Date.parse(value) - now) / 1000))
  const minutes = Math.floor(remaining / 60)
  const seconds = remaining % 60
  return minutes ? `${minutes}m ${seconds}s` : `${seconds}s`
}