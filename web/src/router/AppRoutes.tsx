import { type ReactNode, useEffect, useState } from 'react'
import { AnimatePresence, motion } from 'framer-motion'
import useSWR from 'swr'
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
  useSearchParams,
} from 'react-router-dom'
import HeaderBar from '../components/common/HeaderBar'
import { SiteFooter } from '../components/common/SiteFooter'
import { LoginRequiredOverlay } from '../components/auth/LoginRequiredOverlay'
import { LoginPage } from '../components/auth/LoginPage'
import { RegisterPage } from '../components/auth/RegisterPage'
import { BindTotpPage } from '../components/auth/BindTotpPage'
import { ResetPasswordPage } from '../components/auth/ResetPasswordPage'
import { CompetitionPage } from '../components/trader/CompetitionPage'
import { AITradersPage } from '../components/trader/AITradersPage'
import { TraderLaunchGuestPage } from '../components/trader/TraderLaunchGuestPage'
import { FAQPage } from '../pages/FAQPage'
import { LandingPage } from '../pages/LandingPage'
import { DataPage } from '../pages/DataPage'
import { SettingsPage } from '../pages/SettingsPage'
import { StrategyStudioPage } from '../pages/StrategyStudioPage'
import { TradingPlanningPage } from '../pages/TradingPlanningPage'
import { TradeLedgerPage } from '../pages/TradeLedgerPage'
import { TraderDetails } from '../components/trader/TraderDetails'
import { TerminalDashboard } from '../components/terminal/TerminalDashboard'
import { useAuth } from '../contexts/AuthContext'
import { useLanguage } from '../contexts/LanguageContext'
import { useSystemConfig } from '../hooks/useSystemConfig'
import { t } from '../i18n/translations'
import { api } from '../lib/api'
import type {
  AccountInfo,
  DecisionRecord,
  Position,
  SystemStatus,
  TraderInfo,
} from '../types'
import {
  buildDashboardPath,
  LEGACY_HASH_ROUTES,
  ROUTES,
  type Page,
} from './paths'

function FrontierLoginRedirect() {
  useEffect(() => {
    sessionStorage.setItem('returnUrl', ROUTES.planning)
  }, [])
  return <Navigate to={ROUTES.login} replace />
}

function getTraderSlug(trader: TraderInfo) {
  const idPrefix = trader.trader_id.slice(0, 4)
  return `${trader.trader_name}-${idPrefix}`
}

function findTraderBySlug(slug: string, traderList: TraderInfo[]) {
  const lastDashIndex = slug.lastIndexOf('-')
  if (lastDashIndex === -1) {
    return traderList.find((trader) => trader.trader_name === slug)
  }

  const name = slug.slice(0, lastDashIndex)
  const idPrefix = slug.slice(lastDashIndex + 1)
  return traderList.find(
    (trader) =>
      trader.trader_name === name && trader.trader_id.startsWith(idPrefix)
  )
}

function LoadingScreen() {
  const { language } = useLanguage()

  return (
    <div
      className="min-h-screen flex items-center justify-center"
      style={{ background: '#f6f8fc' }}
    >
      <div className="text-center">
        <img
          src="/icons/nofx-flow.svg"
          alt="NoFx Logo"
          className="w-16 h-16 mx-auto mb-4 animate-pulse"
        />
        <p style={{ color: '#192434' }}>{t('loading', language)}</p>
      </div>
    </div>
  )
}

function LegacyHashRedirect() {
  const location = useLocation()
  const navigate = useNavigate()

  useEffect(() => {
    const hashRoute = LEGACY_HASH_ROUTES[location.hash.slice(1)]
    if (!hashRoute) {
      return
    }

    if (hashRoute === location.pathname && location.hash === '') {
      return
    }

    navigate(
      {
        pathname: hashRoute,
        search: location.search,
      },
      { replace: true }
    )
  }, [location.hash, location.pathname, location.search, navigate])

  return null
}

interface AppChromeProps {
  children: ReactNode
  currentPage?: Page
  showFooter?: boolean
  wrapInMain?: boolean
  animateContent?: boolean
  extraContent?: ReactNode
}

