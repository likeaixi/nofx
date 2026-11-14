import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { LoginPage } from './LoginPage'

const mocks = vi.hoisted(() => ({ login: vi.fn() }))

vi.mock('sonner', () => ({
  toast: {
    error: vi.fn(),
    warning: vi.fn(),
    success: vi.fn(),
    dismiss: vi.fn(),
  },
}))
vi.mock('../../contexts/LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({ login: mocks.login }),
}))

function BindTotpRoute() {
  const location = useLocation() as { state?: { email?: string; password?: string } }
  return (
    <div data-testid="bind-route">
      BIND-PAGE {location.state?.email} {location.state?.password}
    </div>
  )
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/bind-totp" element={<BindTotpRoute />} />
      </Routes>
    </MemoryRouter>
  )
}

function fillAndSubmit(code = '123456') {
  fireEvent.change(screen.getByPlaceholderText('you@example.com'), {
    target: { value: 'owner@example.com' },
  })
  fireEvent.change(screen.getByPlaceholderText('••••••••'), {
    target: { value: 'StrongPass1!' },
  })
  fireEvent.change(screen.getByPlaceholderText('6-digit code or recovery code'), {
    target: { value: code },
  })
  fireEvent.click(screen.getByRole('button', { name: /sign in/i }))
}

describe('LoginPage - TOTP verification code', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('sends email, password and the verification code to login', async () => {
    mocks.login.mockResolvedValue({ success: true })
    renderPage()

    fillAndSubmit('123456')

    await waitFor(() =>
      expect(mocks.login).toHaveBeenCalledWith(
        'owner@example.com',
        'StrongPass1!',
        '123456'
      )
    )
  })

  it('redirects to /bind-totp with the credentials when binding is required', async () => {
    mocks.login.mockResolvedValue({ success: false, needsBinding: true })
    renderPage()

    fillAndSubmit('')

    const target = await screen.findByTestId('bind-route')
    expect(target.textContent).toBe('BIND-PAGE owner@example.com StrongPass1!')
  })

  it('shows credential/code errors inline and stays on the login form', async () => {
    mocks.login.mockResolvedValue({
      success: false,
      message: 'Invalid verification code',
    })
    renderPage()

    fillAndSubmit('000000')

    expect(await screen.findByText('Invalid verification code')).toBeTruthy()
    expect(screen.queryByTestId('bind-route')).toBeNull()
  })
})
