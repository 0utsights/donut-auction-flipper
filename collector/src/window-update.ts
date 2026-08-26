export interface EventSource {
  on(event: string, listener: (...args: any[]) => void): unknown
  once(event: string, listener: (...args: any[]) => void): unknown
  removeListener(event: string, listener: (...args: any[]) => void): unknown
}

export interface WindowUpdateSource extends EventSource {
  _client: EventSource
}

export class WindowClosedError extends Error {}

// Mineflayer applies custom-menu packets before these listeners run. Donut can
// emit several packets for one page change, so the first packet is only an
// acknowledgement; the window is safe to inspect after the relevant stream has
// been quiet for a short bounded period.
export function beginServerWindowUpdate(
  source: WindowUpdateSource,
  windowID: number,
  timeoutMilliseconds: number,
  quietMilliseconds = 250
): { promise: Promise<void>; cancel(): void } {
  let settled = false
  let acknowledged = false
  let quietTimer: ReturnType<typeof setTimeout> | undefined
  let resolveUpdate!: () => void
  let rejectUpdate!: (error: Error) => void

  const cleanup = (): void => {
    clearTimeout(timeoutTimer)
    if (quietTimer) clearTimeout(quietTimer)
    source._client.removeListener('window_items', windowItems)
    source._client.removeListener('set_slot', setSlot)
    source._client.removeListener('open_window', openWindow)
    source._client.removeListener('close_window', closeWindow)
    source.removeListener('end', ended)
  }
  const succeed = (): void => {
    if (settled) return
    settled = true
    cleanup()
    resolveUpdate()
  }
  const fail = (error: Error): void => {
    if (settled) return
    settled = true
    cleanup()
    rejectUpdate(error)
  }
  const acknowledge = (): void => {
    if (settled) return
    acknowledged = true
    if (quietTimer) clearTimeout(quietTimer)
    quietTimer = setTimeout(succeed, quietMilliseconds)
  }
  const matches = (packet: { windowId?: number }): boolean => packet.windowId === windowID
  const windowItems = (packet: { windowId?: number }): void => { if (matches(packet)) acknowledge() }
  const setSlot = (packet: { windowId?: number }): void => { if (matches(packet)) acknowledge() }
  const openWindow = (): void => acknowledge()
  const closeWindow = (packet: { windowId?: number }): void => {
    if (matches(packet)) fail(new WindowClosedError('order window closed during navigation'))
  }
  const ended = (reason: string): void => fail(new Error(`connection ended during menu navigation: ${String(reason).slice(0, 200)}`))
  const promise = new Promise<void>((resolvePromise, rejectPromise) => {
    resolveUpdate = resolvePromise
    rejectUpdate = rejectPromise
  })
  const timeoutTimer = setTimeout(() => {
    fail(new Error(acknowledged ? 'order-menu update did not settle' : 'server did not acknowledge order-menu navigation'))
  }, timeoutMilliseconds)

  source._client.on('window_items', windowItems)
  source._client.on('set_slot', setSlot)
  source._client.on('open_window', openWindow)
  source._client.on('close_window', closeWindow)
  source.once('end', ended)

  return {
    promise,
    cancel: () => {
      if (settled) return
      settled = true
      cleanup()
      resolveUpdate()
    }
  }
}
