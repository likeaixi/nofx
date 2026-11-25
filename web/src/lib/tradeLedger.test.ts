import { describe, it, expect } from 'vitest'
import {
  filterLedger,
  ledgerStats,
  ledgerCSV,
  mergeLedgerRows,
} from './tradeLedger'
import type { HistoricalPosition } from '../types'
const rows = [
  {
    id: 1,
    symbol: 'BTCUSDT',
    side: 'LONG',
    exit_time: 100,
    entry_time: 1,
    realized_pnl: 10,
    fee: 12,
    close_reason: 'AI close',
  },
  {
    id: 2,
    symbol: 'ETHUSDT',
    side: 'SHORT',
    exit_time: 200,
    entry_time: 1,
    realized_pnl: 20,
    fee: 2,
    close_reason: 'sync close',
  },
] as HistoricalPosition[]
describe('trade ledger', () => {
  it('uses net outcomes and consistent filters', () => {
    expect(
      filterLedger(rows, {
        symbol: 'btc',
        side: 'long',
        outcome: 'loss',
        since: 0,
        hideReconciled: false,
      })
    ).toEqual([rows[0]])
    expect(
      filterLedger(rows, {
        symbol: '',
        side: 'all',
        outcome: 'all',
        since: 150,
        hideReconciled: true,
      })
    ).toEqual([])
  })
  it('subtracts fees once and keeps empty rate unavailable', () => {
    expect(ledgerStats(rows)).toMatchObject({
      gross: 30,
      fees: 14,
      net: 16,
      winRate: 50,
      wins: 1,
      losses: 1,
    })
    expect(ledgerStats([]).winRate).toBeNull()
  })
  it('quotes exported text and prevents spreadsheet formulas', () => {
    expect(
      ledgerCSV([{ ...rows[0], close_reason: '=HYPERLINK("bad")' }])
    ).toContain('"\'=HYPERLINK(""bad"")"')
  })
})

it('keeps a shifted page boundary and updates records without duplicates', () => {
  const first = Array.from({ length: 500 }, (_, id) => ({
    ...rows[0],
    id,
    exit_time: 1000 - id,
  }))
  const newer = [
    { ...rows[0], id: 500, exit_time: 1001 },
    ...first.slice(0, 499),
  ]
  const merged = mergeLedgerRows(first, newer)
  expect(merged).toHaveLength(501)
  expect(merged.some((p) => p.id === 499)).toBe(true)
  expect(
    mergeLedgerRows(merged, [{ ...first[0], fee: 15 }]).find((p) => p.id === 0)
      ?.fee
  ).toBe(15)
})
