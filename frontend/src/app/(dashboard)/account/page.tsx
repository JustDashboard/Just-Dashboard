"use client"

import { useState } from "react"
import Link from "next/link"
import { calendarDate, plural, relativeTime } from "@/lib/format"
import { parseAgent } from "@/lib/clients"
import { useAuth } from "@/hooks/use-auth"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, PageState, Section } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { SaveBarProvider } from "@/components/deploy/settings/save-bar"
import { ROLE_MARK } from "@/components/account/capabilities"
import { AccessPicture } from "@/components/account/access-picture"
import { ProfilePanel } from "@/components/account/profile-panel"
import { RoleLadder } from "@/components/account/role-ladder"
import { AvatarRun, UserAvatar, displayNameOf } from "@/components/account/user-avatar"
import { NetworkFact } from "@/components/client-mark"
import { useSessions } from "@/components/account/sessions"
import { useApiKeys } from "@/components/account/api-keys"
import { useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The account, as the host Overview is the server: who this is, in one
 * identity line — the picture where the Overview draws the distribution, the
 * role and when this account joined among its facts, the client and network
 * this session came in through drawn as themselves, and the second factor's
 * verdict at the right end — then everything signed in as this account wired
 * into it, the profile, and what the role allows.
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
  const [edited, setEdited] = useState(false)

  if (!user) return <PageState eyebrow="Account" title="Profile" />

  const name = displayNameOf(user)
  const twoFactor = user.totpEnabled
  const current = sessions.data?.find((s) => s.current)
  const agent = current && parseAgent(current.userAgent)
  const RoleMark = ROLE_MARK[user.role]
  const people = users.data ?? []

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Account" title={name} />

      <HostIdentity
        logo={<UserAvatar key={user.avatarVersion} user={user} size="lg" />}
        title={
          <span className="inline-flex min-w-0 items-center gap-2.5">
            <span className="truncate font-mono">@{user.username}</span>
            <Tag icon={RoleMark}>{user.role}</Tag>
          </span>
        }
        facts={
          <>
            <span>member since {calendarDate(user.createdAt)}</span>
            <FactDot />
            <span>
              signed in{" "}
              {user.lastLoginAt.startsWith("0001") ? "just now" : relativeTime(user.lastLoginAt)}
            </span>
            {current && agent && (
              <>
                <FactDot />
                <HostFact product={agent.product}>
                  {agent.client}
                  {agent.os && ` on ${agent.os}`}
                </HostFact>
                <FactDot />
                <NetworkFact ip={current.ip} />
              </>
            )}
            {admin && people.length > 0 && (
              <>
                <FactDot />
                <Link
                  href="/account/users"
                  className="inline-flex items-center gap-1.5 rounded-sm focus-ring hover:text-foreground"
                >
                  <AvatarRun users={people} />
                  {plural(people.length, "account")}
                </Link>
              </>
            )}
          </>
        }
        aside={
          <Status
            className="text-body"
            verdict={twoFactor ? "ok" : status?.require2fa ? "warning" : "notice"}
            label={twoFactor ? "two-factor on" : "two-factor off"}
          />
        }
      />

      <Section title="Signed in as you">
        {sessions.data && (keys.data || keys.error) ? (
          <AccessPicture
            user={user}
            required={Boolean(status?.require2fa)}
            sessions={sessions.data}
            // An administrator is handed everyone's keys; only this account's act as it.
            keys={keys.data?.filter((k) => k.userId === user.id)}
          />
        ) : (
          <LoadingRows rows={3} />
        )}
      </Section>

      <Section
        title="Profile"
        actions={edited && <Tag style={{ color: "var(--git-modified)" }}>Edited</Tag>}
      >
        <ProfilePanel user={user} onDirty={setEdited} />
      </Section>

      {/* Why a page is missing from the sidebar, answered where the reader
          looks first: the roles as a ladder, lit up to this account's. */}
      <Section title="What this account can do">
        <RoleLadder role={user.role} can={can} />
      </Section>
    </Page>
  )
}
