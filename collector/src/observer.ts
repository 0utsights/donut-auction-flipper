import { randomUUID } from 'node:crypto'
import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import mineflayer, { type Bot } from 'mineflayer'
import { BackendClient } from './backend.js'
import { installAuthenticationProxy } from './auth-proxy.js'
import { fingerprintWindow, isFilteredMostPerItemOrder, isMostPerItemOrder, parseOrder, plainText, projectItem } from './parser.js'
import { EgressMismatchError, minecraftConnect, proxyAgent, verifyEgress } from './proxy.js'
import { redactSensitiveText } from './redaction.js'
import { SafeNavigator, type WindowView } from './safe-navigation.js'
import { loadSchemas } from './schemas.js'
import { backendRetryDelay, taskResultForFailure, type TaskFailureClass } from './task-policy.js'
import { PARSER_VERSION, SCHEMA_VERSION, type ItemView, type MenuSchema, type ObserverTask, type RuntimeConfig, type ScanBatch } from './types.js'
import { beginServerWindowUpdate, WindowClosedError, type WindowUpdateSource } from './window-update.js'

process.umask(0o077)

// Donut rejects machine-speed menu interactions with an "Invalid sequence" kick.
// Discovery is intentionally human-paced; focused watches use the faster bounded
// lane and expose whatever cadence the real menu actually sustains.
const DISCOVERY_CLICK_DELAY_MS = 750
const FOCUSED_CLICK_DELAY_MS = 500
export const MOST_PER_ITEM_RECHECK_MS = 60_000
// Auction history carries the slow safety evidence. Focused order observation
// only confirms the current leading reward and short-term consistency.
const FOCUSED_WATCH_RUNTIME_MS = 3_000
const AUTOMATIC_FOCUSED_RUNTIME_MS = 8_000
const PROFILE_REVALIDATION_RUNTIME_MS = 4_000
// Device-code authorization requires a human browser round trip and regularly
// takes longer than the normal network-login budget. Keep one code stable long
// enough to complete it instead of killing the process and rotating the code.
// Cached-token logins still resolve this wait as soon as the server spawns.
const MICROSOFT_LOGIN_TIMEOUT_MS = 10 * 60_000

class ObserverRuntime {
  private bot: Bot | undefined
  private readonly backend: BackendClient
  private readonly schemas: MenuSchema[]
  private stopping = false
  private reconnects = 0
  private reconnectStreak = 0
  private connectedAt = 0
  private heartbeat?: NodeJS.Timeout
  private activeTask: ObserverTask | undefined
  private activePage = 0
  private connected = false
  private mostPerItemConfirmedAt = 0

  constructor(private readonly config: RuntimeConfig) {
    this.backend = new BackendClient(new URL(config.backendUrl), config.observerToken, config.account.id)
    this.schemas = loadSchemas(config.menuSchemasFile)
  }

