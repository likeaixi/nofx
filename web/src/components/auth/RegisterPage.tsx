import React, { useEffect, useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import PasswordChecklist from 'react-password-checklist'
import { toast } from 'sonner'
import { QRCodeSVG } from 'qrcode.react'
import { useAuth } from '../../contexts/AuthContext'
import { useLanguage } from '../../contexts/LanguageContext'
import { t } from '../../i18n/translations'
import { getSystemConfig } from '../../lib/config'
import { getPostAuthPath } from '../../lib/onboarding'
import { BrandLogo } from '../common/BrandLogo'
import { DeepVoidBackground } from '../common/DeepVoidBackground'

type Step = 'form' | 'totp' | 'recovery'

export function RegisterPage() {
  const { language } = useLanguage()
  const { register, confirmRegister } = useAuth()
  const navigate = useNavigate()
  const [step, setStep] = useState<Step>('form')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [inviteRequired, setInviteRequired] = useState(false)
  const [totpSecret, setTotpSecret] = useState('')
  const [totpUrl, setTotpUrl] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [isFirstUser, setIsFirstUser] = useState(false)
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [passwordValid, setPasswordValid] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [showConfirmPassword, setShowConfirmPassword] = useState(false)

  useEffect(() => {
    getSystemConfig()
      .then((config) => {
        setInviteRequired(config.invite_required === true)
      })
      .catch((err) => {
        console.error('Failed to fetch system config:', err)
      })
  }, [])

  // Step 1: create the pending account and receive the authenticator secret.
  const handleRegister = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (!passwordValid) {
      setError(t('passwordNotMeetRequirements', language))
      return
    }

    if (inviteRequired && !inviteCode.trim()) {
      setError('An invite code is required')
      return
    }

    setLoading(true)
    try {
      const result = await register(email, password, inviteCode.trim() || undefined)

      if (!result.success || !result.data) {
        const msg = result.message || t('registrationFailed', language)
        setError(msg)
        toast.error(msg)
        return
      }

      setTotpSecret(result.data.totp_secret)
      setTotpUrl(result.data.otpauth_url)
      setIsFirstUser(result.data.is_first_user === true)
      setStep('totp')
    } catch (e) {
      console.error('Registration error:', e)
      const errorMsg =
        e instanceof Error ? e.message : 'Registration failed due to server error'
      setError(errorMsg)
      toast.error(errorMsg)
    } finally {
      setLoading(false)
    }
  }

  // Step 2: confirm with a code from the authenticator. On success the
  // session is already persisted by AuthContext; recovery codes show once.
  const handleConfirmTotp = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')

    if (totpCode.trim().length !== 6) {
      setError('Enter the 6-digit code from your authenticator')
      return
    }

    setLoading(true)
    try {
      const result = await confirmRegister(email, password, totpCode.trim())

      if (!result.success || !result.data) {
        const msg = result.message || 'Confirmation failed'
        setError(msg)
        toast.error(msg)
        return
      }

      setRecoveryCodes(result.data.recovery_codes || [])
      setStep('recovery')
    } catch (e) {
      console.error('Registration confirm error:', e)
      const errorMsg =
        e instanceof Error ? e.message : 'Confirmation failed due to server error'
      setError(errorMsg)
      toast.error(errorMsg)
    } finally {
      setLoading(false)
    }
  }

  // Step 3: recovery codes were shown — enter the workspace.
  const handleEnterWorkspace = () => {
    const returnUrl = sessionStorage.getItem('returnUrl')
    if (returnUrl) {
      sessionStorage.removeItem('returnUrl')
      navigate(returnUrl)
      return
    }
    navigate(getPostAuthPath(isFirstUser ? 'beginner' : undefined))
  }

  const copyRecoveryCodes = () => {
    navigator.clipboard
      ?.writeText(recoveryCodes.join('\n'))
      .then(() => toast.success('Recovery codes copied'))
      .catch(() => toast.error('Could not copy — select and copy manually'))
  }

  const inputCls =
    'w-full bg-nofx-bg border border-[rgba(25,36,52,0.14)] rounded-lg px-4 py-3 text-sm focus:border-nofx-gold focus:ring-1 focus:ring-nofx-gold/50 outline-hidden transition-all placeholder-nofx-text-muted text-nofx-text font-sans'

  return (
    <DeepVoidBackground
      className="min-h-screen flex items-center justify-center py-12 font-sans nofx-auth"
      disableAnimation
    >
      <div className="w-full max-w-lg relative z-10 px-6">
        <div className="flex justify-between items-center mb-8">
          <button
            onClick={() => navigate('/')}
            className="flex items-center gap-2 text-nofx-text-muted hover:text-nofx-text transition-colors group px-3 py-1.5 rounded-lg border border-transparent hover:border-[rgba(25,36,52,0.14)] bg-nofx-bg-deeper backdrop-blur-xs"
          >
            <span className="text-xs font-sans tracking-normal">
              Back to home
            </span>
          </button>
        </div>

        <div className="mb-8 text-center">
          <div className="flex justify-center mb-6">
            <BrandLogo />
          </div>
          {step === 'form' && (
            <>
              <h1 className="text-3xl font-bold tracking-tighter text-nofx-text mb-2">
                Create your account
              </h1>
              <p className="text-nofx-text-muted text-sm">
                Set up your local NOFX workspace. Connect your AI wallet and trading
                accounts when you are ready.
              </p>
            </>
          )}
          {step === 'totp' && (
            <>
              <h1 className="text-3xl font-bold tracking-tighter text-nofx-text mb-2">
                Secure your account
              </h1>
              <p className="text-nofx-text-muted text-sm">
                Scan the QR code with Google Authenticator (or a compatible app),
                then enter the 6-digit code it shows.
              </p>
            </>
          )}
          {step === 'recovery' && (
            <>
              <h1 className="text-3xl font-bold tracking-tighter text-nofx-text mb-2">
                Save your recovery codes
              </h1>
              <p className="text-nofx-text-muted text-sm">
                Each code works once if you ever lose access to your authenticator.
              </p>
            </>
          )}
        </div>

        <div className="bg-nofx-bg-lighter backdrop-blur-md border border-[rgba(25,36,52,0.14)] rounded-lg overflow-hidden shadow-lg relative group">
          <div className="p-6 md:p-8 relative">
            {step === 'form' && (
              <form onSubmit={handleRegister} className="space-y-5">
                <div>
                  <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                    {t('email', language)}
                  </label>
                  <input
                    type="email"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    className={inputCls}
                    placeholder="you@example.com"
                    required
                  />
                </div>

                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <div>
                    <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                      {t('password', language)}
                    </label>
                    <div className="relative">
                      <input
                        type={showPassword ? 'text' : 'password'}
                        value={password}
                        onChange={(e) => setPassword(e.target.value)}
                        className={inputCls + ' pr-10'}
                        placeholder="••••••••"
                        required
                      />
                      <button
                        type="button"
                        onClick={() => setShowPassword(!showPassword)}
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-nofx-text-muted hover:text-nofx-text transition-colors"
                      >
                        {showPassword ? <EyeOff size={16} /> : <Eye size={16} />}
                      </button>
                    </div>
                  </div>

                  <div>
                    <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                      {t('confirmPassword', language)}
                    </label>
                    <div className="relative">
                      <input
                        type={showConfirmPassword ? 'text' : 'password'}
                        value={confirmPassword}
                        onChange={(e) => setConfirmPassword(e.target.value)}
                        className={inputCls + ' pr-10'}
                        placeholder="••••••••"
                        required
                      />
                      <button
                        type="button"
                        onClick={() =>
                          setShowConfirmPassword(!showConfirmPassword)
                        }
                        className="absolute right-3 top-1/2 -translate-y-1/2 text-nofx-text-muted hover:text-nofx-text transition-colors"
                      >
                        {showConfirmPassword ? (
                          <EyeOff size={16} />
                        ) : (
                          <Eye size={16} />
                        )}
                      </button>
                    </div>
                  </div>
                </div>

                <div className="bg-nofx-bg-deeper p-3 rounded-lg border border-[rgba(25,36,52,0.14)]">
                  <div className="text-[10px] tracking-normal text-nofx-text-muted mb-2 font-bold flex items-center gap-2">
                    <div className="w-1 h-1 rounded-full bg-nofx-text-muted"></div>
                    Password requirements
                  </div>
                  <div className="text-xs font-sans text-nofx-text-muted">
                    <PasswordChecklist
                      rules={[
                        'minLength',
                        'capital',
                        'lowercase',
                        'number',
                        'specialChar',
                        'match',
                      ]}
                      minLength={8}
                      value={password}
                      valueAgain={confirmPassword}
                      messages={{
                        minLength: t('passwordRuleMinLength', language),
                        capital: t('passwordRuleUppercase', language),
                        lowercase: t('passwordRuleLowercase', language),
                        number: t('passwordRuleNumber', language),
                        specialChar: t('passwordRuleSpecial', language),
                        match: t('passwordRuleMatch', language),
                      }}
                      className="grid grid-cols-2 gap-x-4 gap-y-1"
                      onChange={(isValid) => setPasswordValid(isValid)}
                      iconSize={10}
                    />
                  </div>
                </div>

                {inviteRequired && (
                  <div>
                    <label className="block text-xs tracking-normal text-nofx-gold mb-1.5 ml-1 font-bold">
                      Invite code
                    </label>
                    <input
                      type="text"
                      value={inviteCode}
                      onChange={(e) =>
                        setInviteCode(
                          e.target.value.replace(/[^a-z0-9]/gi, '').toUpperCase()
                        )
                      }
                      className={inputCls + ' tracking-widest'}
                      placeholder="XXXXXXXXXX"
                      maxLength={10}
                      required={inviteRequired}
                    />
                    <p className="text-[10px] text-nofx-text-muted font-sans mt-1 ml-1">
                      Registration is invite-only. Ask the workspace owner for a
                      code.
                    </p>
                  </div>
                )}

                {error && (
                  <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg font-sans">
                    {error}
                  </div>
                )}

                <button
                  type="submit"
                  disabled={
                    loading ||
                    (inviteRequired && !inviteCode.trim()) ||
                    !passwordValid
                  }
                  className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 px-4 rounded-lg text-sm tracking-normal hover:bg-nofx-gold-highlight transition-all transform active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed font-sans flex items-center justify-center gap-2 group mt-4"
                >
                  {loading ? (
                    <span className="animate-pulse">Creating account…</span>
                  ) : (
                    <>
                      <span>Create account</span>
                      <span className="group-hover:translate-x-1 transition-transform">
                        -&gt;
                      </span>
                    </>
                  )}
                </button>
              </form>
            )}

            {step === 'totp' && (
              <form onSubmit={handleConfirmTotp} className="space-y-5">
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
                <div>
                  <label className="block text-xs tracking-normal text-nofx-text-muted mb-1.5 ml-1 font-bold">
                    Authenticator code
                  </label>
                  <input
                    type="text"
                    inputMode="numeric"
                    value={totpCode}
                    onChange={(e) =>
                      setTotpCode(e.target.value.replace(/\D/g, '').slice(0, 6))
                    }
                    className={inputCls + ' text-center tracking-widest'}
                    placeholder="123456"
                    maxLength={6}
                    required
                    autoFocus
                  />
                </div>

                {error && (
                  <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg font-sans">
                    {error}
                  </div>
                )}

                <button
                  type="submit"
                  disabled={loading || totpCode.trim().length !== 6}
                  className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 px-4 rounded-lg text-sm tracking-normal hover:bg-nofx-gold-highlight transition-all transform active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed font-sans flex items-center justify-center gap-2 group mt-4"
                >
                  {loading ? (
                    <span className="animate-pulse">Confirming…</span>
                  ) : (
                    <>
                      <span>Confirm</span>
                      <span className="group-hover:translate-x-1 transition-transform">
                        -&gt;
                      </span>
                    </>
                  )}
                </button>
              </form>
            )}

            {step === 'recovery' && (
              <div className="space-y-5">
                <div className="text-xs bg-nofx-danger/10 border border-nofx-danger/30 text-nofx-danger px-3 py-2 rounded-lg font-sans">
                  These recovery codes are shown only once. Save them somewhere
                  safe — each one can sign you in a single time if you lose your
                  authenticator.
                </div>
                <div className="bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-lg p-4 font-mono text-sm grid grid-cols-2 gap-2">
                  {recoveryCodes.map((code) => (
                    <div key={code} className="text-nofx-text break-all">
                      {code}
                    </div>
                  ))}
                </div>
                <button
                  type="button"
                  onClick={copyRecoveryCodes}
                  className="w-full bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] text-nofx-text font-bold py-2.5 px-4 rounded-lg text-xs tracking-normal hover:border-nofx-gold/50 transition-all font-sans"
                >
                  Copy all codes
                </button>
                <button
                  type="button"
                  onClick={handleEnterWorkspace}
                  className="w-full bg-nofx-gold text-nofx-bg font-bold py-3 px-4 rounded-lg text-sm tracking-normal hover:bg-nofx-gold-highlight transition-all transform active:scale-[0.98] font-sans flex items-center justify-center gap-2 group"
                >
                  <span>I saved them — enter workspace</span>
                  <span className="group-hover:translate-x-1 transition-transform">
                    -&gt;
                  </span>
                </button>
              </div>
            )}
          </div>
        </div>

        <div className="text-center mt-8 space-y-4">
          {step === 'form' && (
            <p className="text-xs font-sans text-nofx-text-muted">
              Already have an account?{' '}
              <button
                onClick={() => navigate('/login')}
                className="text-nofx-gold hover:underline hover:text-nofx-gold-highlight transition-colors ml-1"
              >
                Sign in
              </button>
            </p>
          )}
          <button
            onClick={() => navigate('/')}
            className="text-[10px] text-nofx-text-muted hover:text-nofx-danger transition-colors tracking-normal hover:underline decoration-nofx-danger/30 font-sans"
          >
            Back to home
          </button>
        </div>
      </div>
    </DeepVoidBackground>
  )
}
