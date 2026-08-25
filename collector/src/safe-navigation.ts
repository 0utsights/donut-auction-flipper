import type { ControlRule, MenuSchema, SafeControl } from './types.js'
import { plainText } from './parser.js'

export interface WindowView {
  title: unknown
  slots: ReadonlyArray<{ name?: string; displayName?: string; customName?: unknown } | null>
}

export interface NavigationBot {
  chat(command: string): void
  clickWindow(slot: number, mouseButton: number, mode: number): Promise<void>
  currentWindow?: WindowView | null
}

const allowedCommands = new Set(['/orders'])
const allowedControls = new Set<SafeControl>(['next_page', 'previous_page', 'refresh', 'filter', 'search'])
const forbiddenLabel = /\b(?:buy|purchase|fulfill|create|confirm|cancel|claim|collect|list|sell)\b/i

export class SafeNavigator {
  constructor(private readonly bot: NavigationBot, private readonly schemas: readonly MenuSchema[]) {}

  sendCommand(command: string): void {
    if (!allowedCommands.has(command)) throw new Error('collector command is not allowlisted')
    this.bot.chat(command)
  }

  schemaFor(window: WindowView | null | undefined): MenuSchema | undefined {
    if (!window) return undefined
    const title = plainText(window.title)
    return this.schemas.find(schema => schema.title.test(title))
  }

  controlAvailable(kind: SafeControl): boolean {
    return this.control(kind) !== undefined
  }

  async clickControl(kind: SafeControl): Promise<void> {
    if (!allowedControls.has(kind)) throw new Error('control kind is not allowlisted')
    if (!this.bot.currentWindow || !this.schemaFor(this.bot.currentWindow)) throw new Error('unknown order screen; navigation denied')
    const control = this.control(kind)
    if (!control) throw new Error('control fingerprint changed; navigation denied')
    const [slot] = control
    await this.bot.clickWindow(slot, 0, 0)
  }

  private control(kind: SafeControl): [number, ControlRule] | undefined {
    if (!allowedControls.has(kind)) return undefined
    const window = this.bot.currentWindow
    const schema = this.schemaFor(window)
    if (!window || !schema) return undefined
    const matches = [...schema.controls.entries()].filter(([, rule]) => rule.kind === kind)
    if (matches.length !== 1) return undefined
    const [slot, rule] = matches[0]!
    const item = window.slots[slot]
    const name = item?.name ?? ''
    const label = plainText(item?.customName) || plainText(item?.displayName)
    if (!item || name !== rule.itemName || !rule.label.test(label) || forbiddenLabel.test(label)) return undefined
    return [slot, rule]
  }
}
