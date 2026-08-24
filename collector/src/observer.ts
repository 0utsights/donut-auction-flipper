import { randomUUID } from 'node:crypto'
import { mkdirSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import mineflayer, { type Bot } from 'mineflayer'
import { BackendClient } from './backend.js'
import { fingerprintWindow, parseOrder, projectItem } from './parser.js'
import { minecraftConnect, proxyAgent, verifyEgress } from './proxy.js'
import { SafeNavigator, type WindowView } from './safe-navigation.js'
import { loadSchemas } from './schemas.js'
import { PARSER_VERSION, SCHEMA_VERSION, type ItemView, type MenuSchema, type ObserverTask, type RuntimeConfig, type ScanBatch } from './types.js'

process.umask(0o077)

class ObserverRuntime {
  private bot?: Bot
  private readonly backend: BackendClient
  private readonly schemas: MenuSchema[]
  private stopping = false
  private reconnects = 0
  private heartbeat?: NodeJS.Timeout
  private activeTask: ObserverTask | undefined
  private activePage = 0

  constructor(private readonly config: RuntimeConfig) {
    this.backend = new BackendClient(new URL(config.backendUrl), config.observerToken, config.account.id)
    this.schemas = loadSchemas(config.menuSchemasFile)
  }

  async run(): Promise<void> {
    await verifyEgress(this.config.account.proxyUrl, this.config.expectedEgressCheckUrl, this.config.account.expectedEgressIp)
    await this.connect()
    await this.backend.register(this.config.account.proxyLabel)
    this.heartbeat = setInterval(() => { const task = this.activeTask; void this.backend.heartbeat(this.bot?.player ? (task ? 'scanning' : 'online') : 'connecting', task?.id ?? '', task?.lease_token ?? '', this.activePage, this.bot?.player?.ping ?? 0, this.reconnects).catch(this.report) }, 5_000)
    while (!this.stopping) {
      const controller = new AbortController()
      const task = await this.backend.nextTask(controller.signal)
      if (!task) continue
      this.activeTask = task
      try {
        await this.execute(task)
        await this.backend.finishTask(task.id, task.lease_token, 'complete')
      } catch (error) {
        const message = safeMessage(error)
        await this.backend.finishTask(task.id, task.lease_token, this.bot?.player ? 'retry' : 'failed', message).catch(this.report)
        if (!this.bot?.player) await this.reconnect()
      } finally {
        this.activeTask = undefined
        this.activePage = 0
      }
    }
  }

  stop(): void {
    this.stopping = true
    if (this.heartbeat) clearInterval(this.heartbeat)
    this.bot?.quit('collector shutdown')
  }

  private async connect(): Promise<void> {
    mkdirSync(resolve(this.config.account.profilesFolder), { recursive: true, mode: 0o700 })
    const connect = minecraftConnect(this.config.account.proxyUrl, this.config.minecraftHost, this.config.minecraftPort)
    const bot = mineflayer.createBot({
      username: this.config.account.microsoftUsername, auth: 'microsoft', version: this.config.version,
      profilesFolder: resolve(this.config.account.profilesFolder), fakeHost: this.config.minecraftHost,
      connect: connect as never, agent: proxyAgent(this.config.account.proxyUrl) as never,
      checkTimeoutInterval: 30_000, hideErrors: true, logErrors: false
    })
    this.bot = bot
    bot.on('kicked', reason => this.report(new Error(`kicked: ${safeText(reason, 200)}`)))
    bot.on('error', this.report)
    await waitForEvent(bot, 'spawn', 30_000)
  }

  private async reconnect(): Promise<void> {
    this.reconnects++
    const delay = Math.min(60_000, 1_000 * 2 ** Math.min(6, this.reconnects))
    await sleep(delay)
    if (!this.stopping) await this.connect()
  }

  private async execute(task: ObserverTask): Promise<void> {
    const bot = this.bot
    if (!bot?.player) throw new Error('observer is not connected')
    if (task.parser_schema !== SCHEMA_VERSION) throw new Error('backend requested an unsupported parser schema')
    const navigator = new SafeNavigator(botAdapter(bot), this.schemas)
    navigator.sendCommand('/orders')
    let window = await waitForWindow(bot, 10_000)
    let sessionId = randomUUID()
    const seen = new Set<string>()
    const limit = 200
    const taskDeadline = Date.parse(task.lease_expires_at) - 2_000
    for (let pageIndex = 0; pageIndex < limit; pageIndex++) {
      const captured = this.capture(window as unknown as WindowView)
      if (task.kind !== 'focused_watch' && seen.has(captured.hash)) break
      seen.add(captured.hash)
      const schema = navigator.schemaFor(captured.window)
      const parsed = captured.views.map(parseOrder).filter(value => value !== undefined)
      const listingViews = schema ? captured.views.filter(view => schema.listingSlots.has(view.slot)) : captured.views.filter(view => view.slot < Math.max(0, window.slots.length - 36))
      const complete = schema !== undefined && listingViews.every(view => parseOrder(view) !== undefined)
      const scan: ScanBatch = {
        schema_version: SCHEMA_VERSION, observer_id: this.config.account.id, task_id: task.id, lease_token: task.lease_token, session_id: sessionId,
        content_hash: captured.hash, screen_title: captured.title, page: parsePage(captured.title, pageIndex + 1), complete,
        observed_at: new Date().toISOString(), orders: parsed,
        ...(!schema ? { unknown_schema: true, schema_reason: 'no verified menu schema; capture only and no clicks performed' } : {}),
        ...(schema && !complete ? { unknown_schema: true, schema_reason: 'verified layout contained an unparseable listing slot; navigation stopped' } : {})
      }
      this.activePage = scan.page
      await this.backend.submitScan(scan)
      await this.backend.heartbeat(schema && complete ? 'scanning' : 'schema_hold', task.id, task.lease_token, scan.page, bot.player?.ping ?? 0, this.reconnects)
      if (!schema || !complete) {
        if (!schema || !complete) this.writeCapture(task.id, captured.title, captured.views)
        break
      }
      if (task.kind === 'focused_watch') {
        if (!parsed.some(order => order.signature === task.signature)) throw new Error('focused item is not visible; unsafe search was not attempted')
        if (![...schema.controls.values()].some(rule => rule.kind === 'refresh') || Date.now() >= taskDeadline) break
        await navigator.clickControl('refresh')
        await sleep(Math.max(100, Math.min(5_000, task.desired_freshness_ms)))
        if (!bot.currentWindow) throw new Error('order window closed during focused watch')
        window = bot.currentWindow
        sessionId = randomUUID()
        continue
      }
      if (![...schema.controls.values()].some(rule => rule.kind === 'next_page')) break
      await navigator.clickControl('next_page')
      window = await waitForChangedWindow(bot, captured.hash, 3_000)
    }
  }

  private capture(window: WindowView): { window: WindowView; title: string; views: ItemView[]; hash: string } {
    const title = safeText(window.title, 128)
    const views: ItemView[] = []
    window.slots.forEach((item, slot) => { const view = projectItem(item, slot); if (view) views.push(view) })
    return { window, title, views, hash: fingerprintWindow(title, views) }
  }

  private writeCapture(taskId: string, title: string, views: ItemView[]): void {
    const directory = resolve('captures', this.config.account.id)
    mkdirSync(directory, { recursive: true, mode: 0o700 })
    const sanitized = { parserVersion: PARSER_VERSION, taskId, title, capturedAt: new Date().toISOString(), slots: views.map(view => ({ slot: view.slot, itemId: view.itemId, count: view.count, displayName: view.displayName, text: view.text })) }
    writeFileSync(resolve(directory, `${Date.now()}-${taskId}.json`), JSON.stringify(sanitized, null, 2), { mode: 0o600 })
  }

  private readonly report = (error: unknown): void => { process.stderr.write(`[${this.config.account.id}] ${safeMessage(error)}\n`) }
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

async function waitForChangedWindow(bot: Bot, previousHash: string, timeout: number): Promise<NonNullable<Bot['currentWindow']>> {
  const started = Date.now()
  while (Date.now() - started < timeout) {
    await sleep(50)
    if (!bot.currentWindow) continue
    const title = safeText(String(bot.currentWindow.title), 128)
    const views: ItemView[] = []
    bot.currentWindow.slots.forEach((item, slot) => { const view = projectItem(item, slot); if (view) views.push(view) })
    if (fingerprintWindow(title, views) !== previousHash) return bot.currentWindow
  }
  throw new Error('order page did not change after allowlisted navigation')
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

function parsePage(title: string, fallback: number): number { const match = /(?:page\s*)?(\d+)\s*(?:\/|of)\s*\d+/i.exec(title); return match ? Number(match[1]) : fallback }
function safeText(value: unknown, limit: number): string { const text = String(value ?? '').replace(/[\r\n\0]/g, ' ').trim(); return text.slice(0, limit) }
function safeMessage(error: unknown): string { return safeText(error instanceof Error ? error.message : error, 200) }
function sleep(milliseconds: number): Promise<void> { return new Promise(resolvePromise => setTimeout(resolvePromise, milliseconds)) }

let runtime: ObserverRuntime | undefined
process.once('message', message => {
  runtime = new ObserverRuntime(message as RuntimeConfig)
  void runtime.run().then(() => process.exit(0)).catch(error => { process.stderr.write(`${safeMessage(error)}\n`); process.exit(1) })
})
process.once('SIGTERM', () => runtime?.stop())
process.once('SIGINT', () => runtime?.stop())
