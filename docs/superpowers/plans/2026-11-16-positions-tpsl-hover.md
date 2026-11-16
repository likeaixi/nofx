# Positions TP/SL Hover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Dashboard Positions 表格每行显示 TP/SL 状态点,hover 弹出交易所当前真实挂单的止盈/止损触发价与距标记价百分比(decisions 数据兜底)。

**Architecture:** 纯前端改动。新组件 `PositionTPSL` 每行一个,内部 SWR 拉取既有端点 `GET /api/open-orders?trader_id&symbol`(15s 刷新);纯逻辑(解析/兜底归并/距离)拆到 `tpsl.ts` 便于单测;自研轻量 portal tooltip。`TerminalDashboard` 增加一列并按 symbol 归并 decisions 兜底值。

**Tech Stack:** React 18 + TypeScript + SWR 2 + Vitest + @testing-library/react(jsdom)。无新依赖、无后端改动。

**Spec:** `docs/superpowers/specs/2026-11-16-positions-tpsl-hover-design.md`

## Global Constraints

- 配色:TP 绿 `#2E8B57`,SL 红 `#D6433A`(与 `DecisionCard.tsx:108-145` 惯例一致)
- 不引入任何第三方 tooltip 库;tooltip 用 `createPortal(document.body)` + `fixed` + 视口钳制
- 后端零改动;唯一数据端点 `GET /api/open-orders?trader_id&symbol`(必填 symbol)
- SWR `refreshInterval: 15000`;key 形如 `open-orders-${traderId}-${symbol}`(仓库存惯例为模板串 key)
- demo 模式(无 `traderId`)不发请求、单元格为空,列结构两种模式下不变
- 所有命令在 `web/` 目录执行;类型检查用 `npx tsc`(tsconfig 已 `noEmit`),测试 `npm run test`, lint `npm run lint`
- 提交信息结尾带 `Co-Authored-By: Claude <noreply@anthropic.com>`

## File Structure

| 文件 | 职责 |
|---|---|
| `web/src/types/trading.ts`(改) | 新增 `OpenOrder` 接口(镜像后端 `trader/types/interface.go:108-118` 的 json tag) |
| `web/src/lib/api/data.ts`(改) | `api` 对象新增 `getOpenOrders` |
| `web/src/components/terminal/tpsl.ts`(新) | 纯逻辑:`parseTpsl`、`buildTpslFallback`、`distPct`、`baseLabel`(自 TerminalDashboard 迁出,单一来源) |
| `web/src/components/terminal/tpsl.test.ts`(新) | 上述纯函数单测 |
| `web/src/components/terminal/PositionTPSL.tsx`(新) | `PositionTPSL` 组件 + 同文件 `TerminalTooltip`(仅此一处使用) |
| `web/src/components/terminal/PositionTPSL.test.tsx`(新) | 组件渲染/交互测试(mock api) |
| `web/src/components/terminal/TerminalDashboard.tsx`(改) | 表头/表体加 TP/SL 列;删本地 `baseLabel` 改 import;`useMemo` 归并兜底 |

---

### Task 1: `OpenOrder` 类型与 `getOpenOrders` API 函数

**Files:**
- Modify: `web/src/types/trading.ts`(在 `DecisionAction` 之前,约 :50 处插入)
- Modify: `web/src/lib/api/data.ts`(`api` 对象内,`getPositions` 之后,约 :84 处插入)

**Interfaces:**
- Consumes: 后端 `GET /api/open-orders` 返回 `types.OpenOrder[]`,json tag 为 `order_id/symbol/side/position_side/type/price/stop_price/quantity/status`
- Produces: `OpenOrder` 接口(`web/src/types`,经 index 再导出);`api.getOpenOrders(traderId: string, symbol: string, silent?: boolean): Promise<OpenOrder[]>`(Task 3/4 依赖)

说明:该函数是 `httpClient.request` 薄封装,仓库惯例(data.ts 无单测)不为薄封装写测试,以 `npx tsc` 验证。

- [ ] **Step 1: 在 `web/src/types/trading.ts` 的 `Position` 接口后新增 `OpenOrder`**

