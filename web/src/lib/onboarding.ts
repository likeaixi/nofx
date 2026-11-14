export type UserMode = 'beginner' | 'advanced'

/**
 * Post-auth landing page for a freshly authenticated user.
 * The first owner registers in the guided ('beginner') flow and lands on the
 * welcome route; everyone else goes straight to the traders page.
 */
export function getPostAuthPath(mode: UserMode | null | undefined): string {
  return mode === 'beginner' ? '/welcome' : '/traders'
}
