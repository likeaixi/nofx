import { useState } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import { useLanguage } from '../../contexts/LanguageContext'
import { PositionHistory } from './PositionHistory'
import { EquityChart } from '../charts/EquityChart'
import { GridRiskPanel } from '../strategy/GridRiskPanel'

type OrderRow = {
  id: number
  exchange_order_id: string
  symbol: string
  side: string
  type: string
  status: string
  quantity: number
  filled_quantity: number
  avg_fill_price: number
  commission: number
  commission_asset: string
  created_at: number
}
type FillRow = {
  id: number
  exchange_trade_id: string
  price: number
  quantity: number
  commission: number
  commission_asset: string
  realized_pnl: number
  created_at: number
}
const number = (v: number) =>
  Number.isFinite(v)
    ? v.toLocaleString(undefined, { maximumFractionDigits: 8 })
    : '—'
const date = (v: number) => (v ? new Date(v).toLocaleString() : '—')

function Fills({ traderId, orderId }: { traderId: string; orderId: number }) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const { data, error, isLoading, mutate } = useSWR<FillRow[]>(
    ['order-fills', traderId, orderId],
    () => api.getTraderOrderFills(traderId, orderId)
  )
  if (isLoading)
    return <p role="status">{zh ? '正在读取成交…' : 'Loading fills…'}</p>
  if (error)
    return (
      <button onClick={() => mutate()}>
        {zh ? '成交读取失败，点击重试' : 'Could not load fills. Retry'}
      </button>
    )
  if (!data?.length)
    return (
      <p>{zh ? '暂无已同步的成交记录' : 'No synced fills for this order.'}</p>
    )
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr>
            {(zh
              ? ['成交号', '时间', '价格', '数量', '手续费', '已实现盈亏']
              : ['Fill ID', 'Time', 'Price', 'Quantity', 'Fee', 'Realized P/L']
            ).map((x) => (
              <th key={x} className="p-2 text-left">
                {x}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {data.map((f) => (
            <tr key={f.id}>
              <td className="p-2">{f.exchange_trade_id}</td>
              <td>{date(f.created_at)}</td>
              <td>{number(f.price)}</td>
              <td>{number(f.quantity)}</td>
              <td>
                {number(f.commission)} {f.commission_asset}
              </td>
              <td>{number(f.realized_pnl)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function OrderHistory({ traderId }: { traderId: string }) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const [page, setPage] = useState(0)
  const [expanded, setExpanded] = useState<number | null>(null)
  const { data, error, isLoading, mutate } = useSWR<OrderRow[]>(
    ['trader-orders', traderId, page],
    () => api.getTraderOrders(traderId, page * 50),
    { revalidateOnFocus: false }
  )
  return (
    <div className="space-y-4">
      <div className="flex justify-between gap-3">
        <p className="text-sm">
          {zh
            ? '本 trader 已同步的订单；展开订单查看逐笔成交。'
            : 'Synced orders for this trader. Expand an order to inspect fills.'}
        </p>
        <button onClick={() => mutate()}>{zh ? '刷新' : 'Refresh'}</button>
      </div>
      {isLoading ? (
        <p role="status">{zh ? '正在读取订单…' : 'Loading orders…'}</p>
      ) : error ? (
        <p role="alert">
          {zh ? '订单读取失败，请重试' : 'Could not load orders. Please retry.'}
        </p>
      ) : (
        <>
          {!data?.length && (
            <p>{zh ? '暂无已同步订单' : 'No synced orders.'}</p>
          )}
          {data?.map((o) => (
            <div key={o.id} className="border rounded p-3">
              <button
                className="w-full text-left flex flex-wrap gap-x-5 gap-y-2 text-sm"
                aria-expanded={expanded === o.id}
                onClick={() => setExpanded(expanded === o.id ? null : o.id)}
              >
                <strong>
                  {o.symbol} · {o.side}
                </strong>
                <span>
                  {o.type} · {o.status}
                </span>
                <span>{date(o.created_at)}</span>
                <span>
                  {zh ? '已成交/委托' : 'Filled/ordered'}{' '}
                  {number(o.filled_quantity)}/{number(o.quantity)}
                </span>
                <span>
                  {zh ? '均价' : 'Avg'} {number(o.avg_fill_price)}
                </span>
                <span>
                  {zh ? '手续费' : 'Fee'} {number(o.commission)}{' '}
                  {o.commission_asset}
                </span>
                <span>
                  {zh ? '订单号' : 'Order'} {o.exchange_order_id}
                </span>
                <span>{expanded === o.id ? '−' : '+'}</span>
              </button>
              {expanded === o.id && (
                <div className="mt-3 border-t pt-3">
                  <Fills traderId={traderId} orderId={o.id} />
                </div>
              )}
            </div>
          ))}
        </>
      )}
      <div className="flex gap-4 items-center">
        <button
          disabled={page === 0 || isLoading}
          onClick={() => {
            setExpanded(null)
            setPage((p) => p - 1)
          }}
        >
          {zh ? '上一页' : 'Previous'}
        </button>
        <span>{page + 1}</span>
        <button
          disabled={isLoading || !!error || (data?.length || 0) < 50}
          onClick={() => {
            setExpanded(null)
            setPage((p) => p + 1)
          }}
        >
          {zh ? '下一页' : 'Next'}
        </button>
      </div>
    </div>
  )
}

export function TraderDetails({
  traderId,
  strategyType,
}: {
  traderId: string
  strategyType?: string
}) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const [tab, setTab] = useState('history')
  const tabs = [
    ['history', zh ? '完整交易历史' : 'Trade history'],
    ['equity', zh ? '独立收益图' : 'Equity curve'],
    ['orders', zh ? '订单与成交详情' : 'Orders & fills'],
    ...(strategyType === 'grid_trading'
      ? [['risk', zh ? '网格策略风控' : 'Grid risk']]
      : []),
  ]
  return (
    <section
      className="border-t px-4 py-6 md:px-6 space-y-5"
      aria-label={zh ? '交易员详情' : 'Trader details'}
    >
      <div role="tablist" className="flex gap-2 flex-wrap">
        {tabs.map(([id, label]) => (
          <button
            key={id}
            role="tab"
            aria-selected={tab === id}
            className={`px-4 py-2 border rounded ${tab === id ? 'font-bold bg-nofx-bg-lighter' : ''}`}
            onClick={() => setTab(id)}
          >
            {label}
          </button>
        ))}
      </div>
      <div role="tabpanel">
        {tab === 'history' && (
          <PositionHistory key={traderId} traderId={traderId} />
        )}
        {tab === 'equity' && (
          <div>
            <p className="text-sm mb-3">
              {zh
                ? '按此 trader 的账户净值绘制；共用账户时包含该账户其他交易的影响。'
                : 'Based on this trader’s account equity; shared accounts include the effects of other trades.'}
            </p>
            <EquityChart key={traderId} traderId={traderId} embedded />
          </div>
        )}
        {tab === 'orders' && (
          <OrderHistory key={traderId} traderId={traderId} />
        )}
        {tab === 'risk' && strategyType === 'grid_trading' && (
          <GridRiskPanel
            key={traderId}
            traderId={traderId}
            language={language}
          />
        )}
      </div>
    </section>
  )
}
