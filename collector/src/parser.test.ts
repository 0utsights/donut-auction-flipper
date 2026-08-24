import assert from 'node:assert/strict'
import test from 'node:test'
import { parseOrder, projectItem } from './parser.js'

test('parses a conservative order fixture', () => {
  const view = projectItem({ name: 'diamond_block', count: 64, stackSize: 64, displayName: 'Diamond Block', nbt: { lore: ['Unit Reward: $5,000', 'Remaining: 640', 'Requested: 1000', 'Queue: #2', 'Buyer: Test_User', 'Order ID: ord_123'] } }, 10)
  assert.ok(view)
  const order = parseOrder(view)
  assert.ok(order)
  assert.equal(order.item_id, 'minecraft:diamond_block')
  assert.equal(order.unit_reward, 5000)
  assert.equal(order.remaining_quantity, 640)
  assert.equal(order.requested_quantity, 1000)
  assert.equal(order.order_key, 'ord_123')
  assert.equal(order.price_position, 2)
  assert.equal(order.signature_complete, false)
})

test('does not guess when price or remaining quantity is absent', () => {
  const view = projectItem({ name: 'sand', count: 64, nbt: { lore: ['Cheap item'] } }, 2)
  assert.ok(view)
  assert.equal(parseOrder(view), undefined)
})

test('rejects fractional currency that cannot resolve to whole dollars', () => {
  const view = projectItem({ name: 'iron_ingot', count: 1, nbt: { lore: ['Unit Reward: $0.01', 'Remaining: 64'] } }, 2)
  assert.ok(view)
  assert.equal(parseOrder(view), undefined)
})
