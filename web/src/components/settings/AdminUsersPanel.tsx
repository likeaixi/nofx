import { useCallback, useEffect, useState } from 'react'
import { toast } from 'sonner'
import { httpClient } from '../../lib/httpClient'
import { useAuth } from '../../contexts/AuthContext'

interface InviteRow {
  code: string
  used_by?: string
  revoked: boolean
  created_at: string
}
interface UserRow {
  id: string
  email: string
  role: string
  disabled: boolean
  created_at: string
}

/**
 * Owner-only user management panel: invite codes (create/list/revoke) and
 * users (list/disable/enable). Rendered only for owners — the backend still
 * enforces 403 for everyone else.
 */
export function AdminUsersPanel() {
  const { user } = useAuth()
  const [invites, setInvites] = useState<InviteRow[]>([])
  const [users, setUsers] = useState<UserRow[]>([])
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      // httpClient throws (after its own toast) on network/403/404/5xx —
      // catch here so one failed list doesn't crash the panel.
      const [inv, usr] = await Promise.all([
        httpClient.get<{ invites: InviteRow[] }>('/api/admin/invites'),
        httpClient.get<{ users: UserRow[] }>('/api/admin/users'),
      ])
      if (inv.success && inv.data) setInvites(inv.data.invites ?? [])
      if (usr.success && usr.data) setUsers(usr.data.users ?? [])
    } catch {
      // already toasted by the httpClient interceptor
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  const createInvite = async () => {
    setBusy(true)
    try {
      const r = await httpClient.post<{ code: string }>('/api/admin/invites', {})
      if (r.success && r.data) {
        toast.success(`Invite created: ${r.data.code}`)
        await reload()
      } else toast.error(r.message || 'Failed')
    } catch {
      // network/system error already toasted by the interceptor
    } finally {
      setBusy(false)
    }
  }

  const revokeInvite = async (code: string) => {
    try {
      const r = await httpClient.delete(`/api/admin/invites/${code}`)
      if (!r.success) {
        toast.error(r.message || 'Failed')
        return
      }
      await reload()
    } catch {
      // network/system error already toasted by the interceptor
    }
  }

  const toggleUser = async (u: UserRow) => {
    try {
      const r = await httpClient.patch(`/api/admin/users/${u.id}`, {
        disabled: !u.disabled,
      })
      if (!r.success) {
        toast.error(r.message || 'Failed')
        return
      }
      await reload()
    } catch {
      // network/system error already toasted by the interceptor
    }
  }

  if (loading) {
    return <p className="text-xs text-nofx-text-muted py-4">加载中…</p>
  }

  return (
    <div className="space-y-8">
      <section>
        <div className="flex items-center justify-between mb-3">
          <h3 className="text-sm font-bold text-nofx-text">邀请码</h3>
          <button
            onClick={createInvite}
            disabled={busy}
            className="bg-nofx-gold text-nofx-bg text-xs font-bold px-3 py-1.5 rounded-lg"
          >
            生成邀请码
          </button>
        </div>
        <div className="space-y-1">
          {invites.length === 0 && (
            <p className="text-xs text-nofx-text-muted">暂无邀请码</p>
          )}
          {invites.map((i) => (
            <div
              key={i.code}
              className="flex items-center justify-between bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-lg px-3 py-2"
            >
              <span className="font-mono text-sm text-nofx-text tracking-widest">
                {i.code}
              </span>
              <span className="text-[10px] text-nofx-text-muted">
                {i.revoked ? '已作废' : i.used_by ? '已使用' : '未使用'}
              </span>
              {!i.used_by && !i.revoked && (
                <button
                  onClick={() => revokeInvite(i.code)}
                  className="text-[10px] text-nofx-danger hover:underline"
                >
                  作废
                </button>
              )}
            </div>
          ))}
        </div>
      </section>

      <section>
        <h3 className="text-sm font-bold text-nofx-text mb-3">用户</h3>
        <div className="space-y-1">
          {users.map((u) => (
            <div
              key={u.id}
              className="flex items-center justify-between bg-nofx-bg-deeper border border-[rgba(25,36,52,0.14)] rounded-lg px-3 py-2"
            >
              <div>
                <div className="text-sm text-nofx-text">{u.email}</div>
                <div className="text-[10px] text-nofx-text-muted">
                  {u.role}
                  {u.disabled ? ' · 已停用' : ''}
                </div>
              </div>
              {/* The backend rejects disabling yourself (400); hide the
                  button for the current user entirely. */}
              {u.id !== user?.id && (
                <button
                  onClick={() => toggleUser(u)}
                  className={`text-[10px] px-2 py-1 rounded ${
                    u.disabled ? 'text-nofx-gold' : 'text-nofx-danger'
                  } hover:underline`}
                >
                  {u.disabled ? '恢复' : '停用'}
                </button>
              )}
            </div>
          ))}
        </div>
      </section>
    </div>
  )
}
