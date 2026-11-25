import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { StrategyStudioPage } from './StrategyStudioPage'
import { api } from '../lib/api'
import type { Strategy } from '../types'
vi.mock('../contexts/AuthContext', () => ({
  useAuth: () => ({ token: 'test' }),
}))
vi.mock('../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }))
vi.mock('../components/common/DeepVoidBackground', () => ({
  DeepVoidBackground: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}))
vi.mock('../components/strategy/CopyStrategyEditor', () => ({
  CopyStrategyEditor: ({ strategy }: { strategy: Strategy }) => (
    <div data-testid="copy-editor">{JSON.stringify(strategy)}</div>
  ),
}))
vi.mock('../lib/api', () => ({
  api: {
    getStrategies: vi.fn(),
    getTraders: vi.fn(),
    getSymbols: vi.fn(),
    updateStrategyPublication: vi.fn(),
  },
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
it('keeps a delayed publication response attached to its original strategy', async () => {
  const strategies = ['A', 'B'].map((id) => ({
    id,
    name: `Strategy ${id}`,
    description: '',
    is_active: id === 'A',
    is_default: false,
    is_public: false,
    config_visible: false,
    config: { strategy_type: 'allincrypto' },
    created_at: '',
    updated_at: '',
  })) as Strategy[]
  vi.mocked(api.getStrategies).mockResolvedValue(strategies)
  vi.mocked(api.getTraders).mockResolvedValue([])
  vi.mocked(api.getSymbols).mockResolvedValue({ symbols: [] } as Awaited<
    ReturnType<typeof api.getSymbols>
  >)
  let finish!: () => void
  vi.mocked(api.updateStrategyPublication).mockImplementation(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve
      })
  )
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <StrategyStudioPage />
    </SWRConfig>
  )
  fireEvent.click(await screen.findByLabelText('List in strategy market'))
  fireEvent.click(screen.getByText('Save publication'))
  await waitFor(() =>
    expect(api.updateStrategyPublication).toHaveBeenCalledWith('A', true, false)
  )
  fireEvent.click(screen.getByText('Strategy B'))
  await act(async () => {
    finish()
  })
  expect(
    JSON.parse(screen.getByTestId('copy-editor').textContent!)
  ).toMatchObject({
    id: 'B',
    is_public: false,
    config: { strategy_type: 'allincrypto' },
  })
})
