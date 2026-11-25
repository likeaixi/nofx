import type { TraderInfo } from '../../types'

interface Props {
  strategyId: string
  isDefault: boolean
  isBuiltin?: boolean
  traders?: TraderInfo[]
  language: string
}

export function StrategyStatusBadges({
  strategyId,
  isDefault,
  isBuiltin,
  traders,
  language,
}: Props) {
  const running = (traders ?? []).filter(
    (trader) => trader.strategy_id === strategyId && trader.is_running === true
  )
  const zh = language === 'zh'
  return (
    <span className="flex shrink-0 flex-wrap justify-end gap-1">
      {isBuiltin && (
        <span className="rounded-sm bg-nofx-gold/15 px-1.5 py-0.5 text-[10px] text-nofx-gold">
          {zh ? '内置策略' : 'Built-in strategy'}
        </span>
      )}
      {isDefault && (
        <span className="rounded-sm bg-nofx-gold/15 px-1.5 py-0.5 text-[10px] text-nofx-gold">
          {zh ? '账户默认' : 'Account default'}
        </span>
      )}
      {running.length > 0 && (
        <span
          className="rounded-sm bg-nofx-success/15 px-1.5 py-0.5 text-[10px] text-nofx-success"
          title={running
            .map((trader) => trader.trader_name)
            .join(zh ? '、' : ', ')}
        >
          {zh ? '正在使用' : 'In use'} · {running.length}
        </span>
      )}
    </span>
  )
}