  async run(): Promise<void> {
    await verifyEgress(this.config.account.proxyUrl, this.config.expectedEgressCheckUrl, this.config.account.expectedEgressIp)
    this.log('proxy_egress_verified', `proxy=${this.config.account.proxyLabel}`)
    this.log('minecraft_connecting', `version=${this.config.version}`)
    await this.connect()
    this.log('minecraft_ready')
    await this.backend.register(this.config.account.proxyLabel)
    this.log('backend_registered')
    this.heartbeat = setInterval(() => { const task = this.activeTask; void this.backend.heartbeat(this.bot?.player ? (task ? 'scanning' : 'online') : 'connecting', task?.id ?? '', task?.lease_token ?? '', this.activePage, this.bot?.player?.ping ?? 0, this.reconnects).catch(this.report) }, 5_000)
    let backendFailures = 0
    while (!this.stopping) {
      const controller = new AbortController()
      let task: ObserverTask | undefined
      try {
        task = await this.backend.nextTask(controller.signal)
        if (backendFailures > 0) this.log('backend_restored', `attempts=${backendFailures}`)
        backendFailures = 0
      } catch (error) {
        backendFailures++
        const delay = backendRetryDelay(backendFailures)
        this.log('backend_unavailable', `retry_ms=${delay} reason=${safeMessage(error)}`)
        await sleep(delay)
        // Backend availability has no bearing on the read-only Minecraft
        // transport. Keep the authenticated game session alive and re-register
        // the observer after the control plane returns.
        try {
          await this.backend.register(this.config.account.proxyLabel)
        } catch (registerError) {
          this.log('backend_register_retry_failed', `reason=${safeMessage(registerError)}`)
        }
        continue
      }
      if (!task) continue
      if (!this.connected) await this.reconnect()
      this.activeTask = task
      this.log('task_leased', `kind=${task.kind} priority=${task.priority} target=${task.signature || '-'}`)
      await this.backend.heartbeat('scanning', task.id, task.lease_token, 0, this.bot?.player?.ping ?? 0, this.reconnects)
      let status: 'complete' | 'retry' | 'failed' = 'complete'
      let message = ''
      let reconnect = false
      try {
        message = await this.execute(task) ?? ''
      } catch (error) {
        message = safeMessage(error)
        const failure: TaskFailureClass = error instanceof SchemaHoldError ? 'schema_hold'
          : error instanceof ReconnectRequiredError ? 'reconnect_required'
            : error instanceof MenuSessionEndedError ? 'menu_session_ended' : 'other'
        status = taskResultForFailure(task.kind, task.priority, failure)
        this.log(status === 'complete' ? 'task_complete' : 'task_failed', `reason=${message}`)
        // A closed or non-opening /orders menu can leave the player connection
        // alive while Donut silently ignores subsequent market commands. Rotate
        // only the Minecraft session; prismarine-auth reuses the cached Microsoft
        // authorization, so recovery does not require another device-code login.
        const rotate = error instanceof ReconnectRequiredError || error instanceof MenuSessionEndedError || !this.connected
        if (error instanceof MenuSessionEndedError && this.connected) this.log('orders_menu_reopen_scheduled')
        if (rotate && this.connected) {
          this.connected = false
          this.bot?.quit('collector connection rotation')
        }
        reconnect = rotate || !this.connected
      } finally {
        this.activeTask = undefined
        this.activePage = 0
      }
      await this.backend.finishTask(task.id, task.lease_token, status, message).catch(this.report)
      if (reconnect) await this.reconnect(status === 'complete')
    }
  }

  stop(): void {
    this.stopping = true
    this.connected = false
    if (this.heartbeat) clearInterval(this.heartbeat)
    this.bot?.quit('collector shutdown')
  }

  private async connect(): Promise<void> {
    mkdirSync(resolve(this.config.account.profilesFolder), { recursive: true, mode: 0o700 })
    const connect = minecraftConnect(this.config.account.proxyUrl, this.config.minecraftHost, this.config.minecraftPort)
    const restoreAuthenticationFetch = installAuthenticationProxy(this.config.account.proxyUrl)
    let bot: Bot
    try {
      bot = mineflayer.createBot({
        username: this.config.account.microsoftUsername, auth: 'microsoft', version: this.config.version,
        host: this.config.minecraftHost, port: this.config.minecraftPort, profilesFolder: resolve(this.config.account.profilesFolder), fakeHost: this.config.minecraftHost,
        connect: connect as never, agent: proxyAgent(this.config.account.proxyUrl) as never,
        checkTimeoutInterval: 30_000, hideErrors: true, logErrors: false
      })
    } catch (error) {
      restoreAuthenticationFetch()
      throw error
    }
    // Market observers never move. Disable simulation immediately; the pinned
    // Mineflayer build still emits idle movement ticks until its mount state is
    // entered, so that state is applied after server transfers settle below.
    bot.physicsEnabled = false
    this.bot = bot
    bot.on('kicked', reason => {
      if (this.bot === bot) this.connected = false
      this.report(new Error(`kicked: ${safeText(reason, 200)}`))
    })
    bot._client.once('session', () => { restoreAuthenticationFetch(); this.log('microsoft_session_ready') })
    bot.once('login', () => this.log('minecraft_login_accepted'))
    bot.once('spawn', () => this.log('minecraft_spawn_received'))
    bot.on('error', error => this.report(new Error(`minecraft transport: ${safeMessage(error)}`)))
    bot._client.once('disconnect', packet => this.report(new Error(`minecraft disconnect: ${safeText(packet, 300)}`)))
    bot.once('end', reason => {
      restoreAuthenticationFetch()
      if (this.bot === bot) {
        this.connected = false
        this.bot = undefined
      }
      this.report(new Error(`connection ended: ${safeText(reason, 200)}`))
    })
    await waitForEvent(bot, 'spawn', MICROSOFT_LOGIN_TIMEOUT_MS)
    await waitForStableSpawn(bot, 5_000, 20_000)
    // In Mineflayer 4.37.1 this public event is the supported internal path that
    // suspends idle movement updates. No observer code subscribes to mount and
    // the collector never performs entity interaction or movement.
    bot.emit('mount')
    this.connected = true
    this.connectedAt = Date.now()
    this.mostPerItemConfirmedAt = 0
  }