function AppChrome({
  children,
  currentPage,
  showFooter = true,
  wrapInMain = true,
  animateContent = false,
  extraContent,
}: AppChromeProps) {
  const location = useLocation()
  const { language, setLanguage } = useLanguage()
  const { user, logout } = useAuth()
  const [loginOverlayOpen, setLoginOverlayOpen] = useState(false)
  const [loginOverlayFeature, setLoginOverlayFeature] = useState('')

  const handleLoginRequired = (featureName: string) => {
    setLoginOverlayFeature(featureName)
    setLoginOverlayOpen(true)
  }

  const content = animateContent ? (
    <AnimatePresence mode="wait">
      <motion.div
        key={`${location.pathname}${location.search}`}
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        exit={{ opacity: 0, y: -8 }}
        transition={{ duration: 0.15, ease: 'easeOut' }}
      >
        {children}
      </motion.div>
    </AnimatePresence>
  ) : (
    children
  )

  return (
    <div
      className="nofx-shell min-h-screen"
      style={{ background: '#f6f8fc', color: '#192434' }}
    >
      <HeaderBar
        isLoggedIn={!!user}
        currentPage={currentPage}
        language={language}
        onLanguageChange={setLanguage}
        user={user}
        onLogout={logout}
        onLoginRequired={handleLoginRequired}
      />

      {wrapInMain ? (
        <main className="min-h-screen pt-[72px]">{content}</main>
      ) : (
        content
      )}

      {showFooter ? <SiteFooter language={language} /> : null}

      <LoginRequiredOverlay
        isOpen={loginOverlayOpen}
        onClose={() => setLoginOverlayOpen(false)}
        featureName={loginOverlayFeature}
      />

      {extraContent}
    </div>
  )
}

function TradersRoute() {
  const navigate = useNavigate()
  const { user, token } = useAuth()
  const { data: traders } = useSWR<TraderInfo[]>(
    user && token ? 'traders-route' : null,
    api.getTraders,
    {
      refreshInterval: 5000,
      shouldRetryOnError: false,
    }
  )

  return (
    <AppChrome currentPage="traders" animateContent>
      <AITradersPage
        onTraderSelect={(traderId) => {
          const trader = traders?.find((item) => item.trader_id === traderId)
          navigate(
            buildDashboardPath(trader ? getTraderSlug(trader) : undefined)
          )
        }}
      />
    </AppChrome>
  )
}

function TradeDetailsRoute() {
  const { language } = useLanguage()
  const zh = language === 'zh'
  const [searchParams, setSearchParams] = useSearchParams()
  const {
    data: traders,
    error,
    isLoading,
    mutate,
  } = useSWR<TraderInfo[]>('traders-dashboard', () => api.getTraders(true), {
    refreshInterval: 10000,
    shouldRetryOnError: false,
  })
  const slug = searchParams.get('trader')
  const selectedTrader =
    traders && (slug ? findTraderBySlug(slug, traders) : traders[0])
  const traderId = selectedTrader?.trader_id
  const { data: status } = useSWR<SystemStatus>(
    traderId ? `status-${traderId}` : null,
    () => api.getStatus(traderId!, true),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      dedupingInterval: 10000,
    }
  )
  return (
    <AppChrome currentPage="trade-details">
      <div className="nofx-trade-details-page">
        <div className="nofx-trade-details-heading">
          <h1 className="text-2xl font-semibold">
            {zh ? '交易详情' : 'Trade Details'}
          </h1>
          {!!traders?.length && (
            <label className="flex items-center gap-3">
              <span>{zh ? '交易员' : 'Trader'}</span>
              <select
                className="border rounded px-3 py-2 bg-white"
                value={traderId || ''}
                onChange={(event) => {
                  const trader = traders.find(
                    (item) => item.trader_id === event.target.value
                  )
                  if (trader) setSearchParams({ trader: getTraderSlug(trader) })
                }}
              >
                {!selectedTrader && (
                  <option value="" disabled>
                    {zh ? '请选择交易员' : 'Select a trader'}
                  </option>
                )}
                {traders.map((trader) => (
                  <option key={trader.trader_id} value={trader.trader_id}>
                    {trader.trader_name}
                  </option>
                ))}
              </select>
            </label>
          )}
        </div>
        {isLoading ? (
          <p role="status">{zh ? '正在加载…' : 'Loading…'}</p>
        ) : error ? (
          <button onClick={() => mutate()}>
            {zh ? '读取失败，点击重试' : 'Could not load traders. Retry'}
          </button>
        ) : !traders?.length ? (
          <p>
            {zh
              ? '暂无交易员，请先在配置页面创建。'
              : 'No traders yet. Create one in Configuration.'}
          </p>
        ) : !selectedTrader ? (
          <p>
            {zh
              ? '未找到指定交易员，请重新选择。'
              : 'Trader not found. Please select a trader.'}
          </p>
        ) : (
          <TraderDetails
            key={traderId}
            traderId={traderId!}
            strategyType={status?.strategy_type}
          />
        )}
      </div>
    </AppChrome>
  )
}

