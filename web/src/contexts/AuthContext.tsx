import React, { createContext, useContext, useState, useEffect } from 'react'
import { flushSync } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { toast } from 'sonner'
import { getSystemConfig, invalidateSystemConfig } from '../lib/config'
import { reset401Flag } from '../lib/httpClient'
import { getPostAuthPath } from '../lib/onboarding'
import { ROUTES } from '../router/paths'
import { useLanguage } from './LanguageContext'

interface User {
  id: string
  email: string
  role?: string
}

/** Step-1 registration response: the account is pending until the TOTP code is confirmed. */
interface RegisterTotpData {
  totp_secret: string
  otpauth_url: string
  is_first_user: boolean
}

/** Step-2 registration response: the account is active and a session is issued. */
interface AuthResultData {
  token: string
  user_id: string
  email: string
  role: string
  recovery_codes: string[]
}

/**
 * POST an authentication endpoint via raw fetch.
 *
 * These endpoints use 401/403 for *business* errors (wrong code, missing
 * invite, pending binding, disabled account), and the shared httpClient
 * interceptors would swallow those behind a global "Permission denied"
 * toast / a hard redirect to /login. Raw fetch keeps the error bodies with
 * the caller — the same reason login has always used fetch.
 */
async function postAuthEndpoint<T>(url: string, body: Record<string, unknown>) {
  const response = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  const data = (await response.json().catch(() => ({}))) as T & {
    error?: string
  }
  return { ok: response.ok, status: response.status, data }
}

interface AuthContextType {
  user: User | null
  token: string | null
  login: (
    email: string,
    password: string,
    totpCode?: string
  ) => Promise<{
    success: boolean
    message?: string
    needsBinding?: boolean
  }>
  register: (
    email: string,
    password: string,
    inviteCode?: string
  ) => Promise<{
    success: boolean
    message?: string
    data?: RegisterTotpData
  }>
  confirmRegister: (
    email: string,
    password: string,
    totpCode: string
  ) => Promise<{
    success: boolean
    message?: string
    data?: AuthResultData
  }>
  startBindTotp: (
    email: string,
    password: string
  ) => Promise<{
    success: boolean
    message?: string
    data?: { totp_secret: string; otpauth_url: string }
  }>
  confirmBindTotp: (
    email: string,
    password: string,
    totpCode: string
  ) => Promise<{
    success: boolean
    message?: string
    data?: { recovery_codes: string[] }
  }>
  logout: () => Promise<void>
  isLoading: boolean
}