  private async reconnect(plannedRotation = false): Promise<void> {
    this.reconnects++
    // Planned end-of-scan rotations are success, even when a small live order
    // book completes in under a minute. Only unexpected short-lived failures
    // accumulate exponential backoff. Total reconnects remains diagnostic.
    if (plannedRotation || Date.now() - this.connectedAt >= 60_000) this.reconnectStreak = 0
    this.reconnectStreak++
    const delay = Math.min(60_000, 1_000 * 2 ** Math.min(6, this.reconnectStreak - 1))
    await sleep(delay)
    if (!this.stopping) await this.connect()
  }

  private async execute(task: ObserverTask): Promise<string | undefined> {
    const bot = this.bot
    if (!this.connected || !bot?.player) throw new Error('observer is not connected')
    if (task.parser_schema !== SCHEMA_VERSION) throw new Error('backend requested an unsupported parser schema')
    const navigator = new SafeNavigator(botAdapter(bot), this.schemas)
    const clickDelay = task.kind === 'focused_watch' ? FOCUSED_CLICK_DELAY_MS : DISCOVERY_CLICK_DELAY_MS
    let window: NonNullable<Bot['currentWindow']>
    if (task.kind === 'focused_watch' && task.signature) {
      // A previous focused task can leave an item-filtered page open. It uses
      // the same title, schema, and refresh control as the global order book,
      // so reusing it here falsely treats a small filtered result as the page
      // that proves the global Most Per Item mode. Always reopen `/orders`
      // before verifying the sort, then issue the canonical item search.
      if (bot.currentWindow) {
        bot.closeWindow(bot.currentWindow)
        await sleep(FOCUSED_CLICK_DELAY_MS)
        this.ensureConnected(bot)
      }
      if (shouldReverifyMostPerItem(this.mostPerItemConfirmedAt, Date.now(), MOST_PER_ITEM_RECHECK_MS)) {
        this.log('orders_command_sending')
        navigator.sendCommand('/orders')
        let globalWindow: NonNullable<Bot['currentWindow']>
        try {
          globalWindow = await waitForWindow(bot, 10_000)
        } catch (error) {
          throw new MenuSessionEndedError(`orders menu did not open: ${safeMessage(error)}`)
        }
        globalWindow = await this.ensureMostPerItem(bot, navigator, globalWindow, clickDelay, task.id)
        bot.closeWindow(globalWindow)
        await sleep(FOCUSED_CLICK_DELAY_MS)
        this.ensureConnected(bot)
      } else {
        this.log('most_per_item_session_proof_reused', `age_ms=${Date.now() - this.mostPerItemConfirmedAt}`)
      }
      this.log('orders_item_search_sending', `signature=${task.signature}`)
      navigator.searchOrders(task.signature)
      try {
        window = await waitForWindow(bot, 10_000)
      } catch (error) {
        throw new MenuSessionEndedError(`filtered orders menu did not open: ${safeMessage(error)}`)
      }
    } else if (bot.currentWindow && navigator.schemaFor(bot.currentWindow as unknown as WindowView) && navigator.controlAvailable('refresh')) {
      const current = this.capture(bot.currentWindow as unknown as WindowView)
      this.log('orders_refresh_clicking')
      await sleep(clickDelay)
      this.ensureConnected(bot)
      await clickControlAndWaitForServer(bot, navigator, 'refresh')
      this.ensureConnected(bot)
      window = bot.currentWindow ?? await waitForWindow(bot, 3_000)
    } else {
      this.log('orders_command_sending')
      navigator.sendCommand('/orders')
      try {
        window = await waitForWindow(bot, 10_000)
      } catch (error) {
        throw new MenuSessionEndedError(`orders menu did not open: ${safeMessage(error)}`)
      }
    }
    this.log('orders_window_opened')
    if (task.kind !== 'focused_watch') window = await this.ensureMostPerItem(bot, navigator, window, clickDelay, task.id)
    let sessionId = randomUUID()
    const taskDeadline = task.kind === 'focused_watch'
      ? Date.now() + (task.priority >= 100 ? FOCUSED_WATCH_RUNTIME_MS
        : task.priority >= 75 ? PROFILE_REVALIDATION_RUNTIME_MS : AUTOMATIC_FOCUSED_RUNTIME_MS)
      : Date.parse(task.lease_expires_at) - 2_000
    for (let pageIndex = 0; ; pageIndex++) {
      this.ensureConnected(bot)
      const captured = this.capture(window as unknown as WindowView)
      const schema = navigator.schemaFor(captured.window)
      const listingViews = schema ? captured.views.filter(view => schema.listingSlots.has(view.slot)) : captured.views.filter(view => view.slot < Math.max(0, window.slots.length - 36))
      const parsed = listingViews.map(parseOrder).filter(value => value !== undefined)
      const complete = schema !== undefined && listingViews.every(view => parseOrder(view) !== undefined)
      const observedPage = parsePage(captured.title, pageIndex + 1)
      const submittedOrders = task.kind === 'focused_watch' && task.signature !== undefined
        ? parsed.filter(order => order.signature === task.signature) : parsed
      const filteredSortInvalid = task.kind === 'focused_watch' && submittedOrders.length > 0 && !isFilteredMostPerItemOrder(submittedOrders, submittedOrders.length)
      const filteredPageInvalid = task.kind === 'focused_watch' && observedPage !== 1
      const scan: ScanBatch = {
        schema_version: SCHEMA_VERSION, observer_id: this.config.account.id, task_id: task.id, lease_token: task.lease_token, session_id: sessionId,
        content_hash: captured.hash, screen_title: captured.title, page: observedPage, complete,
        observed_at: new Date().toISOString(), orders: submittedOrders,
        ...(!schema ? { unknown_schema: true, schema_reason: 'no verified menu schema; capture only and no clicks performed' } : {}),
        ...(schema && !complete ? { unknown_schema: true, schema_reason: 'verified layout contained an unparseable listing slot; navigation stopped' } : {}),
        ...(filteredSortInvalid ? { unknown_schema: true, schema_reason: 'item-filtered result did not prove a descending unit-reward order; observation rejected' } : {}),
        ...(filteredPageInvalid ? { unknown_schema: true, schema_reason: 'item-filtered result did not open on its highest-reward page; observation rejected' } : {})
      }
      this.activePage = scan.page
      await this.backend.submitScan(scan)
      this.ensureConnected(bot)
      this.log('page_submitted', `page=${scan.page} orders=${submittedOrders.length} complete=${scan.complete}`)
      if (task.kind === 'focused_watch' && parsed.length !== submittedOrders.length) {
        this.log('filtered_search_noise_ignored', `ignored=${parsed.length - submittedOrders.length}`)
      }
      const yieldToFocus = await this.backend.heartbeat(schema && complete ? 'scanning' : 'schema_hold', task.id, task.lease_token, scan.page, bot.player?.ping ?? 0, this.reconnects)
      if (yieldToFocus && task.kind === 'discovery') {
        this.log('discovery_yielding_to_focus', `page=${scan.page}`)
        return
      }
      if (!schema || !complete || filteredSortInvalid || filteredPageInvalid) {
        this.writeCapture(task.id, captured.hash, captured.title, captured.views)
        this.log('schema_hold', `content=${captured.hash.slice(0, 12)}`)
        if (task.kind === 'focused_watch' && bot.currentWindow) bot.closeWindow(bot.currentWindow)
        throw new SchemaHoldError(scan.schema_reason ?? 'order menu schema requires review')
      }
      if (task.kind === 'focused_watch') {
        if (submittedOrders.length > 0) {
          if (![...schema.controls.values()].some(rule => rule.kind === 'refresh')) throw new SchemaHoldError('focused-watch refresh control is unavailable')
          if (Date.now() >= taskDeadline) {
            if (bot.currentWindow) bot.closeWindow(bot.currentWindow)
            return
          }
          const refreshCycleStartedAt = Date.now()
          await sleep(clickDelay)
          this.ensureConnected(bot)
          await clickControlAndWaitForServer(bot, navigator, 'refresh')
          this.ensureConnected(bot)
          // desired_freshness_ms describes the complete refresh cycle. Account
          // for the safety delay and server acknowledgement already spent so a
          // one-second watch does not accidentally run at almost two seconds.
          const desiredCycle = Math.max(100, Math.min(5_000, task.desired_freshness_ms))
          await sleep(Math.max(0, desiredCycle - (Date.now() - refreshCycleStartedAt)))
          if (!bot.currentWindow) throw new MenuSessionEndedError('order window closed during focused watch')
          window = bot.currentWindow
          sessionId = randomUUID()
          continue
        }
        // The canonical item search returned no active orders. This is a valid
        // market observation, not a reason to paginate or retry for a minute.
        this.log('focused_item_not_found', `signature=${task.signature ?? ''}`)
        if (bot.currentWindow) bot.closeWindow(bot.currentWindow)
        return 'no_active_orders'
      }

      // Continuously refresh the highest unit-reward page. Each refresh is an
      // independent observation and can immediately yield to API-ranked work.
      if (scan.page !== 1) throw new ReconnectRequiredError(`top-page discovery opened unexpectedly at page ${scan.page}`)
      if (!navigator.controlAvailable('refresh')) throw new SchemaHoldError('top-page refresh control is unavailable')
      this.log('top_page_refresh_clicking')
      await sleep(clickDelay)
      this.ensureConnected(bot)
      await clickControlAndWaitForServer(bot, navigator, 'refresh')
      this.ensureConnected(bot)
      window = bot.currentWindow ?? await waitForWindow(bot, 3_000)
      sessionId = randomUUID()
    }
  }

