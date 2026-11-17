import { cleanup, render } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import { ExecutionLog } from './ExecutionLog'
import { DecisionCard } from '../trader/DecisionCard'
import type { DecisionRecord } from '../../types'

afterEach(cleanup)
const oldError = 'opening skipped: xyz:CL is not supported on target exchange binance: failed to get price: <APIError> code=-1121, msg=Invalid symbol.'
function record(error = oldError): DecisionRecord {
  return {
    cycle_number: 1, timestamp: '2026-11-17T12:00:00Z', success: false,
    decisions: [{ action: 'open_long', symbol: 'xyz:CL', success: false, error }],
    execution_log: [`❌ xyz:CL open_long failed: ${error}`],
    error_message: '1 of 1 trade actions failed or were blocked',
  } as DecisionRecord
}

for (const view of ['terminal', 'card']) {
  it(`${view} displays historical unsupported openings as skips without mutating history`, () => {
    const data = record()
    const { container } = render(view === 'terminal' ? <ExecutionLog decisions={[data]} /> : <DecisionCard decision={data} language="en" />)
    expect(container.textContent).toContain('Skipped xyz:CL')
    expect(container.textContent).toContain('binance')
    expect(container.textContent).not.toMatch(/APIError|Invalid symbol|failed|FAULT/)
    expect(data.decisions[0].error).toBe(oldError)
    expect(data.success).toBe(false)
  })
  it(`${view} preserves real market query failures`, () => {
    const data = record('opening skipped: cannot verify market BTCUSDT on target exchange binance: timeout')
    const { container } = render(view === 'terminal' ? <ExecutionLog decisions={[data]} /> : <DecisionCard decision={data} language="en" />)
    expect(container.textContent).toContain('timeout')
    expect(container.textContent).toMatch(/failed|Failed|FAULT/)
  })
}

it('displays structured skips in Chinese without claiming an executed order', () => {
  const data = record()
  data.success = true
  data.error_message = ''
  data.decisions[0] = { ...data.decisions[0], error: '', skipped: true, skip_reason: 'opening skipped: xyz:CL is not supported on target exchange binance; no order placed' }
  data.execution_log = [`⚠ ${data.decisions[0].skip_reason}`]
  const { container } = render(<DecisionCard decision={data} language="zh" />)
  expect(container.textContent).toContain('已跳过 xyz:CL：当前交易所 binance 不支持该币种，未下单。')
  expect(container.textContent).not.toMatch(/❌|失败|APIError/)
  expect(data.decisions[0].success).toBe(false)
})

it('keeps mixed-cycle execution failures visible after removing skipped actions from the count', () => {
  const data = record()
  data.decisions.push({ ...data.decisions[0], symbol: 'BTCUSDT', error: 'insufficient balance' })
  data.error_message = '2 of 2 trade actions failed or were blocked'
  data.execution_log.push('❌ BTCUSDT open_long failed: insufficient balance')
  const { container } = render(<ExecutionLog decisions={[data]} />)
  expect(container.textContent).toContain('FAULT · 1/1 trades failed/blocked')
  expect(container.textContent).toContain('insufficient balance')
  expect(container.textContent).toContain('Skipped xyz:CL')
})
