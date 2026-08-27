import assert from 'node:assert/strict'
import test from 'node:test'
import type { Agent } from 'node:http'
import { installAuthenticationProxy } from './auth-proxy.js'

test('routes authentication fetches through the assigned proxy and restores global fetch', async () => {
  const original = globalThis.fetch
  const marker = {} as Agent
  let receivedAgent: unknown
  const restore = installAuthenticationProxy('http://example.invalid:8080', async (_target, options) => {
    receivedAgent = options.agent
    return { ok: true }
  }, () => marker)

  try {
    await globalThis.fetch('https://login.example.invalid/token', { method: 'POST' })
    assert.equal(receivedAgent, marker)
  } finally {
    restore()
  }
  assert.equal(globalThis.fetch, original)
})
