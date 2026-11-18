import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import type { DecisionRecord } from '../../types'
import { ExecutionLog } from './ExecutionLog'

afterEach(cleanup)

it('shows new fixed-strategy runtime checks above high-numbered historical AI cycles', () => {
 const records = [
  { id: 400, cycle_number: 387, timestamp: '2026-11-18T10:41:50Z', success: true, decisions: [], execution_log: ['Old AI decision'] },
  { id: 402, cycle_number: 2, timestamp: '2026-11-18T11:27:45Z', success: true, decisions: [{action:'wait',symbol:'',success:true}], execution_log: ['AllinCrypto · 15 分钟运行决策日志', '运行状态：检查正常'] },
  { id: 401, cycle_number: 1, timestamp: '2026-11-18T11:11:45Z', success: true, decisions: [], execution_log: ['First runtime check'] },
 ] as DecisionRecord[]
 render(<ExecutionLog decisions={records} />)
 expect(screen.getAllByText(/^CYCLE /).map(el => el.textContent)).toEqual(['CYCLE 2', 'CYCLE 1', 'CYCLE 387'])
 expect(screen.getByText('运行状态：检查正常')).toBeVisible()
 expect(records.map(r=>r.cycle_number)).toEqual([387,2,1])
})

it('orders equal-time records by persisted id rather than reset cycle number', () => {
 const records = [
  { id: 5, cycle_number: 500, timestamp: '2026-11-18T11:00:00Z', success:true },
  { id: 6, cycle_number: 1, timestamp: '2026-11-18T11:00:00Z', success:true },
 ] as DecisionRecord[]
 render(<ExecutionLog decisions={records} />)
 expect(screen.getAllByText(/^CYCLE /).map(el=>el.textContent)).toEqual(['CYCLE 1','CYCLE 500'])
})
