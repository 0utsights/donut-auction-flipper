import assert from 'node:assert/strict'
import test from 'node:test'
import { parseOrder, projectItem } from './parser.js'

test('parses a conservative order fixture', () => {
  const view = projectItem({ name: 'diamond_block', count: 64, stackSize: 64, displayName: 'Diamond Block', nbt: { lore: ['Unit Reward: $5,000', 'Remaining: 640', 'Requested: 1000', 'Queue: #2', 'Buyer: Test_User', 'Order ID: ord_123'] } }, 10)
  assert.ok(view)
  const order = parseOrder(view)
  assert.ok(order)
  assert.equal(order.item_id, 'minecraft:diamond_block')
  assert.equal(order.unit_reward_cents, 500_000)
  assert.equal(order.remaining_quantity, 640)
  assert.equal(order.requested_quantity, 1000)
  assert.equal(order.order_key, 'ord_123')
  assert.equal(order.price_position, 2)
  assert.equal(order.signature_complete, true)
})

test('does not guess when price or remaining quantity is absent', () => {
  const view = projectItem({ name: 'sand', count: 64, nbt: { lore: ['Cheap item'] } }, 2)
  assert.ok(view)
  assert.equal(parseOrder(view), undefined)
})

test('preserves fractional currency as integer cents', () => {
  const view = projectItem({ name: 'iron_ingot', count: 1, nbt: { lore: ['Unit Reward: $0.01', 'Remaining: 64'] } }, 2)
  assert.ok(view)
  assert.equal(parseOrder(view)?.unit_reward_cents, 1)
})

test('reads modern item component names and lore', () => {
  const view = projectItem({
    name: 'diamond_block', count: 1, stackSize: 64,
    customName: '{"text":"Diamond Block"}',
    customLore: ['{"text":"Unit Reward: $5,000"}', '{"text":"Remaining: 640"}', '{"text":"Requested: 1,000"}']
  }, 3)
  assert.ok(view)
  assert.equal(view.displayName, 'Diamond Block')
  assert.deepEqual(view.text.slice(0, 4), ['Diamond Block', 'Unit Reward: $5,000', 'Remaining: 640', 'Requested: 1,000'])
  assert.equal(parseOrder(view)?.unit_reward_cents, 500_000)
  assert.equal(parseOrder(view)?.signature_complete, true)
})

test('keeps modifier-bearing and variant items out of base auction evidence', () => {
  const enchantedCommodity = projectItem({ name: 'diamond_block', customLore: ['$5Keach', '0/64 Delivered', 'enchantments'] }, 1)
  const variantItem = projectItem({ name: 'enchanted_book', customLore: ['$5Keach', '0/64 Delivered'] }, 2)
  assert.ok(enchantedCommodity)
  assert.ok(variantItem)
  assert.equal(parseOrder(enchantedCommodity)?.signature_complete, false)
  assert.equal(parseOrder(variantItem)?.signature_complete, false)

  const component = projectItem({
    name: 'diamond', customLore: ['$5Keach', '0/64 Delivered'],
    componentMap: new Map([['minecraft:enchantments', { data: { levels: { fortune: 3 } } }]])
  }, 3)
  assert.ok(component)
  assert.equal(parseOrder(component)?.signature_complete, false)
})

test('parses Donut delivered progress as remaining quantity', () => {
  const view = projectItem({
    name: 'elytra', count: 1, stackSize: 1,
    customName: { type: 'compound', value: { text: { type: 'string', value: '' }, extra: { type: 'list', value: { type: 'compound', value: [{ text: { type: 'string', value: 'Elytra' } }] } } } },
    customLore: [
      { type: 'compound', value: { extra: { type: 'list', value: { type: 'compound', value: [{ text: { type: 'string', value: '$ ' } }, { text: { type: 'string', value: '250M ' } }, { text: { type: 'string', value: 'each' } }] } } } },
      { type: 'compound', value: { extra: { type: 'list', value: { type: 'compound', value: [{ text: { type: 'string', value: '116/120 Delivered' } }] } } } }
    ]
  }, 0)
  assert.ok(view)
  const order = parseOrder(view)
  assert.ok(order)
  assert.equal(order.unit_reward_cents, 25_000_000_000)
  assert.equal(order.requested_quantity, 120)
  assert.equal(order.remaining_quantity, 4)
  assert.equal(order.signature_complete, false)
})

test('parses abbreviated Donut order totals without inflating remaining units', () => {
  const view = projectItem({ name: 'golden_apple', customLore: ['$19.1Keach', '620k/700k Delivered'] }, 1)
  assert.ok(view)
  const order = parseOrder(view)
  assert.ok(order)
  assert.equal(order.unit_reward_cents, 1_910_000)
  assert.equal(order.requested_quantity, 700_000)
  assert.equal(order.remaining_quantity, 80_000)
})