const AuthContext = createContext<AuthContextType | undefined>(undefined)

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const { language } = useLanguage()
  const navigate = useNavigate()
  const [user, setUser] = useState<User | null>(null)
  const [token, setToken] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(true)

  useEffect(() => {
    // Reset 401 flag on page load to allow fresh 401 handling
    reset401Flag()

    // Check local storage for a persisted session
    getSystemConfig()
      .then(() => {
        const savedToken = localStorage.getItem('auth_token')
        const savedUser = localStorage.getItem('auth_user')
        if (savedToken && savedUser) {
          setToken(savedToken)
          setUser(JSON.parse(savedUser))
        }

        setIsLoading(false)
      })
      .catch((err) => {
        console.error('Failed to fetch system config:', err)
        // On error, continue checking local storage
        const savedToken = localStorage.getItem('auth_token')
        const savedUser = localStorage.getItem('auth_user')

        if (savedToken && savedUser) {
          setToken(savedToken)
          setUser(JSON.parse(savedUser))
        }
        setIsLoading(false)
      })
  }, [])

  // Listen for unauthorized events from httpClient (401 responses)
  useEffect(() => {
    const handleUnauthorized = () => {
      console.log('Unauthorized event received - clearing auth state')
      // Clear auth state when 401 is detected
      setUser(null)
      setToken(null)
      // Note: localStorage cleanup is already done in httpClient
    }

    window.addEventListener('unauthorized', handleUnauthorized)

    return () => {
      window.removeEventListener('unauthorized', handleUnauthorized)
    }
  }, [])

  /** Persist the session (storage + context state) without navigating. */
  const persistSession = (authToken: string, userInfo: User) => {
    reset401Flag()

    localStorage.setItem('auth_token', authToken)
    localStorage.setItem('auth_user', JSON.stringify(userInfo))
    localStorage.setItem('user_id', userInfo.id)
    flushSync(() => {
      setToken(authToken)
      setUser(userInfo)
    })
  }

  /**
   * Persist the session and navigate to the post-auth destination.
   * Used by flows whose last UI step is the login form itself.
   */
  const handlePostAuthSuccess = (authToken: string, userInfo: User) => {
    persistSession(authToken, userInfo)

    const returnUrl = sessionStorage.getItem('returnUrl')
    const nextPath = returnUrl || getPostAuthPath(null)
    if (returnUrl) {
      sessionStorage.removeItem('returnUrl')
    }

    navigate(nextPath)
  }

  const login = async (email: string, password: string, totpCode?: string) => {
    try {
      const { ok, status, data } = await postAuthEndpoint<{
        token?: string
        user_id?: string
        email?: string
        role?: string
      }>('/api/login', { email, password, totp_code: totpCode })

      if (ok && data.token) {
        handlePostAuthSuccess(data.token, {
          id: data.user_id || '',
          email: data.email || email,
          role: data.role,
        })
        return { success: true }
      }

      if (status === 403 && data.error === 'totp_binding_required') {
        return { success: false, needsBinding: true }
      }
      if (status === 403 && data.error === 'account_disabled') {
        return {
          success: false,
          message: 'This account has been disabled',
        }
      }

      return { success: false, message: data.error }
    } catch {
      return { success: false, message: 'Login failed, please try again' }
    }
  }

  // Step 1 of registration: verify the invite (if required), create the
  // pending user, and hand back the authenticator secret. No session yet.
  const register = async (email: string, password: string, inviteCode?: string) => {
    const body: Record<string, unknown> = { email, password, lang: language }
    if (inviteCode) body.invite_code = inviteCode

    try {
      const { ok, data } = await postAuthEndpoint<RegisterTotpData>(
        '/api/register',
        body
      )

      if (ok) {
        // Clear stale onboarding state so new users always see the welcome flow
        localStorage.removeItem('nofx_beginner_onboarding_completed')
        localStorage.removeItem('nofx_beginner_wallet_address')

        return { success: true, data }
      }

      return { success: false, message: data.error || 'Registration failed' }
    } catch {
      return {
        success: false,
        message: 'Registration failed, please try again',
      }
    }
  }

  // Step 2 of registration: activate with a code from the authenticator.
  // The invite is claimed server-side from the pending row — no invite_code
  // is sent here. Persists the session; the page shows the recovery codes
  // once and navigates on.
  const confirmRegister = async (
    email: string,
    password: string,
    totpCode: string
  ) => {
    try {
      const { ok, data } = await postAuthEndpoint<AuthResultData>(
        '/api/register/confirm',
        { email, password, totp_code: totpCode }
      )

      if (ok && data.token) {
        persistSession(data.token, {
          id: data.user_id,
          email: data.email,
          role: data.role,
        })
        return { success: true, data }
      }

      return { success: false, message: data.error || 'Confirmation failed' }
    } catch {
      return {
        success: false,
        message: 'Confirmation failed, please try again',
      }
    }
  }

  // Re-issue an authenticator secret for a pending/unbound account
  // (repeatable — each call replaces the pending secret).
  const startBindTotp = async (email: string, password: string) => {
    try {
      const { ok, data } = await postAuthEndpoint<{
        totp_secret: string
        otpauth_url: string
      }>('/api/auth/bind-totp', { email, password })

      if (ok) {
        return { success: true, data }
      }
      if (data.error === 'account_disabled') {
        return {
          success: false,
          message: 'This account has been disabled',
        }
      }
      return { success: false, message: data.error || 'Failed to start binding' }
    } catch {
      return { success: false, message: 'Binding failed, please try again' }
    }
  }

  // Confirm the binding: enables TOTP, issues recovery codes and a session.
  // Persists the session; the page shows the recovery codes once and
  // navigates on.
  const confirmBindTotp = async (
    email: string,
    password: string,
    totpCode: string
  ) => {
    try {
      const { ok, data } = await postAuthEndpoint<AuthResultData>(
        '/api/auth/bind-totp/confirm',
        { email, password, totp_code: totpCode }
      )

      if (ok && data.token) {
        persistSession(data.token, {
          id: data.user_id,
          email: data.email,
          role: data.role,
        })
        return { success: true, data: { recovery_codes: data.recovery_codes } }
      }
      if (data.error === 'account_disabled') {
        return {
          success: false,
          message: 'This account has been disabled',
        }
      }
      return { success: false, message: data.error || 'Invalid code' }
    } catch {
      return { success: false, message: 'Binding failed, please try again' }
    }
  }

  // NOTE: in-browser password reset was removed. Recovery now runs as a local
  // CLI command on the server (`nofx reset-password`), so it cannot be triggered
  // remotely. The reset-password page shows the operator how to run it.

  const logout = async () => {
    const savedToken = localStorage.getItem('auth_token')
    if (savedToken) {
      try {
        const response = await fetch('/api/logout', {
          method: 'POST',
          headers: { Authorization: `Bearer ${savedToken}` },
        })
        // A password change or an expired/revoked session is already invalid.
        if (!response.ok && response.status !== 401) {
          throw new Error('Session revocation failed')
        }
      } catch {
        toast.error('Unable to confirm sign-out with the server. Please retry.')
        return
      }
    }
    setUser(null)
    setToken(null)
    localStorage.removeItem('auth_token')
    localStorage.removeItem('auth_user')
    invalidateSystemConfig()
    navigate(ROUTES.home)
  }

  return (
    <AuthContext.Provider
      value={{
        user,
        token,
        login,
        register,
        confirmRegister,
        startBindTotp,
        confirmBindTotp,
        logout,
        isLoading,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const context = useContext(AuthContext)
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