```ts
// Mirrors backend trader/types.OpenOrder (GET /api/open-orders).
export interface OpenOrder {
  order_id: string
  symbol: string
  side: string // BUY/SELL
  position_side: string // LONG/SHORT
  type: string // LIMIT/STOP_MARKET/TAKE_PROFIT_MARKET
  price: number
  stop_price: number // Trigger price (for stop orders)
  quantity: number
  status: string
}
```

- [ ] **Step 2: 在 `web/src/lib/api/data.ts` 的 `api` 对象内 `getPositions` 之后新增方法**

```ts
  async getOpenOrders(
    traderId: string,
    symbol: string,
    silent?: boolean
  ): Promise<OpenOrder[]> {
    const params = new URLSearchParams({
      trader_id: traderId,
      symbol,
    })
    const result = await httpClient.request<OpenOrder[]>(
      `${API_BASE}/open-orders?${params}`,
      { silent }
    )
    if (!result.success) throw new Error('Failed to fetch open orders')
    return result.data!
  },
```

注意:`data.ts` 顶部 import 处把 `OpenOrder` 加进现有的 `types` import 列表(该文件已 import `Position`、`DecisionRecord` 等,遵循同一写法)。

- [ ] **Step 3: 类型检查**

Run: `cd web && npx tsc`
Expected: 无错误退出

- [ ] **Step 4: Commit**

