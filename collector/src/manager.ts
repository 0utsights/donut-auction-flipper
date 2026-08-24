import { fork, type ChildProcess } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { loadConfig } from './config.js'
import type { AccountConfig, CollectorConfig, RuntimeConfig } from './types.js'

process.umask(0o077)

const config = loadConfig()
const children = new Map<string, ChildProcess>()
const failures = new Map<string, number>()
let stopping = false

for (const account of config.accounts) launch(account)

function launch(account: AccountConfig): void {
  if (stopping) return
  const startedAt = Date.now()
  const child = fork(fileURLToPath(new URL('./observer.js', import.meta.url)), [], { stdio: ['ignore', 'inherit', 'inherit', 'ipc'] })
  children.set(account.id, child)
  const runtime: RuntimeConfig = {
    backendUrl: config.backendUrl, observerToken: config.observerToken, minecraftHost: config.minecraftHost,
    minecraftPort: config.minecraftPort, version: config.version, expectedEgressCheckUrl: config.expectedEgressCheckUrl,
    ...(config.menuSchemasFile ? { menuSchemasFile: config.menuSchemasFile } : {}), account
  }
  child.send(runtime)
  child.once('exit', (code, signal) => {
    children.delete(account.id)
    if (stopping) return
    const count = Date.now() - startedAt >= 5 * 60_000 ? 1 : (failures.get(account.id) ?? 0) + 1
    failures.set(account.id, count)
    const delay = Math.min(60_000, 1_000 * 2 ** Math.min(6, count))
    process.stderr.write(`[manager] ${account.id} exited code=${code ?? 'none'} signal=${signal ?? 'none'}; restart in ${delay}ms\n`)
    setTimeout(() => launch(account), delay)
  })
  child.once('spawn', () => process.stdout.write(`[manager] started ${account.id} via ${account.proxyLabel}\n`))
}

function stop(): void {
  if (stopping) return
  stopping = true
  for (const child of children.values()) child.kill('SIGTERM')
  setTimeout(() => process.exit(0), 5_000).unref()
}

process.once('SIGTERM', stop)
process.once('SIGINT', stop)

export function runtimeConfig(global: CollectorConfig, account: AccountConfig): RuntimeConfig {
  return {
    backendUrl: global.backendUrl, observerToken: global.observerToken, minecraftHost: global.minecraftHost,
    minecraftPort: global.minecraftPort, version: global.version, expectedEgressCheckUrl: global.expectedEgressCheckUrl,
    ...(global.menuSchemasFile ? { menuSchemasFile: global.menuSchemasFile } : {}), account
  }
}
