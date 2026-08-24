import { readFileSync, statSync } from 'node:fs'
import { resolve } from 'node:path'
import type { AccountConfig, CollectorConfig } from './types.js'

const idPattern = /^[A-Za-z0-9_-]{1,64}$/

export function loadConfig(path = process.env.DN_COLLECTOR_CONFIG ?? './accounts.json'): CollectorConfig {
  const absolute = resolve(path)
  assertPrivateFile(absolute)
  const value = JSON.parse(readFileSync(absolute, 'utf8')) as Partial<CollectorConfig>
  if (!isHttpUrl(value.backendUrl) || typeof value.observerToken !== 'string' || value.observerToken.length < 16 || value.observerToken.length > 512) throw new Error('invalid backendUrl or observerToken')
  if (typeof value.minecraftHost !== 'string' || value.minecraftHost.length < 1 || value.minecraftHost.length > 255 || !Number.isInteger(value.minecraftPort) || value.minecraftPort! < 1 || value.minecraftPort! > 65535) throw new Error('invalid Minecraft endpoint')
  if (value.version !== '1.21.11') throw new Error('collector is pinned to Minecraft 1.21.11')
  if (!isHttpsUrl(value.expectedEgressCheckUrl)) throw new Error('expectedEgressCheckUrl must use HTTPS')
  if (value.menuSchemasFile !== undefined && (typeof value.menuSchemasFile !== 'string' || value.menuSchemasFile.length === 0)) throw new Error('invalid menuSchemasFile')
  if (!Array.isArray(value.accounts) || value.accounts.length === 0) throw new Error('at least one observer account is required')
  const seen = new Set<string>()
  for (const account of value.accounts) validateAccount(account, seen)
  return value as CollectorConfig
}

function validateAccount(value: AccountConfig, seen: Set<string>): void {
  if (!idPattern.test(value.id) || seen.has(value.id)) throw new Error(`invalid or duplicate observer id: ${value.id}`)
  seen.add(value.id)
  if (typeof value.microsoftUsername !== 'string' || value.microsoftUsername.length > 254 || !value.microsoftUsername.includes('@')) throw new Error(`invalid Microsoft username for ${value.id}`)
  if (!idPattern.test(value.proxyLabel)) throw new Error(`invalid proxy label for ${value.id}`)
  const proxy = new URL(value.proxyUrl)
  if (!['socks5:', 'http:', 'https:'].includes(proxy.protocol) || !proxy.hostname || !proxy.port) throw new Error(`invalid proxy URL for ${value.id}`)
  if (typeof value.expectedEgressIp !== 'string' || !/^[0-9a-f:.]{3,64}$/i.test(value.expectedEgressIp)) throw new Error(`invalid expected egress IP for ${value.id}`)
  if (typeof value.profilesFolder !== 'string' || value.profilesFolder.length === 0) throw new Error(`profilesFolder is required for ${value.id}`)
}

function assertPrivateFile(path: string): void {
  if (process.platform === 'win32') return
  const mode = statSync(path).mode & 0o777
  const containerSecret = path.startsWith('/run/secrets/')
  if (!containerSecret && (mode & 0o077) !== 0) throw new Error(`${path} must not be readable or writable by group/other (chmod 600)`)
  if (containerSecret && (mode & 0o022) !== 0) throw new Error(`${path} must not be writable by group/other`)
}

function isHttpUrl(raw: unknown): boolean {
  try { return typeof raw === 'string' && ['http:', 'https:'].includes(new URL(raw).protocol) } catch { return false }
}
function isHttpsUrl(raw: unknown): boolean {
  try { return typeof raw === 'string' && new URL(raw).protocol === 'https:' } catch { return false }
}