```bash
git add web/src/types/trading.ts web/src/lib/api/data.ts
git commit -m "feat(web): add OpenOrder type and getOpenOrders api client

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 纯逻辑模块 `tpsl.ts`(TDD)

**Files:**
- Create: `web/src/components/terminal/tpsl.ts`
- Test: `web/src/components/terminal/tpsl.test.ts`

**Interfaces:**
- Consumes: Task 1 的 `OpenOrder`;`types` 的 `DecisionRecord`(其 `decisions: DecisionAction[]` 含 `symbol/stop_loss?/take_profit?`)
- Produces(Task 3/4 依赖,签名精确如下):
  - `parseTpsl(orders: OpenOrder[] | undefined): { tp: number[]; sl: number[] }`
  - `distPct(trigger: number, mark: number): number`(mark<=0 返回 `NaN`)
  - `buildTpslFallback(decisions: DecisionRecord[] | undefined): Map<string, { tp?: number; sl?: number }>`
  - `baseLabel(raw?: string): string`(与 TerminalDashboard.tsx:58-61 现有实现逐字一致)

- [ ] **Step 1: 写失败测试 `tpsl.test.ts`**

```ts
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
      rec([{ action: 'OPEN_LONG', symbol: 'BTCUSDT', quantity: 1, leverage: 1, price: 100, stop_loss: 90, take_profit: 120, order_id: 1, timestamp: '', success: true }]),
      rec([{ action: 'OPEN_LONG', symbol: 'BTCUSDT', quantity: 1, leverage: 1, price: 100, stop_loss: 80, take_profit: 130, order_id: 2, timestamp: '', success: true }]),
    ])
    expect(m.get('BTC')).toEqual({ tp: 120, sl: 90 })
  })

  it('matches symbol formats BTCUSDT / xyz:BTC / BTC to the same key', () => {
    const m = buildTpslFallback([
      rec([{ action: 'OPEN_LONG', symbol: 'xyz:BTC', quantity: 1, leverage: 1, price: 100, stop_loss: 90, take_profit: 0, order_id: 1, timestamp: '', success: true }]),
    ])
    expect(m.get('BTC')).toEqual({ tp: undefined, sl: 90 })
    expect(m.get('BTCUSDT')).toBeUndefined() // key is normalized base label
  })

  it('skips actions without positive tp/sl', () => {
    const m = buildTpslFallback([
      rec([{ action: 'OPEN_LONG', symbol: 'ETH', quantity: 1, leverage: 1, price: 100, stop_loss: 0, take_profit: 0, order_id: 1, timestamp: '', success: true }]),
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
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/terminal/tpsl.test.ts`
Expected: FAIL(模块 `./tpsl` 不存在)

- [ ] **Step 3: 实现 `tpsl.ts`**

```ts
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
```

- [ ] **Step 4: 运行确认通过**

Run: `cd web && npx vitest run src/components/terminal/tpsl.test.ts`
Expected: PASS(全部用例)

- [ ] **Step 5: Commit**

```bash
git add web/src/components/terminal/tpsl.ts web/src/components/terminal/tpsl.test.ts
git commit -m "feat(web): add tpsl parsing and decision-fallback helpers

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: `PositionTPSL` 组件 + `TerminalTooltip`(TDD)

**Files:**
- Create: `web/src/components/terminal/PositionTPSL.tsx`
- Test: `web/src/components/terminal/PositionTPSL.test.tsx`

**Interfaces:**
- Consumes: Task 1 `api.getOpenOrders`;Task 2 `parseTpsl/distPct/TpslFallback`;`swr` 的 `useSWR`
- Produces(Task 4 依赖): `export function PositionTPSL(props: { traderId?: string; symbol: string; markPrice: number; fallback?: TpslFallback }): JSX.Element | null`;`export function TerminalTooltip(props: { content: React.ReactNode; children: React.ReactNode })`

- [ ] **Step 1: 写失败测试 `PositionTPSL.test.tsx`**

```tsx
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { PositionTPSL } from './PositionTPSL'
import { api } from '../../lib/api'
import type { OpenOrder } from '../../types'

vi.mock('../../lib/api', () => ({
  api: { getOpenOrders: vi.fn() },
}))
const getOpenOrders = vi.mocked(api.getOpenOrders)

afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})

function mount(props: Partial<{ traderId?: string; symbol: string; markPrice: number; fallback?: { tp?: number; sl?: number } }> = {}) {
  return render(
    <SWRConfig value={{ provider: () => new Map(), dedupingInterval: 0 }}>
      <PositionTPSL traderId="t1" symbol="BTCUSDT" markPrice={100} {...props} />
    </SWRConfig>
  )
}

const order = (o: Partial<OpenOrder>): OpenOrder => ({
  order_id: '1',
  symbol: 'BTCUSDT',
  side: 'SELL',
  position_side: 'LONG',
  type: 'STOP_MARKET',
  price: 0,
  stop_price: 90,
  quantity: 1,
  status: 'NEW',
  ...o,
})

describe('PositionTPSL', () => {
  it('renders TP and SL dots and shows prices+pct on hover', async () => {
    getOpenOrders.mockResolvedValue([
      order({ type: 'TAKE_PROFIT_MARKET', stop_price: 110 }),
      order({ type: 'STOP_MARKET', stop_price: 90 }),
    ])
    const { container } = mount()
    const trigger = await screen.findByTestId('tpsl-trigger')
    expect(screen.getAllByText('●')).toHaveLength(2)

    fireEvent.mouseEnter(trigger)
    expect(screen.getByText('TP 110.0 +10.00%')).toBeInTheDocument()
    expect(screen.getByText('SL 90.00 -10.00%')).toBeInTheDocument()
    fireEvent.mouseLeave(trigger)
    expect(screen.queryByText('TP 110.0 +10.00%')).toBeNull()
    expect(container).toBeInTheDocument()
  })

  it('renders only the red dot when only a SL order exists', async () => {
    getOpenOrders.mockResolvedValue([order({ type: 'STOP_MARKET', stop_price: 90 })])
    mount()
    const dots = await screen.findAllByText('●')
    expect(dots).toHaveLength(1)
    expect(dots[0]).toHaveStyle({ color: '#D6433A' })
  })

  it('falls back to decision values with a label when the exchange query fails', async () => {
    getOpenOrders.mockRejectedValue(new Error('boom'))
    mount({ fallback: { tp: 120, sl: 80 } })
    fireEvent.mouseEnter(await screen.findByTestId('tpsl-trigger'))
    expect(await screen.findByText('TP 120.0 +20.00%')).toBeInTheDocument()
    expect(screen.getByText('SL 80.00 -20.00%')).toBeInTheDocument()
    expect(screen.getByText('decision fallback')).toBeInTheDocument()
  })

  it('renders nothing when there are no orders and no fallback', async () => {
    getOpenOrders.mockResolvedValue([])
    const { container } = mount()
    await waitFor(() => expect(getOpenOrders).toHaveBeenCalled())
    expect(container.innerHTML).toBe('')
  })

  it('does not fetch without a traderId (demo mode)', () => {
    const { container } = mount({ traderId: undefined })
    expect(getOpenOrders).not.toHaveBeenCalled()
    expect(container.innerHTML).toBe('')
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npx vitest run src/components/terminal/PositionTPSL.test.tsx`
Expected: FAIL(模块 `./PositionTPSL` 不存在)

- [ ] **Step 3: 实现 `PositionTPSL.tsx`**

```tsx
import { useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties, ReactNode } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import { distPct, parseTpsl } from './tpsl'
import type { TpslFallback } from './tpsl'

const TP_COLOR = '#2E8B57'
const SL_COLOR = '#D6433A'

/** Lightweight hover tooltip: portals to body, fixed position, viewport-clamped. */
export function TerminalTooltip({ content, children }: { content: ReactNode; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  const [style, setStyle] = useState<CSSProperties | null>(null)
  const anchorRef = useRef<HTMLSpanElement>(null)
  const popRef = useRef<HTMLDivElement>(null)

  // Measure after mount, then clamp inside the viewport. The table sits in the
  // page's bottom-right corner, so pop toward upper-left by default.
  useLayoutEffect(() => {
    if (!open) return
    const a = anchorRef.current?.getBoundingClientRect()
    const p = popRef.current
    if (!a || !p) return
    const M = 8
    const w = p.offsetWidth
    const h = p.offsetHeight
    let left = a.right - w
    let top = a.top - h - 6
    if (top < M) top = Math.min(a.bottom + 6, window.innerHeight - h - M)
    if (left < M) left = M
    if (left + w > window.innerWidth - M) left = window.innerWidth - w - M
    setStyle({ position: 'fixed', top, left })
  }, [open, content])

  return (
    <span
      ref={anchorRef}
      data-testid="tpsl-trigger"
      style={{ cursor: 'help' }}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      {children}
      {open &&
        createPortal(
          <div
            ref={popRef}
            className="tm-mono"
            style={{
              ...(style ?? { position: 'fixed', top: -9999, left: -9999, visibility: 'hidden' }),
              zIndex: 1000,
              background: 'var(--tm-bg, #0b0f14)',
              border: '1px solid var(--tm-hair, #263140)',
              borderRadius: 4,
              padding: '6px 8px',
              fontSize: 11,
              lineHeight: 1.6,
              whiteSpace: 'nowrap',
              boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
              pointerEvents: 'none',
            }}
          >
            {content}
          </div>,
          document.body
        )}
    </span>
  )
}

interface PositionTPSLProps {
  traderId?: string
  symbol: string
  markPrice: number
  fallback?: TpslFallback
}

function fmtPx(n: number): string {
  const dp = n >= 1000 ? 0 : n >= 100 ? 1 : n >= 1 ? 2 : 4
  return n.toLocaleString('en-US', { minimumFractionDigits: dp, maximumFractionDigits: dp })
}
function fmtPct(n: number): string {
  return Number.isNaN(n) ? '—' : `${n >= 0 ? '+' : ''}${n.toFixed(2)}%`
}

/** TP/SL status dots for one position row; hover shows trigger prices vs mark. */
export function PositionTPSL({ traderId, symbol, markPrice, fallback }: PositionTPSLProps) {
  const { data, error } = useSWR(
    traderId ? `open-orders-${traderId}-${symbol}` : null,
    () => api.getOpenOrders(traderId!, symbol, true),
    { refreshInterval: 15000 }
  )

  const { tp, sl } = parseTpsl(data)
  const useFallback = !!error
  const fbTp = useFallback && fallback !== undefined && (fallback.tp ?? 0) > 0 ? fallback.tp : undefined
  const fbSl = useFallback && fallback !== undefined && (fallback.sl ?? 0) > 0 ? fallback.sl : undefined
  const tpList = fbTp !== undefined ? [fbTp] : tp
  const slList = fbSl !== undefined ? [fbSl] : sl
  if (tpList.length === 0 && slList.length === 0) return null

  const row = (kind: 'TP' | 'SL', price: number) => (
    <div key={`${kind}-${price}`} style={{ color: kind === 'TP' ? TP_COLOR : SL_COLOR }}>
      {kind} {fmtPx(price)} {fmtPct(distPct(price, markPrice))}
    </div>
  )

  return (
    <TerminalTooltip
      content={
        <>
          {tpList.map((p) => row('TP', p))}
          {slList.map((p) => row('SL', p))}
          {useFallback && (
            <div style={{ color: 'var(--tm-muted, #6b7a8c)', fontSize: 9 }}>decision fallback</div>
          )}
        </>
      }
    >
      <span style={{ letterSpacing: 2 }}>
        {tpList.length > 0 && <span style={{ color: TP_COLOR }}>●</span>}
        {slList.length > 0 && (
          <span style={{ color: SL_COLOR, marginLeft: tpList.length > 0 ? 3 : 0 }}>●</span>
        )}
      </span>
    </TerminalTooltip>
  )
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd web && npx vitest run src/components/terminal/PositionTPSL.test.tsx`
Expected: PASS(5 个用例)

- [ ] **Step 5: Commit**

```bash
git add web/src/components/terminal/PositionTPSL.tsx web/src/components/terminal/PositionTPSL.test.tsx
git commit -m "feat(web): add PositionTPSL hover component with live orders and decision fallback

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 集成进 TerminalDashboard Positions 表格

**Files:**
- Modify: `web/src/components/terminal/TerminalDashboard.tsx`(imports :14-21;删本地 `baseLabel` :58-61;`tpslFallback` memo 与列插入见下)

**Interfaces:**
- Consumes: Task 2 `baseLabel/buildTpslFallback`;Task 3 `PositionTPSL`(props 见 Task 3 Produces)
- Produces: 无(终端消费方)

- [ ] **Step 1: 改 imports**

在 `import { ExecutionLog } from './ExecutionLog'`(约 :17)之后加:

```tsx
import { PositionTPSL } from './PositionTPSL'
import { baseLabel, buildTpslFallback } from './tpsl'
```

删除 TerminalDashboard.tsx:58-61 的本地 `baseLabel` 函数定义(整段删除,函数体已迁至 `tpsl.ts`,文件内其他调用点不变)。

- [ ] **Step 2: 加兜底归并 memo**

在 `const sim = useDemoEngine(demo)`(:199 附近)之前或之后任意组件体位置加(与既有 `useMemo` 用法一致):

```tsx
const tpslFallback = useMemo(() => buildTpslFallback(decisions), [decisions])
```

- [ ] **Step 3: 表头加列**

`TerminalDashboard.tsx:498-506` 表头行,`return%` 单元格之后、`{traderId && !on && <td .../>}`(close 列,:505)之前插入:

```tsx
<td style={{ padding: '0 0 3px', textAlign: 'right' }}>TP/SL</td>
```

- [ ] **Step 4: 表体加单元格**

每行(:533)`{traderId && !on && (...)` close 单元格之前插入:

```tsx
<td style={{ padding: '5px 0', textAlign: 'right', width: 1 }}>
  <PositionTPSL
    traderId={traderId}
    symbol={p.symbol}
    markPrice={p.mark_price || p.entry_price}
    fallback={tpslFallback.get(baseLabel(p.symbol))}
  />
</td>
```

- [ ] **Step 5: 全量验证**

Run: `cd web && npx tsc && npm run test && npm run lint`
Expected: 三者全部通过(既有测试无回归)

- [ ] **Step 6: 手动验证(spec 第 6 节场景)**

`cd web && npm run dev` 后对照:
1. 实盘持仓同时挂 TP+SL → 双点,tooltip 价格与距离%正确
2. 仅挂 SL → 仅红点
3. 同 symbol 多张 TP → tooltip 逐行列出
4. 交易所手动撤单 → ≤15s 标记熄灭
5. 使 open-orders 请求失败(如断网)→ decisions 兜底 + `decision fallback` 标注
6. 表格在视口右下角 hover → tooltip 向左上弹出、不溢出
7. demo 模式 → TP/SL 列为空、无报错
8. 行点击选币、close 按钮不受影响

- [ ] **Step 7: Commit**

```bash
git add web/src/components/terminal/TerminalDashboard.tsx
git commit -m "feat(web): show TP/SL hover dots in positions table

Co-Authored-By: Claude <noreply@anthropic.com>"
```