  private async ensureMostPerItem(bot: Bot, navigator: SafeNavigator, initialWindow: NonNullable<Bot['currentWindow']>, clickDelay: number, taskId: string): Promise<NonNullable<Bot['currentWindow']>> {
    let window = initialWindow
    for (let attempt = 0; attempt <= 3; attempt++) {
      this.ensureConnected(bot)
      const captured = this.capture(window as unknown as WindowView)
      const schema = navigator.schemaFor(captured.window)
      if (!schema) {
        this.writeCapture(taskId, captured.hash, captured.title, captured.views)
        throw new SchemaHoldError('cannot verify Most Per Item on an unknown order screen')
      }
      const listingViews = captured.views.filter(view => schema.listingSlots.has(view.slot))
      const parsed = listingViews.map(parseOrder).filter(value => value !== undefined)
      if (isMostPerItemOrder(parsed, listingViews.length)) {
        this.mostPerItemConfirmedAt = Date.now()
        this.log('most_per_item_confirmed', `attempt=${attempt + 1} listings=${parsed.length}`)
        return window
      }
      if (attempt === 3 || !navigator.controlAvailable('filter')) {
        this.writeCapture(taskId, captured.hash, captured.title, captured.views)
        throw new SchemaHoldError('Most Per Item ordering could not be verified after cycling the hopper')
      }
      this.log('most_per_item_filter_clicking', `attempt=${attempt + 1}`)
      await sleep(clickDelay)
      this.ensureConnected(bot)
      await clickControlAndWaitForServer(bot, navigator, 'filter')
      this.ensureConnected(bot)
      window = bot.currentWindow ?? await waitForWindow(bot, 3_000)
    }
    throw new SchemaHoldError('Most Per Item ordering could not be verified')
  }

