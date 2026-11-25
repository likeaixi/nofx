import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { SWRConfig } from 'swr'
import { PublicStrategyMarket, StrategyPublication } from './StrategySharing'
import { api } from '../../lib/api'
import type { Strategy } from '../../types'
vi.mock('../../lib/api', () => ({
  api: {
    updateStrategyPublication: vi.fn(),
    getPublicStrategies: vi.fn(),
    duplicateStrategy: vi.fn(),
  },
}))
afterEach(() => {
  cleanup()
  vi.clearAllMocks()
})
it('publishes through a metadata-only request and clears visibility on unpublish', async () => {
  vi.mocked(api.updateStrategyPublication).mockResolvedValue(undefined)
  const onSaved = vi.fn()
  render(
    <StrategyPublication
      strategy={
        { id: 'mine', is_public: false, config_visible: true } as Strategy
      }
      zh={false}
      onSaved={onSaved}
    />
  )
  expect(
    screen.getByLabelText('Show parameters and allow copying')
  ).toBeDisabled()
  fireEvent.click(screen.getByLabelText('List in strategy market'))
  fireEvent.click(screen.getByLabelText('Show parameters and allow copying'))
  fireEvent.click(screen.getByText('Save publication'))
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith(true, true))
  expect(api.updateStrategyPublication).toHaveBeenCalledWith('mine', true, true)
  fireEvent.click(screen.getByLabelText('List in strategy market'))
  fireEvent.click(screen.getByText('Save publication'))
  await waitFor(() => expect(onSaved).toHaveBeenCalledWith(false, false))
})
it('copies a public strategy to an independent saved strategy and blocks hidden parameters', async () => {
  vi.mocked(api.getPublicStrategies).mockResolvedValue([
    {
      id: 'shared',
      name: 'Shared',
      description: 'Public configuration',
      is_public: true,
      config_visible: true,
    },
    {
      id: 'hidden',
      name: 'Hidden',
      description: 'Description only',
      is_public: true,
      config_visible: false,
    },
  ])
  vi.mocked(api.duplicateStrategy).mockResolvedValue({ id: 'private-copy' })
  const onCopied = vi.fn().mockResolvedValue(undefined)
  render(
    <SWRConfig value={{ provider: () => new Map() }}>
      <PublicStrategyMarket zh={false} onCopied={onCopied} />
    </SWRConfig>
  )
  fireEvent.click(await screen.findByText('Copy to my strategies'))
  await waitFor(() => expect(onCopied).toHaveBeenCalledWith('private-copy'))
  expect(api.duplicateStrategy).toHaveBeenCalledWith('shared', 'Shared (copy)')
  expect(screen.getByText('Description only · copying disabled')).toBeDisabled()
})
