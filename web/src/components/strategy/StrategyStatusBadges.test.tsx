import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it } from 'vitest'
import type { TraderInfo } from '../../types'
import { StrategyStatusBadges } from './StrategyStatusBadges'

afterEach(cleanup)
const traders = [
 { trader_id: 'a', trader_name: 'Follow one', strategy_id: 'copy', is_running: true },
 { trader_id: 'b', trader_name: 'Follow two', strategy_id: 'copy', is_running: true },
 { trader_id: 'c', trader_name: 'Stopped AI', strategy_id: 'default', is_running: false },
] as TraderInfo[]

it('distinguishes the default from a different strategy used by running traders', () => {
 const view = render(<StrategyStatusBadges strategyId="default" isDefault traders={traders} language="zh" />)
 expect(screen.getByText('默认策略')).toBeVisible()
 expect(screen.queryByText(/正在使用/)).toBeNull()
 view.rerender(<StrategyStatusBadges strategyId="copy" isDefault={false} traders={traders} language="zh" />)
 expect(screen.queryByText('默认策略')).toBeNull()
 expect(screen.getByText('正在使用 · 2')).toHaveAttribute('title', 'Follow one、Follow two')
})

it('can show default and in-use badges on the same strategy', () => {
 render(<StrategyStatusBadges strategyId="copy" isDefault traders={traders} language="en" />)
 expect(screen.getByText('Default strategy')).toBeVisible()
 expect(screen.getByText('In use · 2')).toBeVisible()
})

it('does not infer running usage from a default flag or unavailable trader data', () => {
 render(<StrategyStatusBadges strategyId="copy" isDefault language="zh" />)
 expect(screen.getByText('默认策略')).toBeVisible()
 expect(screen.queryByText(/正在使用/)).toBeNull()
})
