import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveMinecraftDestination } from './proxy.js'

test('resolves the Minecraft SRV target used by a proxied connection', async () => {
  const destination = await resolveMinecraftDestination('play.example.test', 25565, async hostname => {
    assert.equal(hostname, '_minecraft._tcp.play.example.test')
    return [
      { name: 'secondary.example.test.', port: 25566, priority: 20, weight: 100 },
      { name: 'java.example.test.', port: 25567, priority: 10, weight: 5 }
    ]
  })
  assert.deepEqual(destination, { host: 'java.example.test', port: 25567 })
})

test('does not perform SRV discovery for an explicit non-default port', async () => {
  let called = false
  const destination = await resolveMinecraftDestination('play.example.test', 25570, async () => {
    called = true
    return []
  })
  assert.deepEqual(destination, { host: 'play.example.test', port: 25570 })
  assert.equal(called, false)
})

test('falls back to the configured endpoint when SRV discovery fails', async () => {
  const destination = await resolveMinecraftDestination('play.example.test', 25565, async () => { throw new Error('dns unavailable') })
  assert.deepEqual(destination, { host: 'play.example.test', port: 25565 })
})
