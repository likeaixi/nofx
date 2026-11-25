import { expect, it } from 'vitest'
import { cycleCountdown } from './cycleCountdown'
const now = Date.parse('2026-11-25T10:00:00Z')
const status = {
  is_running: true,
  strategy_type: 'allincrypto' as const,
  next_cycle_at: '2026-11-25T10:01:00Z',
}
it('counts down the scheduler timestamp independently of decision logs', () => {
  expect(cycleCountdown(status, now)).toBe('1m 0s')
  expect(cycleCountdown(status, now + 1000)).toBe('0m 59s')
  expect(cycleCountdown(status, now + 59001)).toBe('0m 1s')
})
it('distinguishes running, overdue, stopped, and unavailable schedules', () => {
  expect(cycleCountdown({ ...status, cycle_in_progress: true }, now)).toBe(
    'checking…'
  )
  expect(
    cycleCountdown(
      { ...status, cycle_in_progress: true, strategy_type: 'ai_trading' },
      now
    )
  ).toBe('running…')
  expect(cycleCountdown(status, now + 61000)).toBe('waiting…')
  expect(cycleCountdown({ ...status, is_running: false }, now)).toBe('stopped')
  expect(cycleCountdown({ ...status, next_cycle_at: undefined }, now)).toBe('—')
  expect(cycleCountdown({ ...status, next_cycle_at: 'invalid' }, now)).toBe('—')
})
