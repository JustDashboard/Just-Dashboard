"use client"

import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext } from "@/components/page"
import {
  PasswordSection,
  SessionsSection,
  TwoFactorSection,
} from "@/components/account/security-panels"
import { PasskeysSection } from "@/components/account/passkeys"
import { useSessions } from "@/components/account/sessions"
import { SignInPath } from "@/components/account/sign-in-path"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { SettingsColumn } from "@/components/settings-column"

/**
 * What stands between a session and this server: first as a picture — this
 * account, its password, the second factor and the server, the wires
 * pulsing straight through while a code is asked for and an amber line
 * around a dashed ring while it is not — then as the sections that change
 * each one, in one centred column as a project's settings are (§7): a title,
 * one line of facts saying what the factor is now, then its verbs. The second
 * factor comes first, because "am I protected" is the question the page is
 * opened with; passkeys follow it, since they stand in for the password and
 * the page says the code is still asked for; then the password, which is the
 * floating bar's to change, and the sessions it would sign out.
 *
 * The sections ran across the width, the password beside the sessions, each
 * opening on a sentence or three; the column has no sentence under a title.
 */
export default function AccountSecurityPage() {
  const { status } = useAuth()
  const sessions = useSessions()
  const user = status?.user
  return (
    <SaveBarProvider>
      <Page className="animate-rise">
        <PageContext eyebrow="Account" title="Security" />
        {user && (
          <SignInPath
            user={user}
            required={Boolean(status?.require2fa)}
            sessions={sessions.data ?? undefined}
          />
        )}
        <SettingsColumn>
          <TwoFactorSection />
          <PasskeysSection />
          <PasswordSection />
          <SessionsSection sessions={sessions} />
        </SettingsColumn>
      </Page>
    </SaveBarProvider>
  )
}
