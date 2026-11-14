import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { AdminUsersPanel } from './AdminUsersPanel'

vi.mock('../../lib/httpClient', () => ({
  httpClient: {
    get: vi.fn(),
    post: vi.fn(),
    delete: vi.fn(),
    patch: vi.fn(),
  },
}))

vi.mock('../../contexts/AuthContext', () => ({
  useAuth: () => ({
    user: { id: 'u1', email: 'a@x.test', role: 'owner' },
  }),
}))

vi.mock('sonner', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))

import { httpClient } from '../../lib/httpClient'
import { toast } from 'sonner'

const invitesFixture = {
  invites: [
    { code: 'ABC123', created_by: 'o', revoked: false, created_at: '2026-11-14' },
  ],
}
const usersFixture = {
  users: [
    { id: 'u1', email: 'a@x.test', role: 'owner', disabled: false, created_at: '2026-11-14' },
    { id: 'u2', email: 'b@x.test', role: 'user', disabled: true, created_at: '2026-11-14' },
  ],
}

function mockGets() {
  ;(httpClient.get as any).mockImplementation((url: string) =>
    Promise.resolve({
      success: true,
      data: url === '/api/admin/invites' ? invitesFixture : usersFixture,
    })
  )
}

describe('AdminUsersPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGets()
  })
  afterEach(cleanup)

  it('renders invite and user lists from the admin API', async () => {
    render(<AdminUsersPanel />)
    await waitFor(() => expect(screen.getByText('a@x.test')).toBeTruthy())
    expect(screen.getByText('ABC123')).toBeTruthy()
    // Invite status label and user rows render
    expect(screen.getByText('未使用')).toBeTruthy()
    expect(screen.getByText('b@x.test')).toBeTruthy()
    // disabled flag is appended to the role line
    expect(screen.getByText('user · 已停用')).toBeTruthy()
  })

  it('creates an invite via POST and shows the code', async () => {
    ;(httpClient.post as any).mockResolvedValue({
      success: true,
      data: { code: 'NEW9' },
    })
    render(<AdminUsersPanel />)
    await waitFor(() => expect(screen.getByText('a@x.test')).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: '生成邀请码' }))
    await waitFor(() =>
      expect(httpClient.post).toHaveBeenCalledWith('/api/admin/invites', {})
    )
    await waitFor(() => expect(toast.success).toHaveBeenCalledWith('Invite created: NEW9'))
  })

  it('revokes an invite via DELETE and reloads', async () => {
    ;(httpClient.delete as any).mockResolvedValue({
      success: true,
      data: { message: 'revoked' },
    })
    render(<AdminUsersPanel />)
    await waitFor(() => expect(screen.getByText('ABC123')).toBeTruthy())
    fireEvent.click(screen.getByRole('button', { name: '作废' }))
    await waitFor(() =>
      expect(httpClient.delete).toHaveBeenCalledWith('/api/admin/invites/ABC123')
    )
    // reload refetches both lists
    await waitFor(() =>
      expect(httpClient.get).toHaveBeenCalledWith('/api/admin/invites')
    )
  })

  it('toggles a non-self user via PATCH and hides the toggle for self', async () => {
    ;(httpClient.patch as any).mockResolvedValue({
      success: true,
      data: { message: 'ok' },
    })
    render(<AdminUsersPanel />)
    await waitFor(() => expect(screen.getByText('b@x.test')).toBeTruthy())
    // u2 is disabled -> enable button ('恢复'); u1 is self -> no button
    fireEvent.click(screen.getByRole('button', { name: '恢复' }))
    await waitFor(() =>
      expect(httpClient.patch).toHaveBeenCalledWith('/api/admin/users/u2', {
        disabled: false,
      })
    )
    expect(screen.queryByRole('button', { name: '停用' })).toBeNull()
  })
})
