import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { AuthProvider, useAuth } from './AuthContext'

const mocks = vi.hoisted(() => ({ error: vi.fn() }))
vi.mock('sonner', () => ({ toast: { error: mocks.error } }))
vi.mock('./LanguageContext', () => ({
  useLanguage: () => ({ language: 'en' }),
}))
vi.mock('../lib/config', () => ({
  getSystemConfig: vi.fn().mockResolvedValue({}),
  invalidateSystemConfig: vi.fn(),
}))
vi.mock('../lib/httpClient', () => ({ reset401Flag: vi.fn(), httpClient: {} }))

function Session() {
  const { user, logout, isLoading } = useAuth()
  return (
    <>
      <p>{isLoading ? 'loading' : user?.email || 'signed out'}</p>
      <button onClick={logout}>Sign out</button>
    </>
  )
}

describe('durable logout', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.setItem('auth_token', 'isolated-test-token')
    localStorage.setItem(
      'auth_user',
      JSON.stringify({ id: 'owner', email: 'owner@example.test' })
    )
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  async function logout() {
    render(
      <MemoryRouter>
        <AuthProvider>
          <Session />
        </AuthProvider>
      </MemoryRouter>
    )
    await screen.findByText('owner@example.test')
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }))
  }

  for (const status of [200, 401]) {
    it(`clears the local session when the server confirms revocation or invalidity (${status})`, async () => {
      vi.stubGlobal(
        'fetch',
        vi.fn().mockResolvedValue({ ok: status === 200, status })
      )
      await logout()
      await screen.findByText('signed out')
      expect(localStorage.getItem('auth_token')).toBeNull()
    })
  }

  for (const networkError of [false, true]) {
    it(`does not pretend logout succeeded on ${networkError ? 'network' : 'database'} failure`, async () => {
      const fetcher = vi.fn()
      if (networkError) fetcher.mockRejectedValue(new Error('offline'))
      else fetcher.mockResolvedValue({ ok: false, status: 503 })
      vi.stubGlobal('fetch', fetcher)
      await logout()
      await waitFor(() => expect(mocks.error).toHaveBeenCalled())
      expect(localStorage.getItem('auth_token')).toBe('isolated-test-token')
      expect(screen.getByText('owner@example.test')).toBeTruthy()
    })
  }
})

/**
 * Multi-user TOTP auth contract tests: two-step registration, login with a
 * verification code, and the bind-totp flow. These stub the global fetch and
 * assert both the request bodies (exact field sets) and the session effects.
 */
function jsonResponse(status: number, body: unknown) {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: vi.fn().mockResolvedValue(body),
  }
}

function AuthProbe() {
  const auth = useAuth()

  // Runs the action only on click and records its result — never during render.
  const run = async (label: string, action: () => Promise<unknown>) => {
    const result = await action()
    const el = document.querySelector('[data-testid="probe-log"]')
    if (el) {
      el.textContent = JSON.stringify({ label, result })
    }
  }

  return (
    <div>
      <p data-testid="probe-log">none</p>
      <button
        onClick={() =>
          run('login', () => auth.login('e@x.com', 'StrongPass1!', '123456'))
        }
      >
        do-login
      </button>
      <button
        onClick={() =>
          run('register', () =>
            auth.register('e@x.com', 'StrongPass1!', 'INVITE123')
          )
        }
      >
        do-register
      </button>
      <button
        onClick={() =>
          run('confirmRegister', () =>
            auth.confirmRegister('e@x.com', 'StrongPass1!', '123456')
          )
        }
      >
        do-confirm-register
      </button>
      <button
        onClick={() =>
          run('startBindTotp', () =>
            auth.startBindTotp('e@x.com', 'StrongPass1!')
          )
        }
      >
        do-start-bind
      </button>
      <button
        onClick={() =>
          run('confirmBindTotp', () =>
            auth.confirmBindTotp('e@x.com', 'StrongPass1!', '123456')
          )
        }
      >
        do-confirm-bind
      </button>
    </div>
  )
}

async function renderProbe() {
  render(
    <MemoryRouter>
      <AuthProvider>
        <AuthProbe />
      </AuthProvider>
    </MemoryRouter>
  )
  await screen.findByText('do-login')
}

async function probeResult(): Promise<any> {
  const el = await screen.findByTestId('probe-log')
  return JSON.parse(el.textContent || '{}')
}

