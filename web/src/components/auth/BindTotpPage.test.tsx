import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { BindTotpPage } from './BindTotpPage'

const mocks = vi.hoisted(() => ({
  startBindTotp: vi.fn(),
  confirmBindTotp: vi.fn(),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))
vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({
    startBindTotp: mocks.startBindTotp,
    confirmBindTotp: mocks.confirmBindTotp,
  }),
}))
vi.mock('qrcode.react', () => ({
  QRCodeSVG: (props: { value: string }) => (
    <div data-testid="qr" data-value={props.value} />
  ),
}))

function renderPage(state?: { email?: string; password?: string }) {
  return render(
    <MemoryRouter
      initialEntries={[
        state ? { pathname: '/bind-totp', state } : '/bind-totp',
      ]}
    >
      <Routes>
        <Route path="/bind-totp" element={<BindTotpPage />} />
        <Route path="/login" element={<div>LOGIN-PAGE</div>} />
        <Route path="/traders" element={<div>TRADERS-PAGE</div>} />
      </Routes>
    </MemoryRouter>
  )
}

describe('BindTotpPage', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    sessionStorage.clear()
  })

  it('binds a prefilled account end-to-end and lands in the workspace', async () => {
    mocks.startBindTotp.mockResolvedValue({
      success: true,
      data: {
        totp_secret: 'SECRET999',
        otpauth_url: 'otpauth://totp/NOFX:owner@example.com',
      },
    })
    mocks.confirmBindTotp.mockResolvedValue({
      success: true,
      data: { recovery_codes: ['aaaa-bbbb', 'cccc-dddd'] },
    })
    renderPage({ email: 'owner@example.com', password: 'StrongPass1!' })

    // Credentials arrive via router state — only the Continue click is needed.
    fireEvent.click(screen.getByRole('button', { name: /^continue$/i }))

    expect(mocks.startBindTotp).toHaveBeenCalledWith(
      'owner@example.com',
      'StrongPass1!'
    )
    const qr = await screen.findByTestId('qr')
    expect(qr.getAttribute('data-value')).toBe(
      'otpauth://totp/NOFX:owner@example.com'
    )
    expect(screen.getByText('SECRET999')).toBeTruthy()

    fireEvent.change(screen.getByPlaceholderText('6-digit code'), {
      target: { value: '123456' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^confirm$/i }))

    expect(mocks.confirmBindTotp.mock.calls[0]).toEqual([
      'owner@example.com',
      'StrongPass1!',
      '123456',
    ])

    // Recovery codes are shown once, with the warning.
    expect(await screen.findByText('aaaa-bbbb')).toBeTruthy()
    expect(screen.getByText(/shown only once/i)).toBeTruthy()

    fireEvent.click(
      screen.getByRole('button', { name: /i saved them — continue/i })
    )
    expect(await screen.findByText('TRADERS-PAGE')).toBeTruthy()
  })

  it('collects credentials manually when arriving without router state', async () => {
    mocks.startBindTotp.mockResolvedValue({
      success: true,
      data: { totp_secret: 'S', otpauth_url: 'otpauth://totp/x' },
    })
    renderPage()

    fireEvent.change(screen.getByPlaceholderText('Email'), {
      target: { value: 'owner@example.com' },
    })
    fireEvent.change(screen.getByPlaceholderText('Password'), {
      target: { value: 'StrongPass1!' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^continue$/i }))

    await waitFor(() =>
      expect(mocks.startBindTotp).toHaveBeenCalledWith(
        'owner@example.com',
        'StrongPass1!'
      )
    )
    expect(await screen.findByTestId('qr')).toBeTruthy()
  })

  it('surfaces wrong-credential errors from the bind start endpoint', async () => {
    mocks.startBindTotp.mockResolvedValue({
      success: false,
      message: 'Email or password incorrect',
    })
    renderPage({ email: 'owner@example.com', password: 'wrong' })

    fireEvent.click(screen.getByRole('button', { name: /^continue$/i }))

    expect(await screen.findByText('Email or password incorrect')).toBeTruthy()
    expect(screen.queryByTestId('qr')).toBeNull()
  })

  it('surfaces a wrong confirmation code and keeps the QR step', async () => {
    mocks.startBindTotp.mockResolvedValue({
      success: true,
      data: { totp_secret: 'S', otpauth_url: 'otpauth://totp/x' },
    })
    mocks.confirmBindTotp.mockResolvedValue({
      success: false,
      message: 'Invalid verification code',
    })
    renderPage({ email: 'owner@example.com', password: 'StrongPass1!' })

    fireEvent.click(screen.getByRole('button', { name: /^continue$/i }))
    await screen.findByTestId('qr')
    fireEvent.change(screen.getByPlaceholderText('6-digit code'), {
      target: { value: '000000' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^confirm$/i }))

    expect(await screen.findByText('Invalid verification code')).toBeTruthy()
    expect(screen.getByTestId('qr')).toBeTruthy()
    expect(screen.queryByText(/shown only once/i)).toBeNull()
  })

  it('offers a way back to the login page', async () => {
    renderPage()

    fireEvent.click(screen.getByRole('button', { name: /back to login/i }))

    expect(await screen.findByText('LOGIN-PAGE')).toBeTruthy()
  })
})
