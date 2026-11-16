"use client"

import Link from "next/link"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { parseAgent } from "@/lib/clients"
import { useAuth } from "@/hooks/use-auth"
import { IdentityHeader } from "@/components/identity-header"
import { Fact } from "@/components/fact-line"
import { Page, PageContext, PageState } from "@/components/page"
import { Section } from "@/components/section"
import { Status } from "@/components/status-dot"
import { SaveBarProvider } from "@/components/settings/save-bar"
import { SettingSection } from "@/components/settings/setting-card"
import { SettingsColumn } from "@/components/settings/settings-column"
import { RoleTag } from "@/components/account/role"
import { AccessPicture } from "@/components/account/access-picture"
import { ProfileSection } from "@/components/account/profile-panel"
import { RoleLadder } from "@/components/account/role-ladder"
import { AvatarRun, UserAvatar, displayNameOf } from "@/components/user-avatar"
import { NetworkFact } from "@/components/client-mark"
import { useSessions } from "@/components/account/sessions"
import { useApiKeys } from "@/components/account/api-keys"
import { useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The account, as the host Overview is the server: who this is, in one
 * identity line — the picture where the Overview draws the distribution, the
 * role and when this account joined among its facts, the client and network
 * this session came in through drawn as themselves, and the second factor's
 * verdict beside its name — then everything signed in as this account wired
 * into it, and under the picture one centred column, as a project's settings
 * are: the profile, saved from the floating bar, and what the role allows.
 *
 * It opened on four tiles — two-factor, sessions, keys, users — and the
 * operator asked for them to go. Each is said better elsewhere: two-factor is
 * the line's verdict and the shield on the account's face in the picture;
 * sessions and keys are the picture's two sides, each counted in its head and
 * each a way to its page, every one drawn as the browser or the product that
 * holds it with a wire that pulses while it is in use; the accounts, for an
 * administrator, are their faces at the end of the line's facts.
 */
export default function ProfilePage() {
  return (
    <SaveBarProvider>
      <Profile />
    </SaveBarProvider>
  )
}

function Profile() {
  const { status, can } = useAuth()
  const user = status?.user
  const admin = can("system.admin")
  const sessions = useSessions()
  const keys = useApiKeys()
  const users = useDashboardUsers(admin)

  // While the account is read the page is already its own shape: the identity
  // line with bones in its boxes, not a framed table that the line replaces.
  if (!user) return <PageState title="Profile" skeleton={<IdentityHeader loading state />} />

  const name = displayNameOf(user)
  const twoFactor = user.totpEnabled
  const current = sessions.data?.find((s) => s.current)
  const agent = current && parseAgent(current.userAgent)
  const people = users.data ?? []

  return (
    <Page rise>
      <PageContext title={name} />

      <IdentityHeader
        logo={<UserAvatar key={user.avatarVersion} user={user} size="lg" />}
        title={
          <span className="inline-flex min-w-0 items-center gap-2.5">
            <span className="truncate font-mono">@{user.username}</span>
            <RoleTag role={user.role} />
          </span>
        }
        facts={
          <>
            <Fact>member since {calendarDate(user.createdAt)}</Fact>
            <Fact>signed in {user.lastLoginAt ? relativeTime(user.lastLoginAt) : "just now"}</Fact>
            {current && agent && (
              <>
                <Fact product={agent.product} className="truncate">
                  {agent.client}
                  {agent.os && ` on ${agent.os}`}
                </Fact>
                <Fact>
                  <NetworkFact ip={current.ip} />
                </Fact>
              </>
            )}
            {admin && people.length > 0 && (
              <Fact>
                <Link
                  href="/account/users"
                  className="inline-flex items-center gap-1.5 rounded-sm focus-ring hover:text-foreground"
                >
                  <AvatarRun users={people} />
                  {plural(people.length, "account")}
                </Link>
              </Fact>
            )}
          </>
        }
        state={
          <Status
            verdict={twoFactor ? "ok" : status?.require2fa ? "warning" : "notice"}
            label={twoFactor ? "two-factor on" : "two-factor off"}
          />
        }
      />

      <Section title="Signed in as you">
        {/* Until both reads land the picture waits at its loaded shape, so the
            page has its final shape before the sessions arrive (D11). */}
        <AccessPicture
          user={user}
          required={Boolean(status?.require2fa)}
          sessions={sessions.data && (keys.data || keys.error) ? sessions.data : undefined}
          // An administrator is handed everyone's keys; only this account's act as it.
          keys={keys.data?.filter((k) => k.userId === user.id)}
        />
      </Section>

      <SettingsColumn>
        <ProfileSection user={user} />
        {/* Why a page is missing from the sidebar, answered where the reader
            looks first: the roles as a ladder, lit up to this account's. */}
        <SettingSection title="What this account can do">
          <RoleLadder role={user.role} can={can} />
        </SettingSection>
      </SettingsColumn>
    </Page>
  )
}
