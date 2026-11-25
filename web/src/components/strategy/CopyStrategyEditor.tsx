import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../../contexts/AuthContext'
import { useLanguage } from '../../contexts/LanguageContext'
import type { Strategy } from '../../types'
import { ROUTES } from '../../router/paths'

const defaultURL =
  'https://www.binance.com/en/copy-trading/lead-details/5075281354358777856'
export function CopyStrategyEditor({
  strategy,
  onSaved,
}: {
  strategy?: Strategy
  onSaved: (id: string) => Promise<void>
}) {
  const { token } = useAuth()
  const { language } = useLanguage()
  const zh = language === 'zh'
  const navigate = useNavigate()
  const [name, setName] = useState(
    strategy?.name || (zh ? '影子跟单' : 'Shadow Copy')
  )
  const [url, setURL] = useState(
    strategy?.config.copy_config?.source_url || (strategy ? defaultURL : '')
  )
  const [leverage, setLeverage] = useState(
    strategy?.config.copy_config?.leverage ?? 10
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)
  const [dirty, setDirty] = useState(false)
  const change = () => {
    setSaved(false)
    setDirty(true)
  }
  const save = async () => {
    setBusy(true)
    setError('')
    try {
      const update = !!strategy && !strategy.is_default
      const response = await fetch(
        `${import.meta.env.VITE_API_BASE || ''}/api/strategies${update ? `/${strategy.id}` : ''}`,
        {
          method: update ? 'PUT' : 'POST',
          headers: {
            Authorization: `Bearer ${token}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({
            name: name.trim(),
            is_public: update ? strategy!.is_public : false,
            config_visible: update ? strategy!.config_visible : false,
            description:
              strategy?.description ||
              (zh ? '按公开成交实盘跟单' : 'Live copy of public lead fills'),
            config: {
              ...strategy?.config,
              strategy_type: 'allincrypto',
              publish_config: {
                is_public: update ? strategy!.is_public : false,
                config_visible: update ? strategy!.config_visible : false,
              },
              copy_config: { source_url: url.trim(), leverage },
            },
          }),
        }
      )
      const result = await response.json()
      if (!response.ok)
        throw new Error(result.error || 'Could not save strategy')
      await onSaved(update ? strategy!.id : result.id)
      setSaved(true)
      setDirty(false)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section className="strategy-config-card">
      <h2>{zh ? '影子跟单 · 实盘' : 'Shadow Copy · Live'}</h2>
      <p>
        {zh
          ? '填写币安带单人的主页网址即可识别来源。首次只建立基线，之后跟随新成交，不补开历史仓位。'
          : 'Paste a Binance lead profile URL. The first poll establishes a baseline; only new fills are followed.'}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
        className="strategy-config-fields"
      >
        <label>
          {zh ? '策略名称' : 'Strategy name'}
          <input
            required
            maxLength={100}
            value={name}
            onChange={(e) => {
              setName(e.target.value)
              change()
            }}
          />
        </label>
        <label className="strategy-field-wide">
          {zh ? '被跟单人主页网址' : 'Lead profile URL'}
          <input
            required
            type="url"
            placeholder="https://www.binance.com/en/copy-trading/lead-details/…"
            value={url}
            onChange={(e) => {
              setURL(e.target.value)
              change()
            }}
          />
        </label>
        <label>
          {zh ? '杠杆倍数（1–20）' : 'Leverage (1–20)'}
          <input
            required
            type="number"
            min={1}
            max={20}
            step={1}
            value={leverage}
            onChange={(e) => {
              setLeverage(Number(e.target.value))
              change()
            }}
          />
        </label>
        <p className="strategy-field-wide">
          {zh
            ? '支持 Binance / OKX 的 USDT 永续目标账户。按净权益比例计算名义仓位，杠杆影响保证金；实际成交均价反向 4% 止损。更改已有策略须先停止关联 trader 并处理完其持仓及订单。更换带单人后，下次启动建立独立跟单周期，保留旧历史，不补开历史订单。'
            : 'Supports Binance / OKX USDT perpetual accounts. Notional follows equity ratio; leverage changes required margin. Exchange stop: 4% from actual average fill. Stop linked traders and resolve exposure before editing. Changing the lead starts a separate copy period on next start, preserves previous history and never backfills old entries.'}
        </p>
        {error && (
          <p role="alert" className="strategy-field-wide text-red-600">
            {error}
          </p>
        )}
        <div className="strategy-field-wide flex gap-3 flex-wrap">
          <button disabled={busy || !name.trim()} type="submit">
            {busy
              ? '…'
              : strategy?.is_default
                ? zh
                  ? '另存为影子策略'
                  : 'Save as new copy strategy'
                : zh
                  ? '保存策略'
                  : 'Save strategy'}
          </button>
          {strategy && !strategy.is_default && (
            <button
              type="button"
              disabled={busy || dirty}
              onClick={() =>
                navigate(
                  `${ROUTES.traders}?strategy=${encodeURIComponent(strategy.id)}`
                )
              }
            >
              {zh ? '配置跟单账户' : 'Configure trader account'}
            </button>
          )}
          {saved && <span role="status">{zh ? '已保存' : 'Saved'}</span>}
        </div>
      </form>
    </section>
  )
}
