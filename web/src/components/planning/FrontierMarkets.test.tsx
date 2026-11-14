import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { FrontierMarkets } from './FrontierMarkets'
const mocks = vi.hoisted(() => ({ api: vi.fn() }))
vi.mock('../../lib/planning', () => ({ planningAPI: mocks.api }))
afterEach(() => {
  cleanup()
  mocks.api.mockReset()
})
const market = (
  symbol: string,
  volume: number,
  category = 'crypto',
  execution = true
) => ({
  symbol,
  base: symbol,
  volume_24h: volume,
  category,
  price: '100',
  execution_supported: execution,
})
it('offers ranked public stock shortcuts alongside crypto markets', async () => {
  mocks.api.mockResolvedValue({
    venue: 'binance',
    markets: [market('BTCUSDT', 100), market('TSLAUSDT', 500, 'stocks')],
    sources: ['fixture'],
    fetched_at: new Date().toISOString(),
  })
  const select = vi.fn()
  render(
    <FrontierMarkets
      venue="binance"
      selected="BTCUSDT"
      disabled={false}
      onSelect={select}
    />
  )
  fireEvent.click(screen.getByRole('button', { name: 'Stocks' }))
  const stock = await screen.findByRole('button', { name: /TSLAUSDT/ })
  fireEvent.click(stock)
  expect(select).toHaveBeenCalledWith('TSLAUSDT')
  expect(screen.queryByRole('button', { name: /BTCUSDT/ })).toBeNull()
  expect(mocks.api).toHaveBeenCalledWith('markets?venue=binance')
})
it('retains native HIP-3 prefixes and does not claim all builders support execution', async () => {
  mocks.api.mockResolvedValue({
    venue: 'hyperliquid',
    markets: [
      market('xyz:GOOGL', 100, 'stocks'),
      market('cash:TSLA', 200, 'stocks', false),
    ],
    sources: ['fixture'],
    fetched_at: new Date().toISOString(),
  })
  const select = vi.fn()
  render(
    <FrontierMarkets
      venue="hyperliquid"
      selected="xyz:GOOGL"
      disabled={false}
      onSelect={select}
    />
  )
  const stock = await screen.findByRole('button', { name: /cash:TSLA/ })
  expect(stock).toHaveTextContent('Chart only')
  fireEvent.click(stock)
  expect(select).toHaveBeenCalledWith('cash:TSLA')
  fireEvent.change(screen.getByLabelText('Search exchange markets'), {
    target: { value: 'googl' },
  })
  expect(screen.getByRole('button', { name: /xyz:GOOGL/ })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.queryByRole('button', { name: /cash:TSLA/ })).toBeNull()
})
