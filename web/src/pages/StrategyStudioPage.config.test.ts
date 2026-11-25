import { expect, it } from 'vitest'
import { simplifyConfig } from './StrategyStudioPage'
it('preserves custom general parameters through editor normalization', () => {
  const cfg = simplifyConfig(null)
  cfg.ai_config!.risk_control.btc_eth_max_leverage = 3
  cfg.ai_config!.risk_control.altcoin_max_leverage = 5
  cfg.ai_config!.risk_control.max_margin_usage = 0.25
  cfg.ai_config!.indicators.enable_rsi = true
  cfg.ai_config!.indicators.klines.primary_timeframe = '1h'
  cfg.ai_config!.coin_source.source_type = 'static'
  cfg.ai_config!.coin_source.static_coins = ['BTCUSDT']
  const saved = simplifyConfig(cfg)
  expect(saved.ai_config).toEqual(cfg.ai_config)
})
it('does not turn shadow copying into AI strategy configuration', () => {
  const cfg = {
    strategy_type: 'allincrypto' as const,
    copy_config: {
      source_url: 'https://www.binance.com/en/copy-trading/lead-details/123',
      leverage: 3,
    },
  }
  expect(simplifyConfig(cfg)).toEqual(cfg)
})
