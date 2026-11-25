import type { HistoricalPosition } from '../types'
export interface LedgerFilter {
  symbol: string
  side: string
  outcome: string
  since: number
  hideReconciled: boolean
}
export const netPnl = (p: HistoricalPosition) => p.realized_pnl - p.fee
export function filterLedger(rows: HistoricalPosition[], f: LedgerFilter) {
  return rows.filter(
    (p) =>
      p.symbol.toUpperCase().includes(f.symbol.trim().toUpperCase()) &&
      (f.side === 'all' || p.side.toLowerCase() === f.side) &&
      p.exit_time >= f.since &&
      (f.outcome === 'all' ||
        (f.outcome === 'profit'
          ? netPnl(p) > 0
          : f.outcome === 'loss'
            ? netPnl(p) < 0
            : netPnl(p) === 0)) &&
      (!f.hideReconciled ||
        !/(reconcil|sync|对账|同步)/i.test(p.close_reason || ''))
  )
}
export function ledgerStats(rows: HistoricalPosition[]) {
  const gross = rows.reduce((n, p) => n + p.realized_pnl, 0),
    fees = rows.reduce((n, p) => n + p.fee, 0)
  const wins = rows.filter((p) => netPnl(p) > 0).length,
    losses = rows.filter((p) => netPnl(p) < 0).length
  const profit = rows.reduce((n, p) => n + Math.max(netPnl(p), 0), 0),
    loss = rows.reduce((n, p) => n + Math.min(netPnl(p), 0), 0)
  return {
    gross,
    fees,
    net: gross - fees,
    wins,
    losses,
    winRate: rows.length ? (wins / rows.length) * 100 : null,
    profitFactor: loss < 0 ? profit / -loss : null,
  }
}
export function ledgerCSV(rows: HistoricalPosition[]) {
  const cell = (v: unknown) => {
    let s = String(v ?? '')
    if (typeof v === 'string' && /^[\s]*[=+@-]/.test(s)) s = "'" + s
    return '"' + s.replace(/"/g, '""') + '"'
  }
  const header = [
    'ID',
    'Symbol',
    'Side',
    'Entry time',
    'Exit time',
    'Entry price',
    'Exit price',
    'Quantity',
    'Leverage',
    'Gross PnL',
    'Fees',
    'Net PnL',
    'Close reason',
  ]
  return (
    '\uFEFF' +
    [
      header,
      ...rows.map((p) => [
        p.id,
        p.symbol,
        p.side,
        new Date(p.entry_time).toISOString(),
        new Date(p.exit_time).toISOString(),
        p.entry_price,
        p.exit_price,
        p.entry_quantity || p.quantity,
        p.leverage,
        p.realized_pnl,
        p.fee,
        netPnl(p),
        p.close_reason,
      ]),
    ]
      .map((row) => row.map(cell).join(','))
      .join('\r\n')
  )
}

// Keep already-seen boundary rows when new closes shift the newest page.
export function mergeLedgerRows(
  previous: HistoricalPosition[],
  incoming: HistoricalPosition[]
) {
  const rows = new Map(previous.map((p) => [p.id, p]))
  incoming.forEach((p) => rows.set(p.id, p))
  return [...rows.values()].sort(
    (a, b) => b.exit_time - a.exit_time || b.id - a.id
  )
}
