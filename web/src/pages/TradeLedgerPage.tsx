import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import useSWR from 'swr'
import { api } from '../lib/api'
import { useLanguage } from '../contexts/LanguageContext'
import {
  mergeLedgerRows,
  filterLedger,
  ledgerStats,
  ledgerCSV,
  netPnl,
} from '../lib/tradeLedger'
import type { HistoricalPosition, TraderInfo } from '../types'
import { ROUTES } from '../router/paths'

const num = (v: number | undefined, digits = 2) =>
  v != null && Number.isFinite(v)
    ? v.toLocaleString(undefined, { maximumFractionDigits: digits })
    : '—'
const time = (v: number | string | undefined) =>
  v && Number.isFinite(new Date(v).getTime())
    ? new Date(v).toLocaleString()
    : '—'
const color = (n: number) => (n >= 0 ? 'ledger-positive' : 'ledger-negative')
function Stat({
  title,
  value,
  note,
  tone,
}: {
  title: string
  value: string
  note?: string
  tone?: string
}) {
  return (
    <div className="ledger-stat">
      <span>{title}</span>
      <strong className={tone}>{value}</strong>
      <small>{note || '\u00a0'}</small>
    </div>
  )
}
function Section({
  title,
  note,
  children,
}: {
  title: string
  note?: string
  children: React.ReactNode
}) {
  return (
    <section className="ledger-section">
      <header>
        <h2>{title}</h2>
        <span>{note}</span>
      </header>
      {children}
    </section>
  )
}
export function TradeLedgerPage() {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const [params, setParams] = useSearchParams()
  const {
    data: traders,
    error,
    mutate,
  } = useSWR('traders-dashboard', () => api.getTraders(true), {
    refreshInterval: 10000,
  })
  const id = params.get('trader')
  const trader = id ? traders?.find((t) => t.trader_id === id) : traders?.[0]
  const traderSelector = (
    <label className="ledger-trader-select">
      Trader{' '}
      <select
        value={trader?.trader_id || ''}
        onChange={(e) => setParams({ trader: e.target.value })}
      >
        {!trader && (
          <option value="">{zh ? '请选择交易员' : 'Select trader'}</option>
        )}
        {traders?.map((t) => (
          <option key={t.trader_id} value={t.trader_id}>
            {t.trader_name} · {t.ai_model} ·{' '}
            {t.is_running
              ? zh
                ? '运行中'
                : 'Running'
              : zh
                ? '已停止'
                : 'Stopped'}
          </option>
        ))}
      </select>
    </label>
  )
  return (
    <div className="nofx-ledger-page">
      <div className="ledger-title">
        <h1>
          {zh ? '交易明细' : 'Trade Ledger'} {zh && <small>Trade Ledger</small>}
        </h1>
        <div>
          <Link to={ROUTES.strategy}>{zh ? '← 策略' : '← Strategies'}</Link>
          <Link to={ROUTES.dashboard}>Dashboard</Link>
        </div>
      </div>
      {!trader && <div className="ledger-filters">{traderSelector}</div>}
      {error ? (
        <button onClick={() => mutate()}>
          {zh ? '交易员读取失败，重试' : 'Could not load traders. Retry'}
        </button>
      ) : !traders ? (
        <p>{zh ? '正在读取…' : 'Loading…'}</p>
      ) : !traders.length ? (
        <p>{zh ? '请先创建交易员。' : 'Create a trader first.'}</p>
      ) : trader ? (
        <Ledger
          key={trader.trader_id}
          trader={trader}
          traderSelector={traderSelector}
        />
      ) : (
        <p>
          {zh
            ? '未找到指定交易员，请重新选择。'
            : 'Trader not found. Select another trader.'}
        </p>
      )}
    </div>
  )
}
function Ledger({
  trader,
  traderSelector,
}: {
  trader: TraderInfo
  traderSelector: React.ReactNode
}) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const tr = (cn: string, en: string) => (zh ? cn : en)
  const id = trader.trader_id
  const [auto, setAuto] = useState(true),
    [symbol, setSymbol] = useState(''),
    [side, setSide] = useState('all'),
    [outcome, setOutcome] = useState('all'),
    [range, setRange] = useState('all'),
    [hide, setHide] = useState(false)
  const [older, setOlder] = useState<HistoricalPosition[]>([]),
    [offset, setOffset] = useState(500),
    [more, setMore] = useState(true),
    [busy, setBusy] = useState(false),
    [moreError, setMoreError] = useState('')
  const options = {
    refreshInterval: auto ? 10000 : 0,
    revalidateOnFocus: false,
    shouldRetryOnError: false,
  }
  const account = useSWR(
    ['ledger-account', id],
    () => api.getAccount(id, true),
    options
  )
  const positions = useSWR(
    ['ledger-positions', id],
    () => api.getPositions(id, true),
    options
  )
  const history = useSWR(
    ['ledger-history', id],
    () => api.getPositionHistory(id, 500, true),
    options
  )
  const status = useSWR(
    ['ledger-status', id],
    () => api.getStatus(id, true),
    options
  )
  const decisions = useSWR(
    ['ledger-decisions', id],
    () => api.getLatestDecisions(id, 1, true),
    options
  )
  const [historyWindow, setHistoryWindow] = useState<HistoricalPosition[]>([])
  useEffect(() => {
    const page = history.data?.positions
    if (page) setHistoryWindow((prev) => mergeLedgerRows(prev, page))
  }, [history.data])
  const rows = useMemo(() => {
    const map = new Map<number, HistoricalPosition>()
    older.forEach((p) => map.set(p.id, p))
    historyWindow.forEach((p) => map.set(p.id, p))
    history.data?.positions.forEach((p) => map.set(p.id, p))
    return [...map.values()].sort(
      (a, b) => b.exit_time - a.exit_time || b.id - a.id
    )
  }, [older, historyWindow, history.data])
  const start = status.data?.start_time
    ? new Date(status.data.start_time).getTime()
    : NaN
  const since =
    range === 'run'
      ? Number.isFinite(start) && start > 0
        ? start
        : Infinity
      : range === '24h'
        ? Date.now() - 86400000
        : range === '7d'
          ? Date.now() - 7 * 86400000
          : 0
  const filtered = filterLedger(rows, {
    symbol,
    side,
    outcome,
    since,
    hideReconciled: hide,
  })
  const stats = ledgerStats(filtered)
  const current = positions.data?.filter(
    (p) =>
      p.symbol.toUpperCase().includes(symbol.trim().toUpperCase()) &&
      (side === 'all' || p.side.toLowerCase() === side)
  )
  const last = decisions.data?.[0]
  const isCopy = status.data?.strategy_type === 'allincrypto'
  const candidates = last?.candidate_coins || []
  async function loadMore() {
    setBusy(true)
    setMoreError('')
    try {
      const data = await api.getPositionHistory(id, 500, true, offset)
      setOlder((prev) => [...prev, ...data.positions])
      setOffset((n) => n + data.positions.length)
      setMore(data.positions.length === 500)
    } catch {
      setMoreError(
        tr('读取更早记录失败，请重试', 'Could not load older records. Retry.')
      )
    } finally {
      setBusy(false)
    }
  }
  async function refresh() {
    setOlder([])
    setHistoryWindow([])
    setOffset(500)
    setMore(true)
    setMoreError('')
    await Promise.allSettled([
      account.mutate(),
      positions.mutate(),
      history.mutate(),
      status.mutate(),
      decisions.mutate(),
    ])
  }
  function download() {
    const url = URL.createObjectURL(
      new Blob([ledgerCSV(filtered)], { type: 'text/csv;charset=utf-8' })
    )
    const a = document.createElement('a')
    a.href = url
    a.download = `trades-${id}-${new Date().toISOString().slice(0, 10)}.csv`
    a.click()
    URL.revokeObjectURL(url)
  }
  const missing = tr(
    '初始止损/风险未保存，无法计算 R',
    'Initial stop/risk not stored; R unavailable'
  )
  return (
    <>
      <div className="ledger-filters">
        {traderSelector}
        <label className="ledger-symbol-search">
          {tr('搜索币种', 'Search symbol')}
          <input
            value={symbol}
            onChange={(e) => setSymbol(e.target.value)}
            placeholder="BTC / ETH"
          />
        </label>
        <label>
          {tr('方向', 'Side')}
          <select value={side} onChange={(e) => setSide(e.target.value)}>
            <option value="all">{tr('全部', 'All')}</option>
            <option value="long">LONG</option>
            <option value="short">SHORT</option>
          </select>
        </label>
        <label>
          {tr('平仓结果（扣费）', 'Net outcome')}
          <select value={outcome} onChange={(e) => setOutcome(e.target.value)}>
            <option value="all">{tr('全部', 'All')}</option>
            <option value="profit">{tr('盈利', 'Profit')}</option>
            <option value="loss">{tr('亏损', 'Loss')}</option>
            <option value="flat">{tr('持平', 'Flat')}</option>
          </select>
        </label>
        <label>
          {tr('平仓时间范围', 'Close time')}
          <select value={range} onChange={(e) => setRange(e.target.value)}>
            <option value="all">{tr('全部已加载', 'All loaded')}</option>
            <option value="24h">24h</option>
            <option value="7d">7d</option>
            <option
              value="run"
              disabled={!(Number.isFinite(start) && start > 0)}
            >
              {tr('本次运行', 'Current run')}
            </option>
          </select>
        </label>
        <label className="ledger-check">
          <input
            type="checkbox"
            checked={hide}
            onChange={(e) => setHide(e.target.checked)}
          />
          {tr('隐藏同步/对账记录', 'Hide sync/reconciliation')}
        </label>
        <label className="ledger-check">
          <input
            type="checkbox"
            checked={auto}
            onChange={(e) => setAuto(e.target.checked)}
          />
          {tr('自动刷新 10s', 'Refresh every 10s')}
        </label>
        <button disabled={busy} onClick={() => void refresh()}>
          {tr('刷新', 'Refresh')}
        </button>
        <button
          disabled={!filtered.length || !!history.error}
          onClick={download}
        >
          {tr('导出 CSV', 'Export CSV')}
        </button>
      </div>
      {(account.error ||
        positions.error ||
        history.error ||
        status.error ||
        decisions.error) && (
        <p role="alert" className="ledger-warning">
          {tr(
            '部分数据读取失败；请刷新重试，已有数据可能未更新。',
            'Some data could not be refreshed. Retry; cached data may be stale.'
          )}
        </p>
      )}
      <div className="ledger-stats">
        <Stat
          title={tr('当前权益', 'Current equity')}
          value={account.data ? `$${num(account.data.total_equity)}` : '—'}
          note={tr('交易所账户实时值', 'Live account value')}
        />
        <Stat
          title={tr('账户盈亏', 'Account P/L')}
          value={account.data ? `$${num(account.data.total_pnl)}` : '—'}
          note={tr(
            '账户口径，不随列表筛选',
            'Account scope, unaffected by filters'
          )}
          tone={color(account.data?.total_pnl || 0)}
        />
        <Stat
          title={tr('未平仓', 'Open positions')}
          value={current ? String(current.length) : '—'}
          note={tr('按币种 / 方向筛选', 'Symbol / side filter')}
        />
        <Stat
          title={tr('已平仓', 'Closed positions')}
          value={history.data ? String(filtered.length) : '—'}
          note={tr('当前筛选', 'Current filter')}
        />
        <Stat
          title={tr('净胜率', 'Net win rate')}
          value={stats.winRate == null ? '—' : `${num(stats.winRate, 1)}%`}
          note={`${tr('胜 / 负', 'Wins / losses')} ${stats.wins} / ${stats.losses}`}
        />
        <Stat
          title={tr('净盈亏（扣费）', 'Net P/L')}
          value={history.data ? `$${num(stats.net)}` : '—'}
          note={`${tr('毛盈亏', 'Gross')} $${num(stats.gross)}`}
          tone={color(stats.net)}
        />
        <Stat
          title={tr('手续费', 'Fees')}
          value={history.data ? `$${num(stats.fees)}` : '—'}
          note={tr('当前筛选', 'Current filter')}
        />
        <Stat
          title={tr('盈利因子（净）', 'Net profit factor')}
          value={stats.profitFactor == null ? '—' : num(stats.profitFactor)}
          note={tr('总盈利 / 总亏损绝对值', 'Profit / absolute loss')}
        />
      </div>
      <p className="ledger-note">
        {tr(
          `已加载 ${rows.length} 笔平仓记录；筛选、统计和导出使用同一批已加载记录。当前权益为账户实时值。`,
          `Loaded ${rows.length} closed positions; filters, statistics and export use this loaded set. Equity is the live account value.`
        )}{' '}
        {range === 'run' &&
          `${tr('本次运行开始', 'Run started')}: ${time(start)}`}
      </p>
      <Section
        title={tr('AI 计划单', 'AI planned orders')}
        note={tr('仅展示可核实数据', 'Verified data only')}
      >
        <p className="ledger-empty">
          {isCopy
            ? tr(
                '跟单由源成交触发，不生成 AI 计划单。',
                'Copy trading is triggered by source fills, without AI planned orders.'
              )
            : tr(
                '当前 trader 接口未提供独立的 AI 计划单记录。',
                'This trader does not expose independent AI planned-order records.'
              )}
        </p>
      </Section>
      <Section
        title={tr('最近决策', 'Latest decision')}
        note={last ? time(last.timestamp) : undefined}
      >
        <div className="ledger-decision">
          {isCopy
            ? tr(
                '固定规则跟单；以源信号记录与订单结果为准。',
                'Rule-based copy trading; source signals and order outcomes are authoritative.'
              )
            : last?.decisions?.length
              ? last.decisions.map((d, i) => (
                  <span key={i}>
                    {d.action} {d.symbol}
                    {d.skipped ? ` · ${tr('跳过', 'Skipped')}` : ''}
                  </span>
                ))
              : tr('暂无决策数据', 'No decision data')}
          <small>
            {tr(
              '下一次决策时间不由此页面推算；以实际运行记录为准。',
              'Next decision time is not inferred; actual runtime records are authoritative.'
            )}
          </small>
        </div>
      </Section>
      <Section
        title={`${tr('筛选币池', 'Candidate pool')} · ${candidates.length}`}
        note={tr('最近一轮决策记录', 'Latest decision record')}
      >
        {candidates.length ? (
          <div className="ledger-scroll">
            <table>
              <thead>
                <tr>
                  <th>#</th>
                  <th>{tr('币种', 'Symbol')}</th>
                  <th>{tr('来源', 'Source')}</th>
                </tr>
              </thead>
              <tbody>
                {candidates.map((c, i) => (
                  <tr key={`${c}-${i}`}>
                    <td>{i + 1}</td>
                    <td>
                      <strong>{c}</strong>
                    </td>
                    <td>{tr('决策候选', 'Decision candidate')}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="ledger-empty">
            {isCopy
              ? tr(
                  '跟随来源的新成交，不使用 AI 筛选币池。',
                  'Follows new source fills, without an AI candidate pool.'
                )
              : tr('暂无已记录候选币种', 'No recorded candidates')}
          </p>
        )}
      </Section>
      <Section
        title={`${tr('当前持仓', 'Current positions')} · ${current?.length ?? '—'}`}
        note={tr(
          '实时行情与当前交易所止损；不是初始止损',
          'Live marks and current exchange stops, not initial stops'
        )}
      >
        <div className="ledger-scroll">
          <table>
            <thead>
              <tr>
                {[
                  tr('币种 / 方向', 'Symbol / side'),
                  tr('开仓价', 'Entry'),
                  tr('标记价', 'Mark'),
                  tr('数量', 'Quantity'),
                  tr('名义金额', 'Notional'),
                  tr('杠杆', 'Leverage'),
                  tr('当前止损', 'Current stops'),
                  tr('浮盈', 'Unrealized P/L'),
                ].map((x) => (
                  <th key={x}>{x}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {current?.map((p) => (
                <tr key={`${p.symbol}-${p.side}`}>
                  <td>
                    <strong>{p.symbol}</strong>
                    <small
                      className={`ledger-tag ${p.side.toLowerCase() === 'long' ? 'ledger-positive' : 'ledger-negative'}`}
                    >
                      {p.side.toUpperCase()}
                    </small>
                  </td>
                  <td>{num(p.entry_price, 8)}</td>
                  <td>{num(p.mark_price, 8)}</td>
                  <td>{num(p.quantity, 8)}</td>
                  <td>${num(Math.abs(p.quantity * p.mark_price))}</td>
                  <td>{num(p.leverage)}×</td>
                  <td>
                    {p.stop_loss_status === 'unknown'
                      ? tr('未能读取', 'Unavailable')
                      : p.stop_loss_orders?.length
                        ? p.stop_loss_orders
                            .map((o) => num(o.stop_price, 8))
                            .join(' / ')
                        : '—'}
                  </td>
                  <td className={color(p.unrealized_pnl)}>
                    ${num(p.unrealized_pnl)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {!current?.length && (
          <p className="ledger-empty">
            {positions.isLoading
              ? tr('正在读取持仓…', 'Loading positions…')
              : positions.error
                ? tr('持仓读取失败', 'Could not load positions')
                : tr('当前筛选无持仓', 'No matching positions')}
          </p>
        )}
      </Section>
      <Section
        title={`${tr('平仓记录', 'Closed positions')} · ${filtered.length}`}
        note={tr(
          '按平仓时间倒序；已保存记录重启后保留',
          'Newest first; stored records survive restarts'
        )}
      >
        <div className="ledger-scroll ledger-closed-positions">
          <table>
            <thead>
              <tr>
                {[
                  tr('平仓时间', 'Closed at'),
                  tr('币种 / 方向', 'Symbol / side'),
                  tr('开仓价', 'Entry'),
                  tr('平仓价', 'Exit'),
                  tr('数量', 'Quantity'),
                  tr('名义金额', 'Notional'),
                  tr('杠杆', 'Leverage'),
                  tr('初始止损', 'Initial stop'),
                  tr('风险 $', 'Risk $'),
                  tr('毛盈亏', 'Gross P/L'),
                  tr('手续费', 'Fees'),
                  tr('净盈亏', 'Net P/L'),
                  tr('净 %（初始保证金）', 'Net % (entry margin)'),
                  'R',
                  tr('平仓原因', 'Close reason'),
                  tr('时长', 'Duration'),
                ].map((x) => (
                  <th key={x}>{x}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {filtered.map((p) => {
                const qty = p.entry_quantity || p.quantity
                const margin = (qty * p.entry_price) / p.leverage
                return (
                  <tr key={p.id}>
                    <td>{time(p.exit_time)}</td>
                    <td>
                      <strong>{p.symbol}</strong>
                      <small
                        className={`ledger-tag ${p.side.toLowerCase() === 'long' ? 'ledger-positive' : 'ledger-negative'}`}
                      >
                        {p.side.toUpperCase()}
                      </small>
                    </td>
                    <td>{num(p.entry_price, 8)}</td>
                    <td>{num(p.exit_price, 8)}</td>
                    <td>{num(qty, 8)}</td>
                    <td>${num(qty * p.entry_price)}</td>
                    <td>{num(p.leverage)}×</td>
                    <td title={missing}>—</td>
                    <td title={missing}>—</td>
                    <td className={color(p.realized_pnl)}>
                      ${num(p.realized_pnl)}
                    </td>
                    <td>${num(p.fee)}</td>
                    <td className={color(netPnl(p))}>${num(netPnl(p))}</td>
                    <td className={color(netPnl(p))}>
                      {margin > 0 ? `${num((netPnl(p) / margin) * 100)}%` : '—'}
                    </td>
                    <td title={missing}>—</td>
                    <td>{p.close_reason || '—'}</td>
                    <td>
                      {p.exit_time >= p.entry_time && p.entry_time > 0
                        ? `${num((p.exit_time - p.entry_time) / 60000, 0)}m`
                        : '—'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
        {!filtered.length && (
          <p className="ledger-empty">
            {history.isLoading
              ? tr('正在读取…', 'Loading…')
              : history.error
                ? tr('历史读取失败', 'Could not load history')
                : tr('当前筛选无平仓记录', 'No matching closed positions')}
          </p>
        )}
        <footer>
          <span>{missing}</span>
          {more && history.data?.positions.length === 500 && (
            <button disabled={busy} onClick={() => void loadMore()}>
              {busy ? '…' : tr('加载更早记录', 'Load older records')}
            </button>
          )}
          {moreError && <span role="alert">{moreError}</span>}
        </footer>
      </Section>
    </>
  )
}
