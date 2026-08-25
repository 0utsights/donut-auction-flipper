export function redactSensitiveText(value: unknown): string {
  let text: string
  try { text = typeof value === 'string' ? value : JSON.stringify(value ?? '') } catch { text = String(value ?? '') }
  return text
    .replace(/\b(Bearer)\s+[^\s,;]+/gi, '$1 [redacted]')
    .replace(/([a-z][a-z0-9+.-]*:\/\/)[^\s/@]+(?::[^\s/@]*)?@/gi, '$1[redacted]@')
    .replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, '[redacted-email]')
}
