import assert from 'node:assert/strict'
import test from 'node:test'
import { backendRetryDelay, taskResultForFailure } from './task-policy.js'

test('automatic focused menu failures cool down instead of monopolizing the observer', () => {
  assert.equal(taskResultForFailure('focused_watch', 75, 'menu_session_ended'), 'complete')
  assert.equal(taskResultForFailure('focused_watch', 50, 'menu_session_ended'), 'complete')
  assert.equal(taskResultForFailure('focused_watch', 100, 'menu_session_ended'), 'retry')
  assert.equal(taskResultForFailure('discovery', 10, 'menu_session_ended'), 'retry')
})

test('schema and planned-rotation outcomes retain fail-closed semantics', () => {
  assert.equal(taskResultForFailure('focused_watch', 75, 'schema_hold'), 'failed')
  assert.equal(taskResultForFailure('focused_watch', 75, 'reconnect_required'), 'complete')
  assert.equal(taskResultForFailure('focused_watch', 75, 'other'), 'retry')
})

test('backend retry stays bounded without forcing a Minecraft reconnect', () => {
  assert.equal(backendRetryDelay(1), 1_000)
  assert.equal(backendRetryDelay(2), 2_000)
  assert.equal(backendRetryDelay(6), 30_000)
  assert.equal(backendRetryDelay(100), 30_000)
})
