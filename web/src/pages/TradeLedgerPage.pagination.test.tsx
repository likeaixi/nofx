import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router-dom'
import { SWRConfig } from 'swr'
import { TradeLedgerPage } from './TradeLedgerPage'
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('../lib/api', () => ({
  api: {
    getTraders: async () => [
      { trader_id: 'one', trader_name: 'One' },
      { trader_id: 'two', trader_name: 'Two' },
    ],
    getAccount: async () => ({}),
    getPositions: async () => [],
    getStatus: async () => ({ strategy_type: 'allincrypto' }),
    getLatestDecisions: async () => [],
    getPositionHistory: async (id: string) => ({
      positions: Array.from({ length: id === 'one' ? 45 : 2 }, (_, i) => ({
        id: i + 1,
        symbol: i % 3 === 0 ? 'BTCUSDT' : 'ETHUSDT',
        side: 'LONG',
        exit_time: 1700000000000 - i * 1000,
        entry_time: 1699990000000,
        entry_price: 100,
        exit_price: 110,
        quantity: 1,
        leverage: 10,
        realized_pnl: 10,
        fee: 1,
        close_reason: `close-${i + 1}`,
      })),
    }),
  },
}))
afterEach(cleanup)
function mount() {
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <MemoryRouter>
        <TradeLedgerPage />
      </MemoryRouter>
    </SWRConfig>
  )
}
async function closed() {
  return (
    await screen.findByRole('heading', { name: 'Closed positions · 45' })
  ).closest('section')!
}
it('paginates rows while keeping aggregate stats for the entire filtered set', async () => {
  mount()
  const section = await closed()
  const table = within(section)
  expect(table.getAllByRole('row')).toHaveLength(21)
  expect(table.getByText('close-1')).toBeVisible()
  fireEvent.click(table.getByRole('button', { name: 'Next' }))
  expect(table.getByText('close-21')).toBeVisible()
  expect(table.queryByText('close-1')).toBeNull()
  expect(table.getByText('Page 2 / 3')).toBeVisible()
  fireEvent.click(table.getByRole('button', { name: 'Last' }))
  expect(table.getAllByRole('row')).toHaveLength(6)
  expect(table.getByRole('button', { name: 'Next' })).toBeDisabled()
  expect(
    screen.getByRole('heading', { name: 'Closed positions · 45' })
  ).toBeVisible()
  fireEvent.change(table.getByLabelText('Rows per page'), {
    target: { value: '50' },
  })
  await waitFor(() => expect(table.getAllByRole('row')).toHaveLength(46))
  expect(table.getByText('Page 1 / 1')).toBeVisible()
})
it('resets to the first page on filter or trader changes', async () => {
  mount()
  const section = await closed()
  const table = within(section)
  fireEvent.click(table.getByRole('button', { name: 'Last' }))
  fireEvent.change(screen.getByLabelText('Search symbol'), {
    target: { value: 'BTC' },
  })
  await waitFor(() => expect(table.getAllByRole('row')).toHaveLength(16))
  expect(table.getByText('Page 1 / 1')).toBeVisible()
  fireEvent.change(screen.getByLabelText('Trader'), {
    target: { value: 'two' },
  })
  const second = (
    await screen.findByRole('heading', { name: 'Closed positions · 2' })
  ).closest('section')!
  expect(within(second).getAllByRole('row')).toHaveLength(3)
  expect(within(second).getByText('Page 1 / 1')).toBeVisible()
})
