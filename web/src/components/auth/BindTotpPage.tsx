import React, { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { QRCodeSVG } from 'qrcode.react'
import { toast } from 'sonner'
import { useAuth } from '../../contexts/AuthContext'
import { getPostAuthPath } from '../../lib/onboarding'
import { ROUTES } from '../../router/paths'
import { BrandLogo } from '../common/BrandLogo'
import { DeepVoidBackground } from '../common/DeepVoidBackground'

/**
 * Bind an authenticator to a pending/unbound account. Reached from the login
 * page when the backend answers `totp_binding_required`: the entered email and
 * password are carried over via router state (the bind endpoints re-verify
 * the password server-side).
 */
export function BindTotpPage() {
  const location = useLocation() as { state?: { email?: string; password?: string } }
  const navigate = useNavigate()
  const { startBindTotp, confirmBindTotp } = useAuth()
  const [email, setEmail] = useState(location.state?.email ?? '')
  const [password, setPassword] = useState(location.state?.password ?? '')
  const [totpSecret, setTotpSecret] = useState('')
  const [totpUrl, setTotpUrl] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleBind = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const result = await startBindTotp(email, password)
      if (result.success && result.data) {
        setTotpSecret(result.data.totp_secret)
        setTotpUrl(result.data.otpauth_url)
      } else {
        setError(result.message || 'Failed to start binding')
      }
    } finally {
      setLoading(false)
    }
  }

  const handleConfirm = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    if (totpCode.trim().length !== 6) {
      setError('Enter the 6-digit code from your authenticator')
      return
    }
    setLoading(true)
    try {
      const result = await confirmBindTotp(email, password, totpCode.trim())
      if (result.success && result.data) {
        setRecoveryCodes(result.data.recovery_codes || [])
      } else {
        setError(result.message || 'Invalid code')
      }
    } finally {
      setLoading(false)
    }
  }

  const handleContinue = () => {
    const returnUrl = sessionStorage.getItem('returnUrl')
    if (returnUrl) {
      sessionStorage.removeItem('returnUrl')
      navigate(returnUrl)
      return
    }
    navigate(getPostAuthPath(null))
  }

  const inputCls =
    'w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden text-nofx-text font-sans'

  return (
    <DeepVoidBackground
      className="min-h-screen flex items-center justify-center py-12 font-sans nofx-auth"
      disableAnimation
    >
      <div className="w-full max-w-md px-6 relative z-10">
        <div className="flex justify-center mb-6">
          <BrandLogo />
        </div>
        {!recoveryCodes && (
          <>
            <h1 className="text-2xl font-bold text-nofx-text mb-2 text-center">
              Bind your authenticator
            </h1>
            <p className="text-nofx-text-muted text-sm text-center mb-6">
              Two-factor authentication is required before you can sign in. Scan
              the QR code with Google Authenticator (or a compatible app).
            </p>
          </>
        )}
        {!totpUrl && !recoveryCodes && (
          <form onSubmit={handleBind} className="space-y-4">
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="Email"
              className={inputCls}
              required
              autoFocus
            />
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Password"
              className={inputCls}
              required
            />
            {error && (
              <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg">
                {error}
              </div>
            )}
            <button
              type="submit"
              disabled={loading}
              className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm"
            >
              {loading ? '…' : 'Continue'}
            </button>
          </form>
        )}
        {totpUrl && !recoveryCodes && (
          <form onSubmit={handleConfirm} className="space-y-4">
            <div className="flex justify-center bg-white p-3 rounded-lg w-fit mx-auto">
              <QRCodeSVG value={totpUrl} size={180} />
            </div>
            <p className="text-[10px] text-nofx-text-muted font-sans text-center">
              Cannot scan? Enter this secret manually in your authenticator
              app:{' '}
              <span className="font-mono text-nofx-text break-all">
                {totpSecret}
              </span>
            </p>
            <input
              value={totpCode}
              onChange={(e) =>
                setTotpCode(e.target.value.replace(/\D/g, '').slice(0, 6))
              }
              placeholder="6-digit code"
              maxLength={6}
              inputMode="numeric"
              className={inputCls + ' text-center tracking-widest'}
              required
              autoFocus
            />
            {error && (
              <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg">
                {error}
              </div>
            )}
            <button
              type="submit"
              disabled={loading}
              className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm"
            >
              {loading ? '…' : 'Confirm'}
            </button>
          </form>
        )}
        {recoveryCodes && (
          <div className="space-y-4">
            <h1 className="text-2xl font-bold text-nofx-text mb-2 text-center">
              Save your recovery codes
            </h1>
            <div className="bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger text-xs px-3 py-2 rounded-lg">
              Save these recovery codes now — they are shown only once.
            </div>
            <div className="bg-nofx-bg-lighter border border-[rgba(25,36,52,0.14)] rounded-lg p-4 font-mono text-sm grid grid-cols-2 gap-2">
              {recoveryCodes.map((c) => (
                <div key={c} className="text-nofx-text break-all">
                  {c}
                </div>
              ))}
            </div>
            <button
              onClick={() => {
                toast.success('Saved')
                handleContinue()
              }}
              className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 rounded-lg text-sm"
            >
              I saved them — continue
            </button>
          </div>
        )}
        <div className="text-center mt-6">
          <button
            onClick={() => navigate(ROUTES.login)}
            className="text-xs text-nofx-text-muted hover:text-nofx-gold"
          >
            Back to login
          </button>
        </div>
      </div>
    </DeepVoidBackground>
  )
}
