import assert from 'node:assert/strict'
import test from 'node:test'
import { redactSensitiveText } from './redaction.js'

test('redacts proxy userinfo, email addresses, and bearer credentials', () => {
  const source = 'connect http://proxy-user:proxy-pass@proxy.example:8080 as observer@example.test Authorization: Bearer secret-token'
  const redacted = redactSensitiveText(source)
  for (const secret of ['proxy-user', 'proxy-pass', 'observer@example.test', 'secret-token']) assert.equal(redacted.includes(secret), false)
  assert.match(redacted, /http:\/\/\[redacted\]@proxy\.example:8080/)
})
