import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { vi, it, expect, afterEach } from 'vitest'
import { CopyStrategyEditor } from './CopyStrategyEditor'
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ token: 'test-token' }),
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }))
afterEach(() => vi.unstubAllGlobals())
it('saves a live copy strategy with the entered URL and leverage without starting a trader', async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue({ ok: true, json: async () => ({ id: 'new' }) })
  vi.stubGlobal('fetch', fetcher)
  const saved = vi.fn().mockResolvedValue(undefined)
  render(<CopyStrategyEditor onSaved={saved} />)
  fireEvent.change(screen.getByLabelText('Lead profile URL'), {
    target: {
      value: 'https://www.binance.com/en/copy-trading/lead-details/123',
    },
  })
  fireEvent.change(screen.getByLabelText('Leverage (1–20)'), {
    target: { value: '3' },
  })
  fireEvent.click(screen.getByText('Save strategy'))
  await waitFor(() => expect(saved).toHaveBeenCalledWith('new'))
  expect(fetcher).toHaveBeenCalledTimes(1)
  const [url, req] = fetcher.mock.calls[0]
  expect(url).toBe('/api/strategies')
  expect(req.method).toBe('POST')
  expect(JSON.parse(req.body).config).toMatchObject({
    strategy_type: 'allincrypto',
    copy_config: {
      source_url: 'https://www.binance.com/en/copy-trading/lead-details/123',
      leverage: 3,
    },
  })
})

it('preserves publication settings when editing an existing strategy', async () => {
  const fetcher = vi
    .fn()
    .mockResolvedValue({ ok: true, json: async () => ({}) })
  vi.stubGlobal('fetch', fetcher)
  const strategy = {
    id: 'existing',
    name: 'Copy',
    description: 'Keep description',
    is_default: false,
    is_public: true,
    config_visible: true,
    config: {
      strategy_type: 'allincrypto',
      copy_config: {
        source_url: 'https://www.binance.com/en/copy-trading/lead-details/123',
        leverage: 10,
      },
    },
  } as import('../../types').Strategy
  render(<CopyStrategyEditor strategy={strategy} onSaved={async () => {}} />)
  fireEvent.change(screen.getByLabelText('Leverage (1–20)'), {
    target: { value: '4' },
  })
  expect(screen.getByText('Configure trader account')).toBeDisabled()
  fireEvent.click(screen.getByText('Save strategy'))
  await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1))
  const body = JSON.parse(fetcher.mock.calls[0][1].body)
  expect(body).toMatchObject({
    is_public: true,
    config_visible: true,
    description: 'Keep description',
    config: { publish_config: { is_public: true, config_visible: true } },
  })
})
