import nodeFetch from 'node-fetch'
import type { Agent } from 'node:http'
import { proxyAgent } from './proxy.js'

type FetchImplementation = (target: string | URL, options: Record<string, unknown>) => Promise<unknown>
type AgentFactory = (proxyUrl: string) => Agent

// prismarine-auth uses the process-global fetch implementation and does not
// consume minecraft-protocol's `agent` option. Install a narrowly scoped fetch
// wrapper while Microsoft/Xbox/Minecraft credentials are obtained, then restore
// it as soon as minecraft-protocol emits `session` so local backend traffic is
// never sent to the account proxy.
export function installAuthenticationProxy(proxyUrl: string,
                                           fetchImplementation: FetchImplementation = nodeFetch as unknown as FetchImplementation,
                                           agentFactory: AgentFactory = proxyAgent): () => void {
  const original = globalThis.fetch
  const agent: Agent = agentFactory(proxyUrl)
  let active = true
  globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit): Promise<Response> => {
    const target = typeof input === 'string' || input instanceof URL ? input : input.url
    const options = { ...(init as unknown as Record<string, unknown>), agent }
    const response = await fetchImplementation(target, options)
    return response as unknown as Response
  }) as typeof globalThis.fetch
  return () => {
    if (!active) return
    active = false
    globalThis.fetch = original
  }
}
