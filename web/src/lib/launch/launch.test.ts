import { describe, expect, it, vi, beforeEach } from 'vitest'
import {
  describeLaunchFailures,
  failedLaunchChecks,
  primarySetupTarget,
  setupTargetForCheck,
} from './preflight'
import { pickTradingExchange, pickTradingModel } from './resolve'
import type { LaunchCheck, LaunchPreflightResult } from './types'
import type { AIModel, Exchange } from '../../types'

vi.mock('../api', () => ({ api: {} }))
vi.mock('../api/helpers', () => ({
  API_BASE: '',
  httpClient: { get: vi.fn(), post: vi.fn() },
}))

function preflightResult(checks: LaunchCheck[]): LaunchPreflightResult {
  return {
    ready: checks.every((check) => check.status !== 'failed'),
    checks,
    min_ai_fee_usdc: 1,
    min_trading_usdc: 12,
    checked_at: new Date().toISOString(),
  }
}

describe('setupTargetForCheck', () => {
  it('routes AI model/wallet problems to model setup', () => {
    expect(setupTargetForCheck({ id: 'ai_model', status: 'failed' })).toBe(
      'models'
    )
    expect(setupTargetForCheck({ id: 'ai_wallet', status: 'failed' })).toBe(
      'models'
    )
    expect(
      setupTargetForCheck({ id: 'ai_wallet_funds', status: 'failed' })
    ).toBe('models')
  })

  it('routes exchange config/account problems to exchange setup', () => {
    expect(
      setupTargetForCheck({ id: 'exchange_config', status: 'failed' })
    ).toBe('exchanges')
    expect(
      setupTargetForCheck({ id: 'exchange_account', status: 'failed' })
    ).toBe('exchanges')
  })

  it('routes funding shortfalls to the funds anchor', () => {
    expect(
      setupTargetForCheck({ id: 'exchange_funds', status: 'failed' })
    ).toBe('exchanges-funds')
  })

  it('has no anchor for strategy problems', () => {
    expect(setupTargetForCheck({ id: 'strategy', status: 'failed' })).toBeNull()
  })
})

describe('primarySetupTarget', () => {
  it('returns the anchor for the first failing check', () => {
    const result = preflightResult([
      { id: 'ai_model', status: 'ok' },
      { id: 'ai_wallet_funds', status: 'failed', message: 'AI wallet empty.' },
      { id: 'exchange_funds', status: 'failed', message: 'Low margin.' },
    ])
    expect(primarySetupTarget(result)).toBe('models')
  })

  it('returns null when everything passes', () => {
    const result = preflightResult([
      { id: 'ai_model', status: 'ok' },
      { id: 'exchange_funds', status: 'warning' },
    ])
    expect(primarySetupTarget(result)).toBeNull()
  })
})

describe('describeLaunchFailures / failedLaunchChecks', () => {
  it('collects only failed checks and joins their messages', () => {
    const result = preflightResult([
      { id: 'ai_wallet_funds', status: 'failed', message: 'AI wallet empty.' },
      { id: 'exchange_funds', status: 'warning', message: 'Testnet funds.' },
      { id: 'exchange_account', status: 'failed', message: 'Bad key.' },
      { id: 'strategy', status: 'skipped' },
    ])
    expect(failedLaunchChecks(result)).toHaveLength(2)
    expect(describeLaunchFailures(result)).toBe('AI wallet empty. Bad key.')
  })
})

describe('pickTradingModel', () => {
  const base: Partial<AIModel> = { enabled: true }

  it('picks the first enabled model with a credential', () => {
    const models = [
      { ...base, id: 'deepseek', provider: 'deepseek', has_api_key: true },
      { ...base, id: 'openai', provider: 'openai', has_api_key: true },
    ] as AIModel[]
    expect(pickTradingModel(models)?.id).toBe('deepseek')
  })

  it('returns null when nothing usable exists', () => {
    const models = [
      { id: 'x', provider: 'openai', enabled: false, has_api_key: true },
      { id: 'y', provider: 'deepseek', enabled: true },
    ] as AIModel[]
    expect(pickTradingModel(models)).toBeNull()
  })
})

describe('pickTradingExchange', () => {
  beforeEach(() => vi.clearAllMocks())

  it('requires enabled + key + builder approval + wallet address', () => {
    const ready = {
      id: 'hl',
      exchange_type: 'hyperliquid',
      enabled: true,
      has_api_key: true,
      hyperliquidBuilderApproved: true,
      hyperliquidWalletAddr: '0x1',
    } as unknown as Exchange
    expect(pickTradingExchange([ready])?.id).toBe('hl')

    const unapproved = {
      ...ready,
      hyperliquidBuilderApproved: false,
    } as unknown as Exchange
    expect(pickTradingExchange([unapproved])).toBeNull()

    const noAddr = {
      ...ready,
      hyperliquidWalletAddr: ' ',
    } as unknown as Exchange
    expect(pickTradingExchange([noAddr])).toBeNull()
  })

  it('accepts any enabled CEX with a saved key — no HL prerequisites', () => {
    const binance = {
      id: 'binance-1',
      exchange_type: 'binance',
      enabled: true,
      has_api_key: true,
    } as unknown as Exchange
    expect(pickTradingExchange([binance])?.id).toBe('binance-1')

    const disabled = { ...binance, enabled: false } as unknown as Exchange
    expect(pickTradingExchange([disabled])).toBeNull()

    const noKey = { ...binance, has_api_key: false } as unknown as Exchange
    expect(pickTradingExchange([noKey])).toBeNull()
  })

  it('prefers the first ready exchange across mixed venues', () => {
    const incompleteHL = {
      id: 'hl',
      exchange_type: 'hyperliquid',
      enabled: true,
      has_api_key: true,
      hyperliquidBuilderApproved: false,
      hyperliquidWalletAddr: '0x1',
    } as unknown as Exchange
    const okx = {
      id: 'okx-1',
      exchange_type: 'okx',
      enabled: true,
      has_api_key: true,
    } as unknown as Exchange
    expect(pickTradingExchange([incompleteHL, okx])?.id).toBe('okx-1')
  })
})
