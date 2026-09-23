"use client"

import { calendarDate, plural, relativeTime } from "@/lib/format"
import { parseAgent } from "@/lib/clients"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { DesktopDevice, Key, Shield, Users, type Icon } from "@/components/icons"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageHeader, PageState, Section } from "@/components/page"
import { ProductGlyphs, ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import type { Tone } from "@/components/tone"
import { CAPABILITIES, ROLE_MARK, ROLE_SUMMARY } from "@/components/account/capabilities"
import { ProfilePanel } from "@/components/account/profile-panel"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"
import { NetworkFact, useSessions } from "@/components/account/sessions"
import { keyProducts, usableKeys, useApiKeys } from "@/components/account/api-keys"
import { AvatarRun, useDashboardUsers } from "@/components/account/dashboard-users"

/**
 * The account, as the host Overview is the server: who this is, in one
 * identity line — the picture where the Overview draws the distribution, the
 * role and when this account joined among its facts, the client and network
 * this session came in through drawn as themselves, and the second factor's
 * verdict at the right end — then a row of readings that are each a door to
 * the page that changes them, then the profile and what the role allows.
 *
 * The header's figure strip (role, member since, last sign-in) is the
 * identity line's facts now, as it became on the Overview in 0.7.0. Each
 * reading names what it counts with the things themselves: the sessions by
 * their browsers, the keys by what holds them, the users by their faces.
 * Nothing here is framed; the section titles and the hairlines between tiles
 * are the structure.
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
  const current = sessions.data?.find((s) => s.current)
  const agent = current && parseAgent(current.userAgent)
  const clients = [
    ...new Set(
      (sessions.data ?? [])
        .map((s) => parseAgent(s.userAgent).product)
        .filter((id) => id !== undefined),
    ),
  ]
  const activeKeys = keys.data && usableKeys(keys.data)
  const admins = users.data?.filter((u) => u.role === "admin" && !u.disabled) ?? []
  const RoleMark = ROLE_MARK[user.role]

  return (
    <Page className="animate-rise">
      <PageHeader eyebrow="Account" title={name} />

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

      <StatGrid columns={admin ? 4 : 3}>
        <AccountTile
          href="/account/security"
          icon={Shield}
          title="Two-factor"
          settled
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
        <AccountTile
          href="/account/sessions"
          icon={DesktopDevice}
          title="Sessions"
          settled={Boolean(sessions.data)}
          value={sessions.data?.length}
          hint={
            sessions.data &&
            (sessions.data.length === 1
              ? "just this device"
              : `${sessions.data.length - 1} elsewhere`)
          }
          marks={<ProductGlyphs ids={clients} />}
        />
        <AccountTile
          href="/account/keys"
          icon={Key}
          title="API keys"
          settled={Boolean(keys.data)}
          value={activeKeys?.length}
          hint={
            keys.data &&
            (activeKeys?.length === 0
              ? "none in use"
              : `${keys.data.length - (activeKeys?.length ?? 0)} revoked or expired`)
          }
          marks={activeKeys && <ProductGlyphs ids={keyProducts(activeKeys)} />}
        />
        {admin && (
          <AccountTile
            href="/account/users"
            icon={Users}
            title="Users"
            settled={Boolean(users.data)}
            value={users.data?.length}
            hint={users.data && `${plural(admins.length, "admin")} can sign in`}
            marks={<AvatarRun users={admins} />}
          />
        )}
      </StatGrid>

      <Section title="Profile">
        <ProfilePanel user={user} />
      </Section>

      {/* Why a page is missing from the sidebar, answered where the reader
          looks first. The role's name says little; the six sentences say what
          it can and cannot do on this server, each beside the glyph of the
          sidebar entry it opens. */}
      <Section
        title="What this account can do"
        actions={
          <span className="inline-flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
            <RoleMark aria-hidden className="size-3.5 shrink-0 text-brand" />
            <span className="truncate">{ROLE_SUMMARY[user.role]}</span>
          </span>
        }
      >
        <RowList className="-mt-3">
          {CAPABILITIES.map((cap) => {
            const held = can(cap.key)
            return (
              <Row
                key={cap.key}
                className="px-0"
                leading={
                  <ProductLogo
                    size="sm"
                    fallback={cap.icon}
                    className={held ? "[&_svg]:text-brand" : "opacity-40"}
                  />
                }
                title={<span className={cn(!held && "text-muted-foreground")}>{cap.title}</span>}
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

/**
 * One reading of the account and the way to the page that changes it — a
 * `StatTile` behind a `StatLink`, the Overview's service tile over again: the
 * glyph before the name is the sidebar entry's, the figure rises once when its
 * poll lands, and `marks` names what the figure counts with the things
 * themselves.
 */
function AccountTile({
  href,
  icon: Glyph,
  title,
  value,
  hint,
  tone = "default",
  settled,
  marks,
}: {
  href: string
  icon: Icon
  title: string
  value: React.ReactNode
  hint?: React.ReactNode
  tone?: Tone
  settled: boolean
  marks?: React.ReactNode
}) {
  return (
    <StatLink href={href} label={title}>
      <StatTile
        className="h-full transition-colors group-hover:bg-row-hover"
        label={
          <>
            <Glyph aria-hidden className="mr-1.5 inline-block size-3 align-[-1.5px] text-brand" />
            {title}
          </>
        }
        value={
          <span
            key={settled ? "figure" : "skeleton"}
            className={cn("inline-block", settled && "animate-rise")}
          >
            {settled ? value : "—"}
          </span>
        }
        tone={tone}
        hint={
          <span className="inline-flex max-w-full min-w-0 items-center gap-2">
            <span className="truncate">{settled ? hint : "loading"}</span>
            {settled && marks}
          </span>
        }
      />
    </StatLink>
  )
}