function DashboardRoute() {
  const { user, token } = useAuth()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const selectedTraderSlug = searchParams.get('trader') || undefined
  const [selectedTraderId, setSelectedTraderId] = useState<string | undefined>()
  const [, setLastUpdate] = useState<string>('--:--:--')
  const [decisionsLimit] = useState(5)
  const [accountPollOff, setAccountPollOff] = useState(false)
  const [positionsPollOff, setPositionsPollOff] = useState(false)
  const [decisionsPollOff, setDecisionsPollOff] = useState(false)

  useEffect(() => {
    setAccountPollOff(false)
    setPositionsPollOff(false)
    setDecisionsPollOff(false)
  }, [selectedTraderId])

  const { data: traders } = useSWR<TraderInfo[]>(
    user && token ? 'traders-dashboard' : null,
    () => api.getTraders(true),
    {
      refreshInterval: 10000,
      shouldRetryOnError: false,
    }
  )

  useEffect(() => {
    if (!traders || traders.length === 0) {
      return
    }

    if (selectedTraderSlug) {
      const trader = findTraderBySlug(selectedTraderSlug, traders)
      const nextTraderId = trader?.trader_id || traders[0].trader_id
      if (nextTraderId !== selectedTraderId) {
        setSelectedTraderId(nextTraderId)
      }
      return
    }

    if (!selectedTraderId) {
      setSelectedTraderId(traders[0].trader_id)
    }
  }, [selectedTraderId, selectedTraderSlug, traders])

  const { data: status } = useSWR<SystemStatus>(
    selectedTraderId ? `status-${selectedTraderId}` : null,
    () => api.getStatus(selectedTraderId, true),
    {
      refreshInterval: 15000,
      revalidateOnFocus: false,
      dedupingInterval: 10000,
    }
  )

  const { data: account } = useSWR<AccountInfo>(
    selectedTraderId ? `account-${selectedTraderId}` : null,
    () => api.getAccount(selectedTraderId, true),
    {
      refreshInterval: accountPollOff ? 0 : 15000,
      revalidateOnFocus: false,
      dedupingInterval: 10000,
      onErrorRetry: (_err, _key, _config, revalidate, { retryCount }) => {
        if (retryCount >= 2) {
          setAccountPollOff(true)
          return
        }
        setTimeout(() => revalidate({ retryCount }), 500)
      },
      onSuccess: () => {
        if (accountPollOff) {
          setAccountPollOff(false)
        }
      },
    }
  )

  const { data: positions } = useSWR<Position[]>(
    selectedTraderId ? `positions-${selectedTraderId}` : null,
    () => api.getPositions(selectedTraderId, true),
    {
      refreshInterval: positionsPollOff ? 0 : 15000,
      revalidateOnFocus: false,
      dedupingInterval: 10000,
      onErrorRetry: (_err, _key, _config, revalidate, { retryCount }) => {
        if (retryCount >= 2) {
          setPositionsPollOff(true)
          return
        }
        setTimeout(() => revalidate({ retryCount }), 500)
      },
      onSuccess: () => {
        if (positionsPollOff) {
          setPositionsPollOff(false)
        }
      },
    }
  )

  const { data: decisions } = useSWR<DecisionRecord[]>(
    selectedTraderId
      ? `decisions/latest-${selectedTraderId}-${decisionsLimit}`
      : null,
    () => api.getLatestDecisions(selectedTraderId, decisionsLimit, true),
    {
      refreshInterval: decisionsPollOff ? 0 : 30000,
      revalidateOnFocus: false,
      dedupingInterval: 20000,
      onErrorRetry: (_err, _key, _config, revalidate, { retryCount }) => {
        if (retryCount >= 2) {
          setDecisionsPollOff(true)
          return
        }
        setTimeout(() => revalidate({ retryCount }), 500)
      },
      onSuccess: () => {
        if (decisionsPollOff) {
          setDecisionsPollOff(false)
        }
      },
    }
  )

  useEffect(() => {
    if (account) {
      setLastUpdate(new Date().toLocaleTimeString())
    }
  }, [account])

  const selectedTrader = traders?.find(
    (trader) => trader.trader_id === selectedTraderId
  )

  return (
    <AppChrome currentPage="trader" animateContent>
      <TerminalDashboard
        selectedTrader={selectedTrader}
        status={status}
        account={account}
        positions={positions}
        decisions={decisions}
        traders={traders}
        selectedTraderId={selectedTraderId}
        onTraderSelect={(traderId) => {
          setSelectedTraderId(traderId)
          const trader = traders?.find((item) => item.trader_id === traderId)
          navigate(
            buildDashboardPath(trader ? getTraderSlug(trader) : undefined),
            {
              replace: true,
            }
          )
        }}
      />
    </AppChrome>
  )
}

