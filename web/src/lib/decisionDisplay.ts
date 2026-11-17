import type { DecisionRecord } from '../types'
import type { Language } from '../i18n/translations'

// Recognize only the explicit unsupported-market preflight result, never a
// generic "opening skipped" message (which can be a timeout or invalid data).
function unsupportedMessage(raw: string, language: Language): string | undefined {
  const match = raw.match(/opening skipped: (\S+) is not supported on target exchange ([a-z0-9_-]+)(?=[:;\s]|$)/i)
  if (!match) return undefined
  const [, symbol, exchange] = match
  if (language === 'zh') return `已跳过 ${symbol}：当前交易所 ${exchange} 不支持该币种，未下单。`
  if (language === 'id') return `Dilewati ${symbol}: bursa ${exchange} tidak mendukung simbol ini; tidak ada order.`
  return `Skipped ${symbol}: target exchange ${exchange} does not support this symbol; no order placed.`
}

// Normalize presentation only. Historical audit rows remain untouched.
export function decisionForDisplay(record: DecisionRecord, language: Language = 'en'): DecisionRecord {
  const decisions = (record.decisions ?? []).map((action) => {
    const reason = unsupportedMessage(action.skip_reason || action.error || '', language)
    if (!action.skipped && !reason) return action
    return { ...action, skipped: true, error: '', skip_reason: reason || action.skip_reason }
  })
  if (!decisions.some((action) => action.skipped)) return record
  const trades = decisions.filter((action) => action.action !== 'hold' && action.action !== 'wait' && !action.skipped)
  const failed = trades.filter((action) => !action.success).length
  // Only replace the old generated aggregate. Independent cycle errors remain.
  const generatedSummary = /^\d+ of \d+ trade actions failed or were blocked$/.test(record.error_message || '')
  return {
    ...record,
    decisions,
    execution_log: (record.execution_log ?? []).map((line) => {
      const reason = unsupportedMessage(line, language)
      return reason ? `⚠ ${reason}` : line
    }),
    success: generatedSummary ? failed === 0 : record.success,
    error_message: generatedSummary
      ? (failed ? `${failed} of ${trades.length} trade actions failed or were blocked` : '')
      : record.error_message,
  }
}
