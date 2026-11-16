import type { DecisionRecord, OpenOrder } from '../../types'

export interface TpslFallback {
  tp?: number
  sl?: number
}

/** Split exchange open orders into TP and SL trigger prices. */
export function parseTpsl(orders: OpenOrder[] | undefined): { tp: number[]; sl: number[] } {
  const tp: number[] = []
  const sl: number[] = []
  for (const o of orders ?? []) {
    if (!o || !(o.stop_price > 0)) continue
    if (o.type === 'TAKE_PROFIT_MARKET') tp.push(o.stop_price)
    else if (o.type === 'STOP_MARKET') sl.push(o.stop_price)
  }
  return { tp, sl }
}

/** Signed percent distance from current mark price to a trigger price. */
export function distPct(trigger: number, mark: number): number {
  if (!(mark > 0)) return NaN
  return ((trigger - mark) / mark) * 100
}

/** crypto majors trade on the Hyperliquid main dex; strip namespaces and quote suffixes. */
export function baseLabel(raw?: string): string {
  if (!raw) return ''
  return raw.toUpperCase().replace(/^XYZ:/, '').replace(/[-_]/g, '').replace(/(USDT|USDC|USD)$/, '')
}

/**
 * Map normalized symbol → latest decision-intent TP/SL (fallback when the
 * exchange open-orders query fails). Records arrive newest-first, so the
 * first matching action per symbol wins.
 */
export function buildTpslFallback(decisions: DecisionRecord[] | undefined): Map<string, TpslFallback> {
  const m = new Map<string, TpslFallback>()
  for (const rec of decisions ?? []) {
    for (const a of rec?.decisions ?? []) {
      if (!a?.symbol) continue
      const key = baseLabel(a.symbol)
      if (!key || m.has(key)) continue
      const tp = (a.take_profit ?? 0) > 0 ? a.take_profit : undefined
      const sl = (a.stop_loss ?? 0) > 0 ? a.stop_loss : undefined
      if (tp === undefined && sl === undefined) continue
      m.set(key, { tp, sl })
    }
  }
  return m
}
