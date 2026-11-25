import {
  render,
  screen,
  fireEvent,
  waitFor,
  cleanup,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { TraderDetails } from './TraderDetails'
const mocks = vi.hoisted(() => ({ orders: vi.fn(), fills: vi.fn() }))
vi.mock('../../lib/api', () => ({
  api: { getTraderOrders: mocks.orders, getTraderOrderFills: mocks.fills },
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'zh' }),
}))
vi.mock('./PositionHistory', () => ({
  PositionHistory: ({ traderId }: { traderId: string }) => (
    <div>history-{traderId}</div>
  ),
}))
vi.mock('../charts/EquityChart', () => ({
  EquityChart: ({ traderId }: { traderId: string }) => (
    <div>equity-{traderId}</div>
  ),
}))
vi.mock('../strategy/GridRiskPanel', () => ({
  GridRiskPanel: ({ traderId }: { traderId: string }) => (
    <div>risk-{traderId}</div>
  ),
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
it('loads selected trader order fills on demand and exposes grid risk only for grid strategy', async () => {
  mocks.orders.mockResolvedValue([
    {
      id: 7,
      symbol: 'BTCUSDT',
      side: 'BUY',
      status: 'FILLED',
      type: 'MARKET',
      exchange_order_id: 'native-7',
      created_at: 1,
      quantity: 2,
      filled_quantity: 2,
      avg_fill_price: 100,
      commission: 0.1,
      commission_asset: 'USDT',
    },
  ])
  mocks.fills.mockResolvedValue([
    {
      id: 9,
      exchange_trade_id: 'fill-9',
      created_at: 1,
      price: 100,
      quantity: 2,
      commission: 0.1,
      commission_asset: 'USDT',
      realized_pnl: 0,
    },
  ])
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <TraderDetails traderId="mine" strategyType="allincrypto" />
    </SWRConfig>
  )
  expect(screen.queryByRole('tab', { name: '网格策略风控' })).toBeNull()
  expect(mocks.orders).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('tab', { name: '独立收益图' }))
  expect(screen.getByText('equity-mine')).toBeTruthy()
  fireEvent.click(screen.getByRole('tab', { name: '订单与成交详情' }))
  await waitFor(() => expect(mocks.orders).toHaveBeenCalledWith('mine', 0))
  fireEvent.click(await screen.findByRole('button', { name: /BTCUSDT/ }))
  await waitFor(() => expect(mocks.fills).toHaveBeenCalledWith('mine', 7))
  expect(await screen.findByText('fill-9')).toBeTruthy()
})
it('renders the grid panel when a grid trader is selected', () => {
  render(<TraderDetails traderId="grid" strategyType="grid_trading" />)
  fireEvent.click(screen.getByRole('tab', { name: '网格策略风控' }))
  expect(screen.getByText('risk-grid')).toBeTruthy()
})
