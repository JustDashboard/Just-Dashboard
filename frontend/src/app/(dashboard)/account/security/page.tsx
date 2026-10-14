"use client"

import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import {
  PasswordSection,
  SessionsSection,
  TwoFactorSection,
} from "@/components/account/security-panels"
import { useSessions } from "@/components/account/sessions"
import { SignInPath } from "@/components/account/sign-in-path"

/**
 * What stands between a session and this server: first as a picture — this
 * account, its password, the second factor and the server, the wires
 * pulsing straight through while a code is asked for and an amber line
 * around a dashed ring while it is not — then as the sections that change
 * each one. The second factor comes first and across the width, because "am
 * I protected" is the question the page is opened with; the password and the
 * sessions it would sign out stand side by side under it, the way
 * Configuration's settings do, each head over its fields with its state
 * under it (§7).
 */
export default function AccountSecurityPage() {
  const { status } = useAuth()
  const sessions = useSessions()
  const user = status?.user
  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title="Security" />
      {user && (
        <SignInPath
          user={user}
          required={Boolean(status?.require2fa)}
          sessions={sessions.data ?? undefined}
        />
      )}
      <div className="min-w-0 divide-y divide-hairline">
        <SettingsRow>
          <TwoFactorSection className={SECTION} />
        </SettingsRow>
        <SettingsRow columns>
          <PasswordSection className={SECTION} />
          <SessionsSection className={SECTION} sessions={sessions} />
        </SettingsRow>
      </div>
    </Page>
  )
}

/** Every section in the grid keeps its own width and no padding of its own: the row has it. */
const SECTION = "max-w-none py-0 first:pt-0 last:pb-0"

/**
 * One band of the sections: one across the width, or two side by side from
 * `xl` — Configuration's settings row, so the two pages that are forms about
 * this dashboard are laid out alike.
 */
function SettingsRow({ columns, children }: { columns?: boolean; children: React.ReactNode }) {
  return (
    <div
      className={cn(
        "grid min-w-0 gap-x-12 gap-y-14 py-8 first:pt-0 last:pb-0",
        columns && "xl:grid-cols-2",
      )}
    >
      {children}
    </div>
  )
}
