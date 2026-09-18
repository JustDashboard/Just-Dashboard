"use client"

import { calendarDate, relativeTime } from "@/lib/format"
import { useAuth } from "@/hooks/use-auth"
import { Metric, MetricStrip, Page, PageHeader, PageState, Section } from "@/components/page"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { CAPABILITIES } from "@/components/account/capabilities"
import { ProfilePanel } from "@/components/account/profile-panel"
import { displayNameOf } from "@/components/account/user-avatar"
import { useSessions } from "@/components/account/sessions"
import { usableKeys, useApiKeys } from "@/components/account/api-keys"
import { useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The account, as the host Overview is the server: who this is, a row of
 * readings that are each a door to the page that changes them, then the
 * identity block and what the role allows. Nothing here is framed; the
 * section titles and the hairlines between tiles are the structure.
 */
export default function ProfilePage() {
  const { status, can } = useAuth()
  const user = status?.user
  const admin = can("system.admin")
  const sessions = useSessions()
  const keys = useApiKeys()
  const users = useDashboardUsers(admin)

  if (!user) return <PageState eyebrow="Account" title="Profile" />

  const name = displayNameOf(user)
  const twoFactor = user.totpEnabled
  const activeKeys = keys.data && usableKeys(keys.data)
  const admins = users.data?.filter((u) => u.role === "admin" && !u.disabled).length

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Account"
        title={name}
        actions={
          <MetricStrip>
            <Metric label="Role" value={<span className="capitalize">{user.role}</span>} />
            <Metric label="Member since" value={calendarDate(user.createdAt)} />
            <Metric
              label="Last sign-in"
              value={user.lastLoginAt.startsWith("0001") ? "now" : relativeTime(user.lastLoginAt)}
            />
          </MetricStrip>
        }
      />

      <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
        <span className="font-mono">@{user.username}</span>
        <Tag>{user.role}</Tag>
        <Status
          tone={twoFactor ? "running" : "notice"}
          label={twoFactor ? "two-factor on" : "two-factor off"}
        />
      </div>

      <StatGrid columns={admin ? 4 : 3}>
        <StatLink href="/account/security" label="Security">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Two-factor"
            value={twoFactor ? "On" : "Off"}
            tone={twoFactor ? "success" : status?.require2fa ? "warning" : "default"}
            hint={
              twoFactor
                ? "a code is asked for at every sign-in"
                : status?.require2fa
                  ? "required on this install — enrol now"
                  : "your password is the only factor"
            }
          />
        </StatLink>
        <StatLink href="/account/sessions" label="Sessions">
          <StatTile
            key={sessions.data ? "n" : "s"}
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Sessions"
            value={sessions.data ? sessions.data.length : "—"}
            hint={
              sessions.data
                ? sessions.data.length === 1
                  ? "just this device"
                  : `${sessions.data.length - 1} on other devices`
                : "loading"
            }
          />
        </StatLink>
        <StatLink href="/account/keys" label="API keys">
          <StatTile
            key={keys.data ? "n" : "s"}
            className="h-full transition-colors group-hover:bg-row-hover"
            label="API keys"
            value={activeKeys ? activeKeys.length : "—"}
            hint={
              keys.data
                ? activeKeys && activeKeys.length === 0
                  ? "none in use"
                  : `${keys.data.length - (activeKeys?.length ?? 0)} revoked or expired`
                : "loading"
            }
          />
        </StatLink>
        {admin && (
          <StatLink href="/account/users" label="Users">
            <StatTile
              key={users.data ? "n" : "s"}
              className="h-full transition-colors group-hover:bg-row-hover"
              label="Users"
              value={users.data ? users.data.length : "—"}
              hint={
                users.data
                  ? `${admins} ${admins === 1 ? "admin" : "admins"} can sign in`
                  : "loading"
              }
            />
          </StatLink>
        )}
      </StatGrid>

      <Section title="Identity">
        <ProfilePanel user={user} />
      </Section>

      {/* Why a page is missing from the sidebar, answered where the reader
          looks first. The role's name says little; the six sentences say what
          it can and cannot do on this server. */}
      <Section title="What this account can do">
        <RowList>
          {CAPABILITIES.map((cap) => {
            const held = can(cap.key)
            return (
              <Row
                key={cap.key}
                title={cap.title}
                subtitle={cap.description}
                trailing={
                  <Status
                    tone={held ? "running" : "stopped"}
                    label={held ? "granted" : "not in this role"}
                  />
                }
              />
            )
          })}
        </RowList>
      </Section>
    </Page>
  )
}