  private ensureConnected(bot: Bot): void {
    if (!this.connected || this.bot !== bot || !bot.player) throw new Error('observer disconnected during order scan')
  }

  private capture(window: WindowView): { window: WindowView; title: string; views: ItemView[]; hash: string } {
    const title = plainText(window.title).slice(0, 128)
    const views: ItemView[] = []
    window.slots.forEach((item, slot) => { const view = projectItem(item, slot); if (view) views.push(view) })
    return { window, title, views, hash: fingerprintWindow(title, views) }
  }

  private writeCapture(taskId: string, hash: string, title: string, views: ItemView[]): void {
    const directory = resolve('captures', this.config.account.id)
    mkdirSync(directory, { recursive: true, mode: 0o700 })
    const sanitized = { parserVersion: PARSER_VERSION, taskId, title, capturedAt: new Date().toISOString(), slots: views.map(view => ({ slot: view.slot, itemId: view.itemId, count: view.count, displayName: view.displayName, text: view.text })) }
    try {
      writeFileSync(resolve(directory, `${hash}.json`), JSON.stringify(sanitized, null, 2), { flag: 'wx', mode: 0o600 })
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error
    }
  }

  private readonly report = (error: unknown): void => { process.stderr.write(`[${this.config.account.id}] ${safeMessage(error)}\n`) }
  private readonly log = (event: string, detail = ''): void => { process.stdout.write(`[${this.config.account.id}] ${event}${detail ? ` ${detail}` : ''}\n`) }
}

export function shouldReverifyMostPerItem(lastConfirmedAt: number, now: number, ttl: number): boolean {
  if (!Number.isFinite(lastConfirmedAt) || !Number.isFinite(now) || !Number.isFinite(ttl) || ttl <= 0) return true
  return lastConfirmedAt <= 0 || now < lastConfirmedAt || now - lastConfirmedAt >= ttl
}

