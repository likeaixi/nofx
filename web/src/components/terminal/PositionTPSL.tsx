import { useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CSSProperties, ReactNode } from 'react'
import useSWR from 'swr'
import { api } from '../../lib/api'
import { distPct, parseTpsl } from './tpsl'
import type { TpslFallback } from './tpsl'

const TP_COLOR = '#2E8B57'
const SL_COLOR = '#D6433A'

/** Lightweight hover tooltip: portals to body, fixed position, viewport-clamped. */
export function TerminalTooltip({
  content,
  children,
}: {
  content: ReactNode
  children: ReactNode
}) {
  const [open, setOpen] = useState(false)
  const [style, setStyle] = useState<CSSProperties | null>(null)
  const anchorRef = useRef<HTMLSpanElement>(null)
  const popRef = useRef<HTMLDivElement>(null)

  // Measure after mount, then clamp inside the viewport. The table sits in the
  // page's bottom-right corner, so pop toward upper-left by default.
  useLayoutEffect(() => {
    if (!open) return
    const a = anchorRef.current?.getBoundingClientRect()
    const p = popRef.current
    if (!a || !p) return
    const M = 8
    const w = p.offsetWidth
    const h = p.offsetHeight
    let left = a.right - w
    let top = a.top - h - 6
    if (top < M) top = Math.min(a.bottom + 6, window.innerHeight - h - M)
    if (left < M) left = M
    if (left + w > window.innerWidth - M) left = window.innerWidth - w - M
    setStyle({ position: 'fixed', top, left })
  }, [open, content])

  return (
    <span
      ref={anchorRef}
      data-testid="tpsl-trigger"
      style={{ cursor: 'help' }}
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
    >
      {children}
      {open &&
        createPortal(
          <div
            ref={popRef}
            className="tm-mono"
            style={{
              ...(style ?? {
                position: 'fixed',
                top: -9999,
                left: -9999,
                visibility: 'hidden',
              }),
              zIndex: 1000,
              background: 'var(--tm-bg, #0b0f14)',
              border: '1px solid var(--tm-hair, #263140)',
              borderRadius: 4,
              padding: '6px 8px',
              fontSize: 11,
              lineHeight: 1.6,
              whiteSpace: 'nowrap',
              boxShadow: '0 4px 16px rgba(0,0,0,0.5)',
              pointerEvents: 'none',
            }}
          >
            {content}
          </div>,
          document.body
        )}
    </span>
  )
}

interface PositionTPSLProps {
  traderId?: string
  symbol: string
  markPrice: number
  fallback?: TpslFallback
}

function fmtPx(n: number): string {
  const dp = n >= 1000 ? 0 : n >= 100 ? 1 : n >= 1 ? 2 : 4
  return n.toLocaleString('en-US', {
    minimumFractionDigits: dp,
    maximumFractionDigits: dp,
  })
}
function fmtPct(n: number): string {
  return Number.isNaN(n) ? '—' : `${n >= 0 ? '+' : ''}${n.toFixed(2)}%`
}

/** TP/SL status dots for one position row; hover shows trigger prices vs mark. */
export function PositionTPSL({
  traderId,
  symbol,
  markPrice,
  fallback,
}: PositionTPSLProps) {
  const { data, error } = useSWR(
    traderId ? `open-orders-${traderId}-${symbol}` : null,
    () => api.getOpenOrders(traderId!, symbol, true),
    { refreshInterval: 15000 }
  )

  const { tp, sl } = parseTpsl(data)
  const useFallback = !!error
  const fbTp =
    useFallback && fallback !== undefined && (fallback.tp ?? 0) > 0
      ? fallback.tp
      : undefined
  const fbSl =
    useFallback && fallback !== undefined && (fallback.sl ?? 0) > 0
      ? fallback.sl
      : undefined
  const tpList = fbTp !== undefined ? [fbTp] : tp
  const slList = fbSl !== undefined ? [fbSl] : sl
  if (tpList.length === 0 && slList.length === 0) return null

  const row = (kind: 'TP' | 'SL', price: number) => (
    <div
      key={`${kind}-${price}`}
      style={{ color: kind === 'TP' ? TP_COLOR : SL_COLOR }}
    >
      {kind} {fmtPx(price)} {fmtPct(distPct(price, markPrice))}
    </div>
  )

  return (
    <TerminalTooltip
      content={
        <>
          {tpList.map((p) => row('TP', p))}
          {slList.map((p) => row('SL', p))}
          {useFallback && (
            <div style={{ color: 'var(--tm-muted, #6b7a8c)', fontSize: 9 }}>
              decision fallback
            </div>
          )}
        </>
      }
    >
      <span style={{ letterSpacing: 2 }}>
        {tpList.length > 0 && <span style={{ color: TP_COLOR }}>●</span>}
        {slList.length > 0 && (
          <span
            style={{ color: SL_COLOR, marginLeft: tpList.length > 0 ? 3 : 0 }}
          >
            ●
          </span>
        )}
      </span>
    </TerminalTooltip>
  )
}