export function AppRoutes() {
  const { user, token, isLoading } = useAuth()
  const { config: systemConfig, loading: configLoading } = useSystemConfig()
  const { pathname } = useLocation()
  useEffect(() => {
    window.scrollTo({ top: 0, left: 0, behavior: 'instant' })
  }, [pathname])
  const isAuthenticated = !!user && !!token

  if (isLoading || configLoading) {
    return <LoadingScreen />
  }

  return (
    <>
      <LegacyHashRedirect />
      <Routes>
        <Route
          path="/planning"
          element={<Navigate to={ROUTES.planning} replace />}
        />
        <Route
          path={ROUTES.planning}
          element={
            isAuthenticated ? (
              <AppChrome
                currentPage="planning"
                showFooter={false}
                wrapInMain={false}
              >
                <TradingPlanningPage />
              </AppChrome>
            ) : (
              <FrontierLoginRedirect />
            )
          }
        />

        <Route path={ROUTES.home} element={<LandingPage />} />
        <Route path={ROUTES.login} element={<LoginPage />} />
        <Route path={ROUTES.register} element={<RegisterPage />} />
        <Route path={ROUTES.bindTotp} element={<BindTotpPage />} />
        <Route path={ROUTES.resetPassword} element={<ResetPasswordPage />} />
        <Route
          path={ROUTES.setup}
          element={
            // Registration is always open (invite-gated once initialized), so
            // first-run setup funnels into the standard register flow.
            user ? (
              <Navigate to={ROUTES.welcome} replace />
            ) : systemConfig?.initialized ? (
              <Navigate to={ROUTES.login} replace />
            ) : (
              <Navigate to={ROUTES.register} replace />
            )
          }
        />
        <Route
          path={ROUTES.faq}
          element={
            <AppChrome currentPage="faq" showFooter={false} wrapInMain={false}>
              <FAQPage />
            </AppChrome>
          }
        />
        <Route
          path={ROUTES.data}
          element={
            <AppChrome currentPage="data" showFooter={false}>
              <DataPage />
            </AppChrome>
          }
        />
        <Route
          path={ROUTES.settings}
          element={
            isAuthenticated ? (
              <AppChrome showFooter={false}>
                <SettingsPage />
              </AppChrome>
            ) : (
              <Navigate to={ROUTES.login} replace />
            )
          }
        />
        <Route
          path={ROUTES.welcome}
          element={
            // Post-registration landing page for the first owner (the guided
            // AI-wallet deposit overlay was removed with wallet onboarding;
            // the traders page itself is the welcome screen).
            isAuthenticated ? (
              <TradersRoute />
            ) : (
              <Navigate to={ROUTES.login} replace />
            )
          }
        />
        <Route
          path={ROUTES.competition}
          element={
            isAuthenticated ? (
              <AppChrome currentPage="competition" animateContent>
                <CompetitionPage />
              </AppChrome>
            ) : (
              <LandingPage />
            )
          }
        />
        <Route
          path={ROUTES.strategyMarket}
          element={
            isAuthenticated ? (
              <Navigate to={ROUTES.strategy} replace />
            ) : (
              <LandingPage />
            )
          }
        />
        <Route
          path={ROUTES.traders}
          element={
            isAuthenticated ? (
              <TradersRoute />
            ) : (
              <AppChrome
                currentPage="traders"
                showFooter={false}
                animateContent
              >
                <TraderLaunchGuestPage />
              </AppChrome>
            )
          }
        />
        <Route
          path={ROUTES.tradeLedger}
          element={
            isAuthenticated ? (
              <AppChrome currentPage="trade-ledger">
                <TradeLedgerPage />
              </AppChrome>
            ) : (
              <Navigate to={ROUTES.login} replace />
            )
          }
        />
        <Route
          path={ROUTES.tradeDetails}
          element={
            isAuthenticated ? (
              <TradeDetailsRoute />
            ) : (
              <Navigate to={ROUTES.login} replace />
            )
          }
        />
        <Route
          path={ROUTES.dashboard}
          element={isAuthenticated ? <DashboardRoute /> : <LandingPage />}
        />
        <Route
          path={ROUTES.strategy}
          element={
            isAuthenticated ? (
              <AppChrome currentPage="strategy" animateContent>
                <StrategyStudioPage />
              </AppChrome>
            ) : (
              <LandingPage />
            )
          }
        />
        <Route path="*" element={<Navigate to={ROUTES.home} replace />} />
      </Routes>
    </>
  )
}
