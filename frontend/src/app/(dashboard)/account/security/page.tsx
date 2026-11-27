"use client"

import { useAuth } from "@/hooks/use-auth"
import { plural } from "@/lib/format"
import { ShieldCheck } from "@/components/icons"
import { Page, PageContext } from "@/components/page"
import { IdentityHeader } from "@/components/identity-header"
import { Fact } from "@/components/fact-line"
import { Status } from "@/components/status-dot"
import {
  PasswordSection,
  SessionsSection,
  TwoFactorSection,
} from "@/components/account/security-panels"
import { PasskeysSection, usePasskeys } from "@/components/account/passkeys"
import { useSessions } from "@/components/account/sessions"
import { SignInPath } from "@/components/account/sign-in-path"
import { SaveBarProvider } from "@/components/settings/save-bar"
import { SettingsColumn } from "@/components/settings/settings-column"

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
 *
 * It opens on the identity line its siblings open on: the second factor as
 * the state, and what else guards the account as the facts. It was the one
 * Account page that started on its picture, 30px higher than the others.
 */
export default function AccountSecurityPage() {
  const { status } = useAuth()
  const sessions = useSessions()
  const passkeys = usePasskeys()
  const user = status?.user
  const required = Boolean(status?.require2fa)
  return (
    <SaveBarProvider>
      <Page rise>
        <PageContext title="Security" />
        <IdentityHeader
          fallback={ShieldCheck}
          title="Security"
          loading={!user}
          state={
            user ? (
              <Status
                verdict={user.totpEnabled ? "ok" : required ? "warning" : "notice"}
                label={user.totpEnabled ? "two-factor on" : "two-factor off"}
              />
            ) : (
              true
            )
          }
          facts={
            user && (
              <>
                {/* What needs doing first, so a phone's one line shows it. */}
                {!user.totpEnabled && required && <Fact>required on this install</Fact>}
                <Fact className="numeric">
                  {passkeys.data
                    ? passkeys.data.length > 0
                      ? plural(passkeys.data.length, "passkey")
                      : "no passkey"
                    : "passkeys"}
                </Fact>
                <Fact className="numeric">
                  {sessions.data ? plural(sessions.data.length, "session") : "sessions"}
                </Fact>
              </>
            )
          }
        />
        {user && (
          <SignInPath user={user} required={required} sessions={sessions.data ?? undefined} />
        )}
        <SettingsColumn>
          <TwoFactorSection />
          <PasskeysSection keys={passkeys} />
          <PasswordSection />
          <SessionsSection sessions={sessions} />
        </SettingsColumn>
      </Page>
    </SaveBarProvider>
  )
}
