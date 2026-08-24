import mineflayer from 'mineflayer'
import { resolve } from 'node:path'
import { mkdirSync } from 'node:fs'
import { loadConfig } from './config.js'
import { minecraftConnect, proxyAgent, verifyEgress } from './proxy.js'

process.umask(0o077)

const config = loadConfig()
const index = process.argv.indexOf('--account')
const id = index >= 0 ? process.argv[index + 1] : undefined
const account = config.accounts.find(candidate => candidate.id === id)
if (!account) throw new Error('usage: npm run auth -- --account <observer-id>')

await verifyEgress(account.proxyUrl, config.expectedEgressCheckUrl, account.expectedEgressIp)
mkdirSync(resolve(account.profilesFolder), { recursive: true, mode: 0o700 })
const bot = mineflayer.createBot({
  username: account.microsoftUsername, auth: 'microsoft', version: config.version,
  profilesFolder: resolve(account.profilesFolder), fakeHost: config.minecraftHost,
  connect: minecraftConnect(account.proxyUrl, config.minecraftHost, config.minecraftPort) as never,
  agent: proxyAgent(account.proxyUrl) as never, hideErrors: true, logErrors: false
})
bot.once('login', () => { process.stdout.write(`authenticated ${account.id}; token cache saved\n`); bot.quit('authentication complete') })
bot.once('error', error => { throw error })
