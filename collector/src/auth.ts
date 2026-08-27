import mineflayer from 'mineflayer'
import { resolve } from 'node:path'
import { mkdirSync } from 'node:fs'
import { loadConfig } from './config.js'
import { minecraftConnect, proxyAgent, verifyEgress } from './proxy.js'
import { redactSensitiveText } from './redaction.js'

process.umask(0o077)

const config = loadConfig()
const index = process.argv.indexOf('--account')
const id = index >= 0 ? process.argv[index + 1] : undefined
const selectedAccount = config.accounts.find(candidate => candidate.id === id)
if (!selectedAccount) throw new Error('usage: npm run auth -- --account <observer-id>')
const account = selectedAccount

await verifyEgress(account.proxyUrl, config.expectedEgressCheckUrl, account.expectedEgressIp)
mkdirSync(resolve(account.profilesFolder), { recursive: true, mode: 0o700 })

let finished = false
let bot: ReturnType<typeof mineflayer.createBot> | undefined
let loginDeadline: NodeJS.Timeout | undefined
let disconnectReason = ''

function fail(error: unknown): void {
  if (finished) return
  finished = true
  if (loginDeadline) clearTimeout(loginDeadline)

  const message = redactSensitiveText(error instanceof Error ? error.message : error).replace(/[\r\n\0]/g, ' ').slice(0, 500)
  const explanation = message.includes('Failed to obtain profile data')
    ? 'Microsoft sign-in succeeded, but this account has no accessible Minecraft Java profile. Confirm it owns Java Edition and has a Java profile/name in the official launcher or minecraft.net, then try again.'
    : message

  process.stderr.write(`authentication failed for ${account.id}: ${explanation}\n`)
  try { bot?.quit('authentication failed') } catch { /* connection may not be open yet */ }
  process.exitCode = 1
}

process.once('uncaughtException', fail)
process.once('unhandledRejection', fail)

bot = mineflayer.createBot({
  username: account.microsoftUsername, auth: 'microsoft', version: config.version,
  host: config.minecraftHost, port: config.minecraftPort, profilesFolder: resolve(account.profilesFolder), fakeHost: config.minecraftHost,
  connect: minecraftConnect(account.proxyUrl, config.minecraftHost, config.minecraftPort) as never,
  agent: proxyAgent(account.proxyUrl) as never, hideErrors: true, logErrors: false
})
bot.once('login', () => {
  if (finished) return
  finished = true
  if (loginDeadline) clearTimeout(loginDeadline)
  process.stdout.write(`authenticated ${account.id}; token cache saved\n`)
  bot?.quit('authentication complete')
})
bot.once('error', fail)
bot._client.once('disconnect', packet => { disconnectReason = redactSensitiveText(packet).replace(/[\r\n\0]/g, ' ').slice(0, 300) })
bot.once('end', reason => {
  if (!finished) fail(new Error(`Minecraft connection ended before login${disconnectReason ? `: ${disconnectReason}` : (reason ? `: ${String(reason).slice(0, 300)}` : '')}`))
})
loginDeadline = setTimeout(() => fail(new Error('Minecraft login timed out after 30 seconds')), 30_000)
