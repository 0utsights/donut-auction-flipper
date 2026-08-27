import type { TaskKind } from './types.js'

export type TaskFailureClass = 'schema_hold' | 'reconnect_required' | 'menu_session_ended' | 'other'
export type TaskResultStatus = 'complete' | 'retry' | 'failed'

export function backendRetryDelay(failures: number): number {
  return Math.min(30_000, 1_000 * 2 ** Math.min(5, Math.max(0, failures - 1)))
}

export function taskResultForFailure(kind: TaskKind, priority: number, failure: TaskFailureClass): TaskResultStatus {
  if (failure === 'schema_hold') return 'failed'
  if (failure === 'reconnect_required') return 'complete'
  // An automatic focused task that loses its menu must not immediately lease
  // itself again from page one and monopolize the only observer. Completing it
  // applies the normal per-item cooldown; a later discovery pass can requeue it.
  // Priority 100 is a live player request and may retry within its short watch.
  if (failure === 'menu_session_ended' && kind === 'focused_watch' && priority < 100) return 'complete'
  return 'retry'
}
