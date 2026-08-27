const MAX_RESTART_DELAY_MS = 5 * 60_000

// Fast retries recover transient network failures. Repeated failed logins back
// off to five minutes so Donut's session/sequence cooldown can actually clear
// instead of being extended by a new connection every minute.
export function restartDelay(failureCount: number): number {
  const failures = Math.max(1, Math.min(30, Math.trunc(failureCount)))
  return Math.min(MAX_RESTART_DELAY_MS, 1_000 * 2 ** Math.min(9, failures))
}
