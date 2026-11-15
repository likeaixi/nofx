import { api } from '../api'
import type { AIModel, Exchange } from '../../types'

export function modelHasCredential(model: AIModel) {
  return Boolean(model.has_api_key || model.apiKey)
}

export function exchangeHasKey(exchange: Exchange) {
  return Boolean(exchange.has_api_key || exchange.apiKey)
}

export function isHyperliquidExchange(exchange: Exchange) {
  return exchange.exchange_type === 'hyperliquid'
}

/** Any enabled model with a saved credential. */
export function pickTradingModel(models: AIModel[]) {
  return models.find((model) => model.enabled && modelHasCredential(model)) || null
}

/**
 * A launch-capable exchange: enabled with a saved credential. Hyperliquid
 * additionally needs the one-time agent authorization (builder approval plus
 * wallet address) before the agent can sign trades.
 */
export function exchangeLaunchReady(exchange: Exchange) {
  if (!exchange.enabled || !exchangeHasKey(exchange)) return false
  if (isHyperliquidExchange(exchange)) {
    return (
      Boolean(exchange.hyperliquidBuilderApproved) &&
      (exchange.hyperliquidWalletAddr || '').trim() !== ''
    )
  }
  return true
}

/** Any exchange that can execute Autopilot trades right now. */
export function pickTradingExchange(exchanges: Exchange[]) {
  return exchanges.find(exchangeLaunchReady) || null
}

/**
 * Resolves a launch-capable AI model. Returns null when none is configured —
 * the caller routes the user into model setup.
 */
export async function resolveLaunchModel(): Promise<AIModel | null> {
  const models = await api.getModelConfigs()
  return pickTradingModel(models)
}

/**
 * Resolves a launch-capable exchange — any venue with a saved credential.
 * Returns the exchange or a message explaining the most specific missing
 * prerequisite.
 */
export async function resolveLaunchExchange(): Promise<
  { exchange: Exchange } | { exchange: null; reason: string }
> {
  const exchanges = await api.getExchangeConfigs()
  const ready = pickTradingExchange(exchanges)
  if (ready) return { exchange: ready }

  if (exchanges.length === 0) {
    return {
      exchange: null,
      reason:
        'No exchange account is connected. Connect an exchange in Config first.',
    }
  }

  const enabled = exchanges.find((exchange) => exchange.enabled)
  if (!enabled) {
    return {
      exchange: null,
      reason: 'No enabled exchange. Enable the exchange account first.',
    }
  }
  if (!exchangeHasKey(enabled)) {
    return {
      exchange: null,
      reason: `The ${enabled.name || enabled.exchange_type} API key is missing. Reconnect the exchange and save its API key.`,
    }
  }

  // Enabled with a key but still not launch-ready: only Hyperliquid has
  // extra prerequisites, so report the most specific one.
  const hyperliquid = enabled
  if (!hyperliquid.hyperliquidBuilderApproved) {
    return {
      exchange: null,
      reason:
        'Hyperliquid builder authorization is not complete. Finish wallet authorization first.',
    }
  }
  return {
    exchange: null,
    reason: 'The Hyperliquid wallet address is missing. Reconnect Hyperliquid first.',
  }
}
