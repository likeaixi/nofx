import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import HeaderBar from '../common/HeaderBar'

vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ register: vi.fn() }),
}))

vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))

vi.mock('../common/HyperliquidWalletConnect', () => ({
  HyperliquidWalletConnect: () => null,
}))

// The first-time SetupPage (single-user bootstrap) was removed with the
// multi-user TOTP upgrade: registration is now always open on /register and
// the first owner lands in the guided flow via getPostAuthPath('beginner')
// (covered in RegisterPage.test.tsx).
describe('beginner-only live trading experience', () => {
  beforeEach(() => {
    localStorage.clear()
  })

  it('does not expose a mode switch after login', () => {
    render(
      <MemoryRouter initialEntries={['/traders']}>
        <HeaderBar
          isLoggedIn
          user={{ email: 'owner@example.com' }}
          onLogout={() => undefined}
        />
      </MemoryRouter>
    )

    fireEvent.click(screen.getByRole('button', { name: /owner@example.com/i }))

    expect(screen.queryByText(/Switch to Advanced/i)).toBeNull()
    expect(screen.queryByText(/Switch to Beginner/i)).toBeNull()
  })

  it('keeps desktop navigation labels on one line', () => {
    render(
      <MemoryRouter initialEntries={['/traders']}>
        <HeaderBar />
      </MemoryRouter>
    )

    expect(screen.getByRole('button', { name: 'Config' }).className).toContain(
      'whitespace-nowrap'
    )
  })

  it('keeps the full navigation visible in the desktop header', () => {
    render(
      <MemoryRouter initialEntries={['/traders']}>
        <HeaderBar />
      </MemoryRouter>
    )

    expect(
      screen.getByRole('button', { name: 'Config' }).parentElement?.className
    ).toContain('lg:flex')
    expect(
      screen.getByRole('button', { name: 'Open navigation' }).className
    ).toContain('lg:hidden')
  })
})
