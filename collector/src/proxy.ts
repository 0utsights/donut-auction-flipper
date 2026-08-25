import http from 'node:http'
import https from 'node:https'
import net from 'node:net'
import { resolveSrv } from 'node:dns/promises'
import type { Agent } from 'node:http'
import { ProxyAgent } from 'proxy-agent'
import { SocksClient } from 'socks'

interface SocketClient {
  setSocket(socket: NodeJS.ReadWriteStream): void
  emit(event: 'connect'): boolean
  emit(event: 'error', error: Error): boolean
}

export class EgressMismatchError extends Error {}

export function proxyAgent(proxyUrl: string): Agent {
  return new ProxyAgent({ getProxyForUrl: () => proxyUrl }) as Agent
}

export function minecraftConnect(proxyUrl: string, destinationHost: string, destinationPort: number): (client: SocketClient) => void {
  const proxy = new URL(proxyUrl)
  const destination = resolveMinecraftDestination(destinationHost, destinationPort)
  if (proxy.protocol === 'socks5:') {
    return client => {
      void destination.then(target => SocksClient.createConnection({
        proxy: { host: proxy.hostname, port: Number(proxy.port), type: 5, ...(proxy.username ? { userId: decodeURIComponent(proxy.username) } : {}), ...(proxy.password ? { password: decodeURIComponent(proxy.password) } : {}) },
        command: 'connect', destination: target
      })).then(info => { client.setSocket(info.socket); client.emit('connect') }).catch(error => client.emit('error', error as Error))
    }
  }
  return client => {
    void destination.then(target => {
      const transport = proxy.protocol === 'https:' ? https : http
      const headers: Record<string, string> = {}
      if (proxy.username || proxy.password) headers['Proxy-Authorization'] = `Basic ${Buffer.from(`${decodeURIComponent(proxy.username)}:${decodeURIComponent(proxy.password)}`).toString('base64')}`
      const request = transport.request({ host: proxy.hostname, port: Number(proxy.port), method: 'CONNECT', path: `${target.host}:${target.port}`, headers })
      request.once('connect', (response, socket) => {
        if (response.statusCode !== 200) { socket.destroy(); client.emit('error', new Error(`proxy CONNECT returned ${response.statusCode}`)); return }
        client.setSocket(socket); client.emit('connect')
      })
      request.once('error', error => client.emit('error', error))
      request.end()
    }).catch(error => client.emit('error', error as Error))
  }
}

interface SRVRecord { name: string; port: number; priority: number; weight: number }
type SRVResolver = (hostname: string) => Promise<readonly SRVRecord[]>

export async function resolveMinecraftDestination(host: string, port: number, resolver: SRVResolver = resolveSrv): Promise<{ host: string; port: number }> {
  const fallback = { host, port }
  if (port !== 25565 || net.isIP(host) !== 0 || host === 'localhost') return fallback
  try {
    const records = (await resolver(`_minecraft._tcp.${host}`)).filter(record => record.name && Number.isInteger(record.port) && record.port > 0 && record.port <= 65535)
    if (records.length === 0) return fallback
    const selected = [...records].sort((left, right) => left.priority - right.priority || right.weight - left.weight || left.name.localeCompare(right.name))[0]
    if (!selected) return fallback
    return { host: selected.name.replace(/\.$/, ''), port: selected.port }
  } catch {
    return fallback
  }
}

export async function verifyEgress(proxyUrl: string, checkUrl: string, expectedIp: string): Promise<void> {
  const body = await new Promise<string>((resolve, reject) => {
    const request = https.get(checkUrl, { agent: proxyAgent(proxyUrl), timeout: 10_000, headers: { Accept: 'application/json' } }, response => {
      if (response.statusCode !== 200) { response.resume(); reject(new Error(`egress check returned HTTP ${response.statusCode}`)); return }
      const chunks: Buffer[] = []; let size = 0
      response.on('data', (chunk: Buffer) => { size += chunk.length; if (size > 4096) request.destroy(new Error('egress response too large')); else chunks.push(chunk) })
      response.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')))
    })
    request.once('timeout', () => request.destroy(new Error('egress check timed out')))
    request.once('error', reject)
  })
  let actual = body.trim()
  try { const parsed = JSON.parse(body) as { ip?: string }; actual = parsed.ip ?? actual } catch { /* plain-text providers are accepted */ }
  if (actual !== expectedIp) throw new EgressMismatchError(`proxy egress mismatch: expected ${expectedIp}, received ${actual.slice(0, 64)}`)
}
