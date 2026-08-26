import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import test from 'node:test'
import { beginServerWindowUpdate, WindowClosedError, type WindowUpdateSource } from './window-update.js'

function source(): { bot: WindowUpdateSource; client: EventEmitter; botEvents: EventEmitter } {
  const client = new EventEmitter()
  const botEvents = new EventEmitter()
  return {
    client,
    botEvents,
    bot: {
      _client: client,
      on: botEvents.on.bind(botEvents),
      once: botEvents.once.bind(botEvents),
      removeListener: botEvents.removeListener.bind(botEvents)
    }
  }
}

test('waits for the complete relevant packet burst to become quiet', async () => {
  const fixture = source()
  const update = beginServerWindowUpdate(fixture.bot, 7, 500, 35)
  let resolved = false
  void update.promise.then(() => { resolved = true })

  fixture.client.emit('window_items', { windowId: 7 })
  await new Promise(resolve => setTimeout(resolve, 20))
  fixture.client.emit('set_slot', { windowId: 7 })
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(resolved, false)
  await update.promise
  assert.equal(resolved, true)
})

test('tracks packets for a replacement window id', async () => {
  const fixture = source()
  const update = beginServerWindowUpdate(fixture.bot, 7, 500, 35)
  let resolved = false
  void update.promise.then(() => { resolved = true })

  fixture.client.emit('open_window', { windowId: 9 })
  await new Promise(resolve => setTimeout(resolve, 20))
  fixture.client.emit('window_items', { windowId: 9 })
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(resolved, false)
  await update.promise
  assert.equal(resolved, true)
})

test('ignores packets for unrelated windows', async () => {
  const fixture = source()
  const update = beginServerWindowUpdate(fixture.bot, 7, 80, 10)
  fixture.client.emit('window_items', { windowId: 8 })
  await assert.rejects(update.promise, /did not acknowledge/)
})

test('fails immediately when the active window closes', async () => {
  const fixture = source()
  const update = beginServerWindowUpdate(fixture.bot, 7, 500, 20)
  fixture.client.emit('close_window', { windowId: 7 })
  await assert.rejects(update.promise, WindowClosedError)
})

test('cancel removes listeners without leaving a rejected promise', async () => {
  const fixture = source()
  const update = beginServerWindowUpdate(fixture.bot, 7, 500, 20)
  update.cancel()
  await update.promise
  assert.equal(fixture.client.listenerCount('window_items'), 0)
  assert.equal(fixture.client.listenerCount('set_slot'), 0)
})
