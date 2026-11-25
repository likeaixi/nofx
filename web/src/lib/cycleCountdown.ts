import type { SystemStatus } from '../types'

type CycleStatus = Pick<
  SystemStatus,
  'is_running' | 'next_cycle_at' | 'cycle_in_progress' | 'strategy_type'
>

// Decision-log timestamps are deliberately excluded: log cadence and scheduler
// cadence differ, particularly for one-minute copy checks / 15-minute reports.
export function cycleCountdown(
  status: CycleStatus | undefined,
  now: number
): string {
  if (!status) return '—'
  if (!status.is_running) return 'stopped'
  if (status.cycle_in_progress)
    return status.strategy_type === 'allincrypto' ? 'checking…' : 'running…'
  if (!status.next_cycle_at) return '—'
  const next = Date.parse(status.next_cycle_at)
  if (!Number.isFinite(next)) return '—'
  if (next <= now) return 'waiting…'
  const seconds = Math.ceil((next - now) / 1000)
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}
