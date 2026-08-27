import assert from 'node:assert/strict'
import test from 'node:test'
import { SafeNavigator, type NavigationBot } from './safe-navigation.js'
import type { MenuSchema } from './types.js'

function fixture(): { bot: NavigationBot & { commands: string[]; clicks: number[] }; schemas: MenuSchema[]; slots: Array<{ name?: string; displayName?: string; customName?: unknown } | null> } {
  const slots: Array<{ name?: string; displayName?: string; customName?: unknown } | null> = Array.from({ length: 54 }, () => null)
  const bot = {
    commands: [] as string[], clicks: [] as number[],
    currentWindow: { title: 'Orders - Page 1/3', slots },
    chat(command: string) { this.commands.push(command) },
    async clickWindow(slot: number) { this.clicks.push(slot) }
  }
  slots[50] = { name: 'minecraft:arrow', displayName: 'Next Page' }
  return { bot, slots, schemas: [{ id: 'fixture', title: /^Orders/, listingSlots: new Set([0, 1]), controls: new Map([[50, { kind: 'next_page', itemName: 'minecraft:arrow', label: /^Next Page$/i }]]) }] }
}

test('only the exact orders command is allowed', () => {
  const { bot, schemas } = fixture(); const navigator = new SafeNavigator(bot, schemas)
  navigator.sendCommand('/orders')
  assert.deepEqual(bot.commands, ['/orders'])
  assert.throws(() => navigator.sendCommand('/orders buy diamond'), /not allowlisted/)
  assert.throws(() => navigator.sendCommand('/ah'), /not allowlisted/)
})

test('allows only one canonical item argument for read-only order search', () => {
  const { bot, schemas } = fixture(); const navigator = new SafeNavigator(bot, schemas)
  navigator.searchOrders('minecraft:redstone_block')
  assert.deepEqual(bot.commands, ['/orders redstone_block'])
  assert.throws(() => navigator.searchOrders('minecraft:redstone block'), /not allowlisted/)
  assert.throws(() => navigator.searchOrders('minecraft:my'), /not allowlisted/)
  assert.throws(() => navigator.searchOrders('minecraft:reload'), /not allowlisted/)
  assert.throws(() => navigator.searchOrders('diamond_block'), /not allowlisted/)
  assert.throws(() => navigator.searchOrders('minecraft:diamond_block extra'), /not allowlisted/)
})

test('validates controls using custom component labels', () => {
  const { bot, schemas, slots } = fixture()
  slots[50] = { name: 'minecraft:arrow', displayName: 'Arrow', customName: { type: 'string', value: 'Next Page' } }
  const navigator = new SafeNavigator(bot, schemas)
  assert.equal(navigator.controlAvailable('next_page'), true)
})

test('clicks only a verified non-transactional control', async () => {
  const { bot, schemas, slots } = fixture(); const navigator = new SafeNavigator(bot, schemas)
  assert.equal(navigator.controlAvailable('next_page'), true)
  await navigator.clickControl('next_page')
  assert.deepEqual(bot.clicks, [50])
  slots[50] = { name: 'minecraft:diamond', displayName: 'Buy Now' }
  assert.equal(navigator.controlAvailable('next_page'), false)
  await assert.rejects(navigator.clickControl('next_page'), /navigation denied/)
  assert.deepEqual(bot.clicks, [50])
})

test('allows only the exact verified hopper filter', async () => {
  const { bot, schemas, slots } = fixture()
  slots[47] = { name: 'minecraft:hopper', displayName: 'Filter' }
  schemas[0]!.controls = new Map([...schemas[0]!.controls, [47, { kind: 'filter', itemName: 'minecraft:hopper', label: /^Filter$/i }]])
  const navigator = new SafeNavigator(bot, schemas)
  await navigator.clickControl('filter')
  assert.deepEqual(bot.clicks, [47])
  slots[47] = { name: 'minecraft:hopper', displayName: 'Create Order' }
  assert.equal(navigator.controlAvailable('filter'), false)
  await assert.rejects(navigator.clickControl('filter'), /navigation denied/)
})

test('unknown windows are capture-only', async () => {
  const { bot } = fixture(); const navigator = new SafeNavigator(bot, [])
  await assert.rejects(navigator.clickControl('next_page'), /unknown order screen/)
  assert.deepEqual(bot.clicks, [])
})

test('matches Mineflayer chat-component window titles', () => {
  const { bot, schemas } = fixture()
  bot.currentWindow = { ...bot.currentWindow!, title: { type: 'string', value: 'Orders - Page 1/3' } }
  const navigator = new SafeNavigator(bot, schemas)
  assert.equal(navigator.schemaFor(bot.currentWindow)?.id, 'fixture')
})
