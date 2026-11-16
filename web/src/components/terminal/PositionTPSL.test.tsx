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
