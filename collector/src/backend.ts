import type { ObserverTask, ScanBatch } from './types.js'
import { PARSER_VERSION } from './types.js'

export class BackendClient {
  constructor(private readonly base: URL, private readonly token: string, private readonly observerId: string) {}

  async register(proxyLabel: string): Promise<void> {
    await this.request('/api/v1/observers/register', 'POST', {
      observer_id: this.observerId, parser_version: PARSER_VERSION, proxy_label: proxyLabel,
      capabilities: ['orders', 'window_capture', 'safe_pagination']
    })
  }

  async heartbeat(state: string, taskId = '', leaseToken = '', page = 0, latencyMs = 0, reconnectCount = 0): Promise<void> {
    await this.request('/api/v1/observers/heartbeat', 'POST', {
      observer_id: this.observerId, state, task_id: taskId, lease_token: leaseToken, page, latency_ms: latencyMs, reconnect_count: reconnectCount
    })
  }

  async nextTask(signal: AbortSignal): Promise<ObserverTask | undefined> {
    const result = await this.request(`/api/v1/observers/tasks?observer_id=${encodeURIComponent(this.observerId)}`, 'GET', undefined, signal)
    if (result.status === 204) return undefined
    const body = await result.json() as { task: ObserverTask }
    return body.task
  }

  async submitScan(scan: ScanBatch): Promise<void> { await this.request('/api/v1/observers/order-scans', 'POST', scan) }

  async finishTask(taskId: string, leaseToken: string, status: 'complete' | 'retry' | 'failed', message = ''): Promise<void> {
    await this.request('/api/v1/observers/task-result', 'POST', { observer_id: this.observerId, task_id: taskId, lease_token: leaseToken, status, message })
  }

  private async request(path: string, method: string, body?: unknown, signal?: AbortSignal): Promise<Response> {
    const response = await fetch(new URL(path, this.base), {
      method, headers: { Accept: 'application/json', Authorization: `Bearer ${this.token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }), ...(signal === undefined ? {} : { signal })
    })
    if (!response.ok && response.status !== 204) {
      const message = (await response.text()).slice(0, 300).replace(/[\r\n]/g, ' ')
      throw new Error(`backend HTTP ${response.status}: ${message}`)
    }
    return response
  }
}
