import { useState } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import type { Strategy } from '../../types'

const panel =
  'mb-4 rounded-lg border border-[rgba(25,36,52,0.14)] bg-nofx-bg-lighter p-4 text-sm text-nofx-text'
const button =
  'rounded-md border border-[rgba(25,36,52,0.18)] px-3 py-2 text-sm disabled:opacity-40'

export function StrategyPublication({
  strategy,
  zh,
  onSaved,
}: {
  strategy: Strategy
  zh: boolean
  onSaved: (published: boolean, visible: boolean) => void
}) {
  const [published, setPublished] = useState(strategy.is_public)
  const [visible, setVisible] = useState(
    strategy.is_public && strategy.config_visible
  )
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const save = async () => {
    setBusy(true)
    setMessage('')
    setError('')
    try {
      await api.updateStrategyPublication(
        strategy.id,
        published,
        published && visible
      )
      onSaved(published, published && visible)
      setMessage(zh ? '公开设置已保存' : 'Publication settings saved')
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setBusy(false)
    }
  }
  return (
    <section
      className={panel}
      aria-label={zh ? '策略公开设置' : 'Strategy publication'}
    >
      <h2 className="mb-2 font-semibold">
        {zh ? '公开策略' : 'Publish strategy'}
      </h2>
      <p className="mb-3 text-xs text-nofx-text-muted">
        {zh
          ? '公开已保存的名称和介绍；开启参数可见后，其他用户可以查看并复制配置。数据服务密钥和私有数据连接不会公开或复制。'
          : 'Publish the saved name and description. Visible parameters let others view and copy the configuration. Provider keys and private data connections are excluded.'}
      </p>
      <div className="flex flex-wrap items-center gap-4">
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={published}
            disabled={busy}
            onChange={(e) => {
              setPublished(e.target.checked)
              if (!e.target.checked) setVisible(false)
            }}
          />
          {zh ? '公开到策略市场' : 'List in strategy market'}
        </label>
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={visible}
            disabled={busy || !published}
            onChange={(e) => setVisible(e.target.checked)}
          />
          {zh ? '公开参数，允许复制' : 'Show parameters and allow copying'}
        </label>
        <button className={button} disabled={busy} onClick={() => void save()}>
          {busy ? '…' : zh ? '保存公开设置' : 'Save publication'}
        </button>
      </div>
      {message && (
        <p role="status" className="mt-2 text-xs">
          {message}
        </p>
      )}
      {error && (
        <p role="alert" className="mt-2 text-xs text-red-600">
          {error}
        </p>
      )}
    </section>
  )
}

export function PublicStrategyMarket({
  zh,
  onCopied,
}: {
  zh: boolean
  onCopied: (id: string) => Promise<void>
}) {
  const { data, error, mutate } = useSWR(
    'public-strategy-market',
    () => api.getPublicStrategies(),
    { refreshInterval: 30000 }
  )
  const [busy, setBusy] = useState('')
  const [copyError, setCopyError] = useState('')
  const copy = async (id: string, name: string) => {
    setBusy(id)
    setCopyError('')
    try {
      const result = await api.duplicateStrategy(
        id,
        `${name} (${zh ? '副本' : 'copy'})`
      )
      await onCopied(result.id)
    } catch (e) {
      setCopyError(e instanceof Error ? e.message : 'Copy failed')
      void mutate()
    } finally {
      setBusy('')
    }
  }
  return (
    <section className={panel}>
      <h2 className="mb-2 text-lg font-semibold">
        {zh ? '公开策略市场' : 'Public strategy market'}
      </h2>
      <p className="mb-4 text-xs text-nofx-text-muted">
        {zh
          ? '复制后成为你自己的私有策略，不自动启动交易。请配置自己的交易所及数据服务。'
          : 'Copies are private and do not start trading. Configure your own exchange and data services.'}
      </p>
      {error ? (
        <button className={button} onClick={() => void mutate()}>
          {zh ? '读取失败，重试' : 'Could not load. Retry'}
        </button>
      ) : !data ? (
        <p>{zh ? '加载中…' : 'Loading…'}</p>
      ) : !data.length ? (
        <p>{zh ? '暂无公开策略' : 'No public strategies yet'}</p>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {data.map((strategy) => (
            <article
              key={strategy.id}
              className="rounded-lg border border-[rgba(25,36,52,0.14)] p-4"
            >
              <h3 className="font-semibold">{strategy.name}</h3>
              <p className="my-2 whitespace-pre-wrap break-words text-xs text-nofx-text-muted">
                {strategy.description}
              </p>
              {strategy.config_visible && strategy.config && (
                <details className="mb-3 text-xs">
                  <summary className="cursor-pointer">
                    {zh ? '查看公开参数' : 'View public parameters'}
                  </summary>
                  <pre className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-all">
                    {JSON.stringify(strategy.config, null, 2)}
                  </pre>
                </details>
              )}
              <button
                className={button}
                disabled={!!busy || !strategy.config_visible}
                onClick={() => void copy(strategy.id, strategy.name)}
              >
                {busy === strategy.id
                  ? '…'
                  : !strategy.config_visible
                    ? zh
                      ? '仅公开介绍，不可复制'
                      : 'Description only · copying disabled'
                    : zh
                      ? '复制到我的策略'
                      : 'Copy to my strategies'}
              </button>
            </article>
          ))}
        </div>
      )}
      {copyError && (
        <p role="alert" className="mt-3 text-red-600">
          {copyError}
        </p>
      )}
    </section>
  )
}
