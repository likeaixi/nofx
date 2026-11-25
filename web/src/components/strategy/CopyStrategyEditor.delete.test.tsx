import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { CopyStrategyEditor } from './CopyStrategyEditor'
import { confirmToast } from '../../lib/notify'
import type { Strategy } from '../../types'
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ token: 'test-token' }),
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }))
vi.mock('../../lib/notify', () => ({ confirmToast: vi.fn() }))
const strategy = {
  id: 'copy-one',
  name: 'My Copy',
  is_default: false,
  is_active: false,
  config: { strategy_type: 'allincrypto' },
} as Strategy
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})
it('deletes only the confirmed strategy and refreshes the list', async () => {
  vi.mocked(confirmToast).mockResolvedValue(true)
  const fetcher = vi.fn().mockResolvedValue({ ok: true })
  vi.stubGlobal('fetch', fetcher)
  const removed = vi.fn().mockResolvedValue(undefined)
  render(
    <CopyStrategyEditor
      strategy={strategy}
      onSaved={vi.fn()}
      onDeleted={removed}
    />
  )
  fireEvent.click(screen.getByText('Delete strategy'))
  await waitFor(() => expect(removed).toHaveBeenCalledTimes(1))
  expect(fetcher).toHaveBeenCalledWith(
    '/api/strategies/copy-one',
    expect.objectContaining({ method: 'DELETE' })
  )
})
it('does not delete when confirmation is canceled', async () => {
  vi.mocked(confirmToast).mockResolvedValue(false)
  const fetcher = vi.fn()
  vi.stubGlobal('fetch', fetcher)
  render(
    <CopyStrategyEditor
      strategy={strategy}
      onSaved={vi.fn()}
      onDeleted={vi.fn()}
    />
  )
  fireEvent.click(screen.getByText('Delete strategy'))
  await waitFor(() => expect(screen.getByText('Delete strategy')).toBeEnabled())
  expect(fetcher).not.toHaveBeenCalled()
})
it('keeps the strategy when the server rejects linked traders', async () => {
  vi.mocked(confirmToast).mockResolvedValue(true)
  vi.stubGlobal(
    'fetch',
    vi.fn().mockResolvedValue({
      ok: false,
      json: async () => ({ error: 'Reassign linked traders first' }),
    })
  )
  const removed = vi.fn()
  render(
    <CopyStrategyEditor
      strategy={strategy}
      onSaved={vi.fn()}
      onDeleted={removed}
    />
  )
  fireEvent.click(screen.getByText('Delete strategy'))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Reassign linked traders first'
  )
  expect(removed).not.toHaveBeenCalled()
})
it('disables deletion of the current account default', () => {
  render(
    <CopyStrategyEditor
      strategy={{ ...strategy, is_active: true }}
      onSaved={vi.fn()}
      onDeleted={vi.fn()}
    />
  )
  expect(screen.getByText('Delete strategy')).toBeDisabled()
})

it('keeps system-provided built-ins instead of deleting and recreating them', () => {
  render(
    <CopyStrategyEditor
      strategy={{ ...strategy, is_builtin: true }}
      onSaved={vi.fn()}
      onDeleted={vi.fn()}
    />
  )
  expect(screen.getByText('Delete strategy')).toBeDisabled()
  expect(screen.getByText(/Built-in strategies are retained/)).toBeVisible()
})