function botAdapter(bot: Bot): { chat(command: string): void; clickWindow(slot: number, mouseButton: number, mode: number): Promise<void>; currentWindow: WindowView | null } {
  return {
    chat: command => bot.chat(command), clickWindow: (slot, button, mode) => bot.clickWindow(slot, button, mode),
    get currentWindow() { return bot.currentWindow as unknown as WindowView | null }
  }
}

async function waitForWindow(bot: Bot, timeout: number): Promise<NonNullable<Bot['currentWindow']>> {
  if (bot.currentWindow) return bot.currentWindow
  return await waitForEvent(bot, 'windowOpen', timeout) as NonNullable<Bot['currentWindow']>
}

async function clickControlAndWaitForServer(bot: Bot, navigator: SafeNavigator, kind: 'next_page' | 'refresh' | 'filter'): Promise<void> {
  const windowId = bot.currentWindow?.id
  if (windowId === undefined) throw new MenuSessionEndedError('order window closed before navigation')
  const update = beginServerWindowUpdate(bot as unknown as WindowUpdateSource, windowId, 5_000)
  try {
    await navigator.clickControl(kind)
    try {
      await update.promise
    } catch (error) {
      if (error instanceof WindowClosedError) throw new MenuSessionEndedError(error.message)
      throw error
    }
  } finally {
    update.cancel()
  }
}

function waitForEvent(target: Bot, event: 'spawn' | 'windowOpen', timeout: number): Promise<unknown> {
  return new Promise((resolvePromise, reject) => {
    const timer = setTimeout(() => { cleanup(); reject(new Error(`${event} timed out`)) }, timeout)
    const success = (value: unknown): void => { cleanup(); resolvePromise(value) }
    const ended = (reason: string): void => { cleanup(); reject(new Error(`connection ended: ${safeText(reason, 200)}`)) }
    const cleanup = (): void => { clearTimeout(timer); target.removeListener(event, success); target.removeListener('end', ended) }
    target.once(event, success); target.once('end', ended)
  })
}

function waitForStableSpawn(bot: Bot, quietMilliseconds: number, timeoutMilliseconds: number): Promise<void> {
  return new Promise((resolvePromise, reject) => {
    let quietTimer: NodeJS.Timeout
    const deadline = setTimeout(() => { cleanup(); reject(new Error('server transfer did not stabilize')) }, timeoutMilliseconds)
    const stable = (): void => { cleanup(); resolvePromise() }
    const reset = (): void => { clearTimeout(quietTimer); quietTimer = setTimeout(stable, quietMilliseconds) }
    const ended = (reason: string): void => { cleanup(); reject(new Error(`connection ended during server transfer: ${safeText(reason, 200)}`)) }
    const cleanup = (): void => { clearTimeout(deadline); clearTimeout(quietTimer); bot.removeListener('spawn', reset); bot.removeListener('end', ended) }
    bot.on('spawn', reset)
    bot.once('end', ended)
    reset()
  })
}

function parsePage(title: string, fallback: number): number {
  const match = /\bpage\s+(\d+)(?:\s*(?:\/|of)\s*\d+)?/i.exec(title) ?? /(\d+)\s*(?:\/|of)\s*\d+/i.exec(title)
  return match ? Number(match[1]) : fallback
}
class SchemaHoldError extends Error {}
class ReconnectRequiredError extends Error {}
class MenuSessionEndedError extends Error {}

function safeText(value: unknown, limit: number): string {
  return redactSensitiveText(value).replace(/[\r\n\0]/g, ' ').trim().slice(0, limit)
}
function safeMessage(error: unknown): string { return safeText(error instanceof Error ? error.message : error, 200) }
function sleep(milliseconds: number): Promise<void> { return new Promise(resolvePromise => setTimeout(resolvePromise, milliseconds)) }

let runtime: ObserverRuntime | undefined
process.once('message', message => {
  runtime = new ObserverRuntime(message as RuntimeConfig)
  void runtime.run().then(() => process.exit(0)).catch(error => {
    process.stderr.write(`${safeMessage(error)}\n`)
    // Configuration-level egress mismatches must disable this account. Other
    // network failures remain restartable and use the manager's backoff.
    process.exit(error instanceof EgressMismatchError ? 78 : 1)
  })
})
process.once('SIGTERM', () => runtime?.stop())
process.once('SIGINT', () => runtime?.stop())