describe('multi-user TOTP auth flows', () => {
  let fetcher: ReturnType<typeof vi.fn>

  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    sessionStorage.clear()
    fetcher = vi.fn()
    vi.stubGlobal('fetch', fetcher)
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  const bodyOf = (call: number) => JSON.parse(fetcher.mock.calls[call][1].body)

  it('login sends totp_code and persists a session including the role', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(200, {
        token: 'tok-1',
        user_id: 'u-1',
        email: 'e@x.com',
        role: 'owner',
      })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-login'))

    const result = await waitFor(async () => probeResult())
    expect(result).toEqual({ label: 'login', result: { success: true } })
    expect(fetcher).toHaveBeenCalledWith(
      '/api/login',
      expect.objectContaining({ method: 'POST' })
    )
    expect(bodyOf(0)).toEqual({
      email: 'e@x.com',
      password: 'StrongPass1!',
      totp_code: '123456',
    })
    expect(localStorage.getItem('auth_token')).toBe('tok-1')
    expect(localStorage.getItem('user_id')).toBe('u-1')
    expect(JSON.parse(localStorage.getItem('auth_user') || '{}')).toEqual({
      id: 'u-1',
      email: 'e@x.com',
      role: 'owner',
    })
  })

  it('login flags needsBinding instead of storing a session for unbound accounts', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(403, { error: 'totp_binding_required' })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-login'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({ success: false, needsBinding: true })
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it('login maps account_disabled to a friendly message', async () => {
    fetcher.mockResolvedValue(jsonResponse(403, { error: 'account_disabled' }))
    await renderProbe()
    fireEvent.click(screen.getByText('do-login'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({
      success: false,
      message: 'This account has been disabled',
    })
  })

  it('login surfaces uniform credential/code errors verbatim', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(401, { error: 'Invalid verification code' })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-login'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({
      success: false,
      message: 'Invalid verification code',
    })
  })

  it('register step 1 returns the authenticator setup and does NOT log in', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(200, {
        totp_secret: 'SECRET234',
        otpauth_url: 'otpauth://totp/NOFX:e@x.com',
        is_first_user: false,
      })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-register'))

    const result = await waitFor(async () => probeResult())
    expect(result.result.success).toBe(true)
    expect(result.result.data.otpauth_url).toBe('otpauth://totp/NOFX:e@x.com')
    expect(fetcher.mock.calls[0][0]).toBe('/api/register')
    expect(bodyOf(0)).toEqual({
      email: 'e@x.com',
      password: 'StrongPass1!',
      lang: 'en',
      invite_code: 'INVITE123',
    })
    // No token in the step-1 response — nothing may be persisted yet.
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it('register surfaces invite errors (403) instead of a generic permission failure', async () => {
    fetcher.mockResolvedValue(jsonResponse(403, { error: 'Invite code required' }))
    await renderProbe()
    fireEvent.click(screen.getByText('do-register'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({
      success: false,
      message: 'Invite code required',
    })
  })

  it('confirmRegister sends exactly {email,password,totp_code}, activates and persists the session', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(200, {
        token: 'tok-2',
        user_id: 'u-2',
        email: 'e@x.com',
        role: 'user',
        recovery_codes: ['aaaa-bbbb', 'cccc-dddd'],
      })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-confirm-register'))

    const result = await waitFor(async () => probeResult())
    expect(result.result.success).toBe(true)
    expect(result.result.data.recovery_codes).toHaveLength(2)
    // Contract amendment: the invite is claimed server-side — no invite_code
    // may be sent at confirm time.
    expect(fetcher.mock.calls[0][0]).toBe('/api/register/confirm')
    expect(bodyOf(0)).toEqual({
      email: 'e@x.com',
      password: 'StrongPass1!',
      totp_code: '123456',
    })
    expect(localStorage.getItem('auth_token')).toBe('tok-2')
    expect(JSON.parse(localStorage.getItem('auth_user') || '{}').role).toBe('user')
  })

  it('confirmRegister surfaces wrong-code errors without persisting anything', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(401, { error: 'Invalid verification code' })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-confirm-register'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({
      success: false,
      message: 'Invalid verification code',
    })
    expect(localStorage.getItem('auth_token')).toBeNull()
  })

  it('startBindTotp posts credentials and returns the otpauth url', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(200, {
        totp_secret: 'SECRET999',
        otpauth_url: 'otpauth://totp/bind',
      })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-start-bind'))

    const result = await waitFor(async () => probeResult())
    expect(result.result.success).toBe(true)
    expect(result.result.data.otpauth_url).toBe('otpauth://totp/bind')
    expect(fetcher.mock.calls[0][0]).toBe('/api/auth/bind-totp')
    expect(bodyOf(0)).toEqual({ email: 'e@x.com', password: 'StrongPass1!' })
  })

  it('confirmBindTotp persists the session and returns recovery codes', async () => {
    fetcher.mockResolvedValue(
      jsonResponse(200, {
        token: 'tok-3',
        user_id: 'u-3',
        email: 'e@x.com',
        role: 'user',
        recovery_codes: ['r1', 'r2', 'r3'],
      })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-confirm-bind'))

    const result = await waitFor(async () => probeResult())
    expect(result.result.success).toBe(true)
    expect(result.result.data.recovery_codes).toEqual(['r1', 'r2', 'r3'])
    expect(fetcher.mock.calls[0][0]).toBe('/api/auth/bind-totp/confirm')
    expect(bodyOf(0)).toEqual({
      email: 'e@x.com',
      password: 'StrongPass1!',
      totp_code: '123456',
    })
    expect(localStorage.getItem('auth_token')).toBe('tok-3')
  })

  it('confirmBindTotp surfaces wrong-password (401) errors instead of redirecting', async () => {
    // Through the shared httpClient this 401 would trigger the global
    // session-expiry redirect; the bind flow must keep it local.
    fetcher.mockResolvedValue(
      jsonResponse(401, { error: 'Email or password incorrect' })
    )
    await renderProbe()
    fireEvent.click(screen.getByText('do-confirm-bind'))

    const result = await waitFor(async () => probeResult())
    expect(result.result).toEqual({
      success: false,
      message: 'Email or password incorrect',
    })
    expect(localStorage.getItem('auth_token')).toBeNull()
  })
})
