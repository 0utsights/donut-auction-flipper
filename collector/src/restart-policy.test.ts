import assert from 'node:assert/strict'
import test from 'node:test'
import { restartDelay } from './restart-policy.js'

test('backs off repeated login failures long enough for server cooldowns', () => {
  assert.equal(restartDelay(1), 2_000)
  assert.equal(restartDelay(5), 32_000)
  assert.equal(restartDelay(8), 256_000)
  assert.equal(restartDelay(9), 300_000)
  assert.equal(restartDelay(30), 300_000)
})
