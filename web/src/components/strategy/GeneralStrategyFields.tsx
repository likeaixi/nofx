import type {
  AIStrategyConfig,
  IndicatorConfig,
  RiskControlConfig,
} from '../../types'
import { useLanguage } from '../../contexts/LanguageContext'
export function GeneralStrategyFields({
  config,
  onChange,
}: {
  config: AIStrategyConfig
  onChange: (patch: Partial<AIStrategyConfig>) => void
}) {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const riskFields: Array<
    [keyof RiskControlConfig, string, string, number, number, number]
  > = [
    [
      'btc_eth_max_leverage',
      'BTC/ETH 杠杆上限',
      'BTC/ETH max leverage',
      1,
      20,
      1,
    ],
    [
      'altcoin_max_leverage',
      '其他合约杠杆上限',
      'Other max leverage',
      1,
      20,
      1,
    ],
    ['max_positions', '最多持仓数', 'Max positions', 1, 8, 1],
    [
      'max_margin_usage',
      '最大保证金占比（0–1）',
      'Max margin ratio (0–1)',
      0.1,
      1,
      0.01,
    ],
    [
      'btc_eth_max_position_value_ratio',
      'BTC/ETH 名义仓位 / 净权益上限',
      'BTC/ETH notional / equity cap',
      0.5,
      10,
      0.1,
    ],
    [
      'altcoin_max_position_value_ratio',
      '其他合约名义仓位 / 净权益上限',
      'Other notional / equity cap',
      0.5,
      10,
      0.1,
    ],
    [
      'min_position_size',
      '最小开仓名义金额（USDT）',
      'Min notional (USDT)',
      10,
      1000,
      1,
    ],
    [
      'min_risk_reward_ratio',
      '最低盈亏比（AI 指引）',
      'Min reward / risk (AI guidance)',
      1,
      10,
      0.1,
    ],
    ['min_confidence', '最低置信度', 'Min confidence', 50, 100, 1],
  ]
  const indicators: Array<[keyof IndicatorConfig, string]> = [
    ['enable_ema', 'EMA'],
    ['enable_macd', 'MACD'],
    ['enable_rsi', 'RSI'],
    ['enable_atr', 'ATR'],
    ['enable_boll', 'BOLL'],
    ['enable_volume', zh ? '成交量' : 'Volume'],
    ['enable_oi', 'OI'],
    ['enable_funding_rate', zh ? '资金费率' : 'Funding'],
  ]
  return (
    <section className="strategy-config-card">
      <h2>{zh ? '通用策略参数' : 'General strategy parameters'}</h2>
      <p>
        {zh
          ? '参数保存后用于此策略；保存不会启动实盘。止盈止损由 AI 按策略生成，最低盈亏比是决策指引。'
          : 'Saved parameters apply to this strategy; saving does not start trading. AI generates exits using the strategy and reward/risk guidance.'}
      </p>
      <div className="strategy-config-fields">
        <label>
          {zh ? '候选来源' : 'Candidate source'}
          <select
            value={config.coin_source.source_type}
            onChange={(e) =>
              onChange({
                coin_source: {
                  ...config.coin_source,
                  source_type: e.target.value as 'static' | 'oi_top',
                  use_oi_top: e.target.value === 'oi_top',
                  use_ai500: false,
                  use_oi_low: false,
                  use_hyper_all: false,
                  use_hyper_main: false,
                },
              })
            }
          >
            {!['static', 'oi_top'].includes(config.coin_source.source_type) && (
              <option value={config.coin_source.source_type}>
                {config.coin_source.source_type}
              </option>
            )}
            <option value="oi_top">OI Top</option>
            <option value="static">
              {zh ? '自选币种' : 'Selected symbols'}
            </option>
          </select>
        </label>

        {riskFields.map(([key, cn, en, min, max, step]) => (
          <label key={key}>
            {zh ? cn : en}
            <input
              type="number"
              min={min}
              max={max}
              step={step}
              value={config.risk_control[key]}
              onChange={(e) => {
                const value = Number(e.target.value)
                if (Number.isFinite(value))
                  onChange({
                    risk_control: { ...config.risk_control, [key]: value },
                  })
              }}
            />
          </label>
        ))}
        <label>
          {zh ? 'K 线周期' : 'Candle timeframe'}
          <select
            value={config.indicators.klines.primary_timeframe}
            onChange={(e) =>
              onChange({
                indicators: {
                  ...config.indicators,
                  klines: {
                    ...config.indicators.klines,
                    primary_timeframe: e.target.value,
                    selected_timeframes: config.indicators.klines
                      .enable_multi_timeframe
                      ? config.indicators.klines.selected_timeframes
                      : [e.target.value],
                  },
                },
              })
            }
          >
            {['1m', '5m', '15m', '30m', '1h', '4h', '1d'].map((x) => (
              <option key={x}>{x}</option>
            ))}
          </select>
        </label>
        <label>
          {zh ? 'K 线数量' : 'Candle count'}
          <input
            type="number"
            min={10}
            max={30}
            value={config.indicators.klines.primary_count}
            onChange={(e) =>
              onChange({
                indicators: {
                  ...config.indicators,
                  klines: {
                    ...config.indicators.klines,
                    primary_count: Number(e.target.value),
                  },
                },
              })
            }
          />
        </label>
        <div className="strategy-field-wide flex gap-4 flex-wrap">
          {indicators.map(([key, label]) => (
            <label key={key} className="strategy-check">
              <input
                type="checkbox"
                checked={!!config.indicators[key]}
                onChange={(e) =>
                  onChange({
                    indicators: {
                      ...config.indicators,
                      [key]: e.target.checked,
                    },
                  })
                }
              />
              {label}
            </label>
          ))}
        </div>
        <label className="strategy-field-wide">
          {zh ? '策略补充指令' : 'Strategy instructions'}
          <textarea
            rows={4}
            value={config.custom_prompt || ''}
            onChange={(e) => onChange({ custom_prompt: e.target.value })}
          />
        </label>
      </div>
    </section>
  )
}
