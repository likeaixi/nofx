import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { TraderConfigModal } from './TraderConfigModal'
import type { Exchange } from '../../types'

vi.mock('../../contexts/LanguageContext', () => ({ useLanguage: () => ({ language: 'en' }) }))
vi.mock('../../lib/httpClient', () => ({
  httpClient: { get: async () => ({ success: true, data: { strategies: [{
    id: 'fixed', name: 'AllinCrypto', config: { strategy_type: 'allincrypto' }
  }] } }) }
}))
afterEach(cleanup)

it('creates a stopped fixed trader without a model or editable AI cadence', async () => {
  const save = vi.fn().mockResolvedValue(undefined)
  const okx: Exchange = { id: 'target', name: 'OKX', account_name: 'Futures', exchange_type: 'okx', type: 'cex', enabled: true }
  render(<TraderConfigModal isOpen onClose={() => {}} initialStrategyId="fixed" availableModels={[]} availableExchanges={[okx]} onSave={save} />)
  await screen.findByText(/Checks public fills every minute/)
  expect(screen.queryByRole('spinbutton')).toBeNull()
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Follow' } })
  fireEvent.click(screen.getByRole('button', { name: 'Create Trader' }))
  await waitFor(() => expect(save).toHaveBeenCalledWith(expect.objectContaining({
    name: 'Follow', strategy_id: 'fixed', ai_model_id: '', exchange_id: 'target', scan_interval_minutes: 1
  })))
})
