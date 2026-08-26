import assert from 'node:assert/strict'
import { chmodSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import test from 'node:test'
import { loadConfig, observerAuthorizationEnabled } from './config.js'

function config(backendUrl: string): object {
  return {
    backendUrl,
    observerToken: 'observer-token-123456',
    minecraftHost: 'play.example.test',
    minecraftPort: 25565,
    version: '1.21.11',
    expectedEgressCheckUrl: 'https://example.test/ip',
    accounts: [{
      id: 'observer-1', microsoftUsername: 'observer@example.test', proxyLabel: 'proxy-1',
      proxyUrl: 'socks5://user:pass@proxy.example.test:1080', expectedEgressIp: '192.0.2.10', profilesFolder: './profiles/observer-1'
    }]
  }
}

function withConfig(value: object, run: (path: string) => void): void {
  const directory = mkdtempSync(join(tmpdir(), 'donut-collector-config-'))
  const path = join(directory, 'accounts.json')
  try {
    writeFileSync(path, JSON.stringify(value), { mode: 0o600 })
    chmodSync(path, 0o600)
    run(path)
  } finally {
    rmSync(directory, { recursive: true, force: true })
  }
}

test('allows loopback HTTP for local development', () => {
  withConfig(config('http://127.0.0.1:8080'), path => assert.equal(loadConfig(path).backendUrl, 'http://127.0.0.1:8080'))
})

test('requires HTTPS for a remote backend', () => {
  withConfig(config('http://backend.example.test'), path => assert.throws(() => loadConfig(path), /must use HTTPS/))
  withConfig(config('https://backend.example.test'), path => assert.equal(loadConfig(path).backendUrl, 'https://backend.example.test'))
})

test('requires an exact explicit server-observer authorization flag', () => {
  assert.equal(observerAuthorizationEnabled({}), false)
  assert.equal(observerAuthorizationEnabled({ DN_SERVER_OBSERVER_AUTHORIZED: 'false' }), false)
  assert.equal(observerAuthorizationEnabled({ DN_SERVER_OBSERVER_AUTHORIZED: 'TRUE' }), false)
  assert.equal(observerAuthorizationEnabled({ DN_SERVER_OBSERVER_AUTHORIZED: 'true' }), true)
})
