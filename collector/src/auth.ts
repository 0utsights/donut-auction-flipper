import mineflayer from 'mineflayer'
import { resolve } from 'node:path'
import { mkdirSync } from 'node:fs'
import { loadConfig } from './config.js'
import { minecraftConnect, proxyAgent, verifyEgress } from './proxy.js'

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

function fail(error: unknown): void {
  if (finished) return
  finished = true

  const message = error instanceof Error ? error.message : String(error)
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
  profilesFolder: resolve(account.profilesFolder), fakeHost: config.minecraftHost,
  connect: minecraftConnect(account.proxyUrl, config.minecraftHost, config.minecraftPort) as never,
  agent: proxyAgent(account.proxyUrl) as never, hideErrors: true, logErrors: false
})
bot.once('login', () => {
  if (finished) return
  finished = true
  process.stdout.write(`authenticated ${account.id}; token cache saved\n`)
  bot?.quit('authentication complete')
})
bot.once('error', fail)
