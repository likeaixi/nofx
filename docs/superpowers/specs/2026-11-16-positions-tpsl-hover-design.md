# Dashboard Positions 卡片 TP/SL 悬停显示设计

- 日期:2026-11-16
- 分支:`dev-allin`
- 状态:已与 owner 确认(数据源=交易所实时+decisions 兜底;触发=行内标记;内容=价格+距离%)

## 1. 背景与目标

Dashboard(`/dashboard`,TerminalDashboard)的 Positions 卡片只显示仓位、入场价、规模、PnL、收益率,看不到当前持仓的止盈/止损点位。用户需要在监控时快速确认"这单 TP/SL 挂在哪、离当前价还有多远"。

目标:每行持仓显示一个 TP/SL 状态标记,鼠标悬停弹出该持仓在交易所**当前真实挂单**的 TP/SL 触发价与距标记价的百分比。

## 2. 现状调查结论(实现依据)

- Positions 是 `TerminalDashboard.tsx:495-564` 的内联表格;行级已有点击选币(`:514-522`)与原生 `title`,无受控 hover
- `/api/positions`(`api/handler_order.go:161-179`)三层(前端类型 `web/src/types/trading.ts:38-49`、后端 normalize、交易所适配器)均**不含** TP/SL 字段
- 交易所实时挂单端点已存在:`GET /api/open-orders`(`api/server.go:431-433` 注册,`handler_order.go:444-473`),query `trader_id` + **必填** `symbol`,返回 `types.OpenOrder[]`(`trader/types/interface.go:107-118`),`type` 为 `STOP_MARKET`(SL)/`TAKE_PROFIT_MARKET`(TP),触发价 `stop_price`;全部 10 个交易所适配器均已实现 `GetOpenOrders`
- decisions 兜底数据 dashboard 已在轮询:`AppRoutes.tsx:324-346` → `GET /api/decisions/latest`,`DecisionAction.stop_loss/take_profit`(`store/decision.go:82-96`、前端镜像 `trading.ts:51-65`)已含 TP/SL 意图价
- tooltip 无第三方库;`MetricTooltip.tsx` 有 portal+fixed+视口钳制可参照;terminal 主题配色惯例:TP 绿 `#2E8B57`、SL 红 `#D6433A`(`DecisionCard.tsx:108-145`)
- `/api/orders`(查 DB)依赖后台 OrderSync 且 kucoin/indodax 无同步,新鲜度不确定,不采用

## 3. 方案选型

选定**方案 A:每行 SWR 后台拉取**(已否决:后端聚合进 `/api/positions`——轮询即打交易所且多观看者不共享缓存;hover 才拉+decisions 标记——标记可能失真)。

## 4. 设计

### 4.1 数据层

- `web/src/types/trading.ts` 新增 `OpenOrder` 接口,镜像后端 `types.OpenOrder` 的 JSON 输出(字段以 `trader/types/interface.go:107-118` 的 json tag 为准,实现时核实):`order_id`、`symbol`、`side`、`position_side`、`type`、`price`、`stop_price`、`quantity`、`status`
- `web/src/lib/api/data.ts` 新增:
  ```ts
  getOpenOrders(traderId: string, symbol: string): Promise<OpenOrder[]>
  // GET ${API_BASE}/open-orders?trader_id=...&symbol=...
  ```
- 零后端改动

### 4.2 新组件 `web/src/components/terminal/PositionTPSL.tsx`

每行持仓渲染一个,职责单一:拉取、解析、展示该 symbol 的 TP/SL 状态。

- **拉取**:内部 `useSWR`,key `` `open-orders-${traderId}-${symbol}` ``,fetcher `getOpenOrders`,`refreshInterval: 15000`(与 positions 轮询同频);SWR 自动去重,多行同 symbol 只发一次
- **解析**:仅取 `type === 'STOP_MARKET'`(SL)与 `type === 'TAKE_PROFIT_MARKET'`(TP);触发价取 `stop_price`;同向多张单全部保留、逐行列出;其余类型(LIMIT 等)忽略
- **标记(chip)**:两个小圆点——绿点=存在 ≥1 张 TP 单,红点=存在 ≥1 张 SL 单;两者皆无且无兜底数据时不渲染任何内容
- **tooltip 内容**:每张单一行,如 `TP 65,200 (+2.1%)` / `SL 61,800 (-1.4%)`;距离% = `(stop_price − mark_price) / mark_price × 100`,TP 绿、SL 红;数据来源为兜底时底部一行小字 `decision fallback`

### 4.3 兜底策略(两者结合)

优先级:交易所实时挂单 > decisions 意图价 > 不渲染。

- SWR 数据就绪 → 用实时挂单
- SWR error(交易所查询失败)→ 按 symbol 匹配 `decisions` 中最近一条 `stop_loss`/`take_profit` 非零值,tooltip 标注 `decision fallback`
- SWR loading 首帧 → 不渲染(短暂空白可接受)
- decisions 匹配时的 symbol 格式差异(如 `BTCUSDT` vs `BTC`)在实现时按 `market.Normalize` 等价规则处理,以实际数据格式核实为准

### 4.4 集成(TerminalDashboard.tsx)

- Positions 表头(`:498-506`)新增一列 **TP/SL**,行内渲染 `<PositionTPSL />`,props:`traderId`、`symbol`(原始 `p.symbol`)、`markPrice`、该 symbol 的兜底 `tp/sl`(由 `decisions` prop 预先按 symbol 归并)
- demo 模式(无 `traderId`,与现有 close 按钮 `traderId && !on` 判断一致):列头保留、单元格渲染为空(两种模式间列结构不变)
- 行点击选币交互不受影响(chip 仅响应 hover,无 click)

### 4.5 Tooltip 实现

新写轻量组件 `TerminalTooltip`,与 `PositionTPSL` 同文件(仅此一处使用,需要复用时再拆独立文件):`onMouseEnter/Leave` + `createPortal(document.body)` + `fixed` 定位 + 视口钳制。参照 `MetricTooltip.tsx` 的定位思路,不引入其 KaTeX 等依赖。表格位于页面右下角,tooltip 默认向左上弹出,溢出时翻转。约 60 行,不引第三方库。

## 5. 错误处理

- `getOpenOrders` 失败:SWR error,静默降级到 decisions 兜底;再无数据则 chip 不渲染,任何情况下不打断页面
- 后端 4xx/5xx / 交易所限流:同上路径
- `stop_price` 为 0 或缺失的单:跳过该单

## 6. 验证场景(手动)

1. 实盘持仓,交易所同时挂有 TP+SL → 双点亮,tooltip 价格与距离%正确
2. 仅挂 SL(如 signal-managed exit 无固定 TP)→ 仅红点
3. 同 symbol 多张 TP 单 → tooltip 逐行列出
4. 交易所手动撤单 → ≤15s 内标记熄灭
5. 人为使 open-orders 请求失败 → decisions 兜底显示并带 `decision fallback` 标注
6. tooltip 出现在视口边缘 → 翻转不溢出、不遮挡
7. demo 模式 → TP/SL 列为空,页面无报错
8. 行点击选币、close 按钮等既有交互不受影响

## 7. 不做的事(YAGNI)

- 不改后端任何代码;不新增/修改 API
- 不在 K 线上画 TP/SL 价格线(AdvancedChart 已有该能力,且当前未被路由使用)
- 不显示每张单的数量/状态明细(owner 已选"价格+距离%")
- 不引入第三方 tooltip 库
