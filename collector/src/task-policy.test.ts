import assert from 'node:assert/strict'
import test from 'node:test'
import { backendRetryDelay, guardedInteractionDelay, minecraftReconnectDelay, taskResultForFailure } from './task-policy.js'

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
  assert.equal(taskResultForFailure('discovery', 10, 'menu_reset_required'), 'complete')
  assert.equal(taskResultForFailure('focused_watch', 75, 'menu_reset_required'), 'complete')
  assert.equal(taskResultForFailure('focused_watch', 100, 'menu_reset_required'), 'retry')
})

test('temporarily slows menu interaction after an invalid-sequence kick', () => {
  assert.equal(guardedInteractionDelay(500, 20_000, 10_000), 750)
  assert.equal(guardedInteractionDelay(500, 10_000, 10_000), 500)
  assert.throws(() => guardedInteractionDelay(-1, 0, 0), /invalid interaction delay/)
})

test('backend retry stays bounded without forcing a Minecraft reconnect', () => {
  assert.equal(backendRetryDelay(1), 1_000)
  assert.equal(backendRetryDelay(2), 2_000)
  assert.equal(backendRetryDelay(6), 30_000)
  assert.equal(backendRetryDelay(100), 30_000)
})

test('intentional Minecraft rotations wait for Donut to retire the old session', () => {
  assert.equal(minecraftReconnectDelay(1_000, 160_000, 100_000), 60_000)
  assert.equal(minecraftReconnectDelay(30_000, 110_000, 100_000), 30_000)
  assert.equal(minecraftReconnectDelay(4_000, 90_000, 100_000), 4_000)
})
