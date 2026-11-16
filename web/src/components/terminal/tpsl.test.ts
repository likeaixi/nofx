import { describe, expect, it } from 'vitest'
import type { DecisionRecord, OpenOrder } from '../../types'
import { baseLabel, buildTpslFallback, distPct, parseTpsl } from './tpsl'

const order = (o: Partial<OpenOrder>): OpenOrder => ({
  order_id: '1',
  symbol: 'BTCUSDT',
  side: 'SELL',
  position_side: 'LONG',
  type: 'STOP_MARKET',
  price: 0,
  stop_price: 100,
  quantity: 1,
  status: 'NEW',
  ...o,
})

describe('parseTpsl', () => {
  it('splits STOP_MARKET into sl and TAKE_PROFIT_MARKET into tp', () => {
    const r = parseTpsl([
      order({ type: 'TAKE_PROFIT_MARKET', stop_price: 110 }),
      order({ type: 'STOP_MARKET', stop_price: 90 }),
    ])
    expect(r).toEqual({ tp: [110], sl: [90] })
  })

  it('keeps multiple same-direction orders', () => {
    const r = parseTpsl([
      order({ type: 'TAKE_PROFIT_MARKET', stop_price: 110 }),
      order({ type: 'TAKE_PROFIT_MARKET', stop_price: 120 }),
    ])
    expect(r.tp).toEqual([110, 120])
    expect(r.sl).toEqual([])
  })

  it('ignores LIMIT orders and zero stop_price', () => {
    const r = parseTpsl([
      order({ type: 'LIMIT', price: 95, stop_price: 0 }),
      order({ type: 'STOP_MARKET', stop_price: 0 }),
    ])
    expect(r).toEqual({ tp: [], sl: [] })
  })

  it('tolerates undefined input', () => {
    expect(parseTpsl(undefined)).toEqual({ tp: [], sl: [] })
  })
})

describe('distPct', () => {
  it('computes signed percent distance from mark', () => {
    expect(distPct(110, 100)).toBeCloseTo(10)
    expect(distPct(90, 100)).toBeCloseTo(-10)
  })
  it('returns NaN for non-positive mark', () => {
    expect(distPct(110, 0)).toBeNaN()
  })
})

describe('buildTpslFallback', () => {
  const rec = (actions: DecisionRecord['decisions']): DecisionRecord => ({
    timestamp: '2026-11-16T00:00:00Z',
    cycle_number: 1,
    system_prompt: '',
    input_prompt: '',
    cot_trace: '',
    decision_json: '',
    account_state: {} as DecisionRecord['account_state'],
    positions: [],
    candidate_coins: [],
    decisions: actions,
    execution_log: [],
    success: true,
  })

  it('maps newest-first decisions by normalized symbol; first entry wins', () => {
    const m = buildTpslFallback([
      rec([
        {
          action: 'OPEN_LONG',
          symbol: 'BTCUSDT',
          quantity: 1,
          leverage: 1,
          price: 100,
          stop_loss: 90,
          take_profit: 120,
          order_id: 1,
          timestamp: '',
          success: true,
        },
      ]),
      rec([
        {
          action: 'OPEN_LONG',
          symbol: 'BTCUSDT',
          quantity: 1,
          leverage: 1,
          price: 100,
          stop_loss: 80,
          take_profit: 130,
          order_id: 2,
          timestamp: '',
          success: true,
        },
      ]),
    ])
    expect(m.get('BTC')).toEqual({ tp: 120, sl: 90 })
  })

  it('matches symbol formats BTCUSDT / xyz:BTC / BTC to the same key', () => {
    const m = buildTpslFallback([
      rec([
        {
          action: 'OPEN_LONG',
          symbol: 'xyz:BTC',
          quantity: 1,
          leverage: 1,
          price: 100,
          stop_loss: 90,
          take_profit: 0,
          order_id: 1,
          timestamp: '',
          success: true,
        },
      ]),
    ])
    expect(m.get('BTC')).toEqual({ tp: undefined, sl: 90 })
    expect(m.get('BTCUSDT')).toBeUndefined() // key is normalized base label
  })

  it('skips actions without positive tp/sl', () => {
    const m = buildTpslFallback([
      rec([
        {
          action: 'OPEN_LONG',
          symbol: 'ETH',
          quantity: 1,
          leverage: 1,
          price: 100,
          stop_loss: 0,
          take_profit: 0,
          order_id: 1,
          timestamp: '',
          success: true,
        },
      ]),
    ])
    expect(m.size).toBe(0)
  })
})

describe('baseLabel', () => {
  it('normalizes xyz prefix, separators and stablecoin suffix', () => {
    expect(baseLabel('xyz:BTC')).toBe('BTC')
    expect(baseLabel('BTC-USDT')).toBe('BTC')
    expect(baseLabel('btcusdt')).toBe('BTC')
    expect(baseLabel('PEPE')).toBe('PEPE')
  })
})
