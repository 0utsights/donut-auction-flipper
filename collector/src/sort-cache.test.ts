import test from 'node:test'
import assert from 'node:assert/strict'
import { MOST_PER_ITEM_RECHECK_MS, shouldReverifyMostPerItem } from './observer.js'

test('reuses a recent session sort proof but rechecks it at the bounded deadline', () => {
  const now = 1_000_000
  assert.equal(shouldReverifyMostPerItem(now - MOST_PER_ITEM_RECHECK_MS + 1, now, MOST_PER_ITEM_RECHECK_MS), false)
  assert.equal(shouldReverifyMostPerItem(now - MOST_PER_ITEM_RECHECK_MS, now, MOST_PER_ITEM_RECHECK_MS), true)
})

test('fails closed for missing, future, or invalid sort proof timing', () => {
  assert.equal(shouldReverifyMostPerItem(0, 1_000, MOST_PER_ITEM_RECHECK_MS), true)
  assert.equal(shouldReverifyMostPerItem(2_000, 1_000, MOST_PER_ITEM_RECHECK_MS), true)
  assert.equal(shouldReverifyMostPerItem(1_000, 2_000, 0), true)
})
