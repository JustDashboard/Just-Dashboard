"use client"

import { useSearchParams } from "next/navigation"
import { ShieldCheck } from "@/components/icons"
import { Rail, RailLine, RailName, RailRow, railInk } from "@/components/rail"
import {
  accountHref,
  reachesRoot,
  rootSummary,
  signInSummary,
} from "@/components/system-users/access"
import { AccountMark } from "@/components/system-users/marks"
import type { SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"

const ROOT_INK = {
  danger: "text-destructive",
  warning: "text-warning",
  default: "text-muted-foreground",
  muted: "text-faint",
} as const

/**
 * The accounts list, folded into a column beside the account it opened: its
 * search, its filter and its order (root, then people, then what packages
 * made), so the next account to check is one press away. Each row is the
 * account's face or product, its name, whether it can sign in, and a shield in
 * the tone of how far it reaches; the one on screen is filled. A rule stands
 * over the first of the accounts packages made.
 */
export function AccountRail({
  users,
  current,
  currentUser,
  loading,
  error,
  onRetry,
  query,
  onQuery,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  className,
}: {
  users: SystemUser[]
  current: string
  /** The account on screen, drawn first when the list does not hold it. */
  currentUser?: SystemUser
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** Opens an account carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab")
  const firstDaemon = users.findIndex((user) => user.system && user.uid !== 0)
  return (
    <Rail
      slot="account-rail"
      label="Accounts"
      search={{ placeholder: "Name or group", query, onQuery }}
      rows={users}
      keyOf={(user) => user.username}
      current={(user) => user.username === current}
      pinned={currentUser}
      divideBefore={(_, index) => index === firstDaemon && index > 0}
      row={(user, on) => (
        <AccountRow
          user={user}
          current={on}
          href={withTab(accountHref(user.username), tab)}
          onOpen={onOpen}
        />
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No account matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      caption={caption}
      className={className}
    />
  )
}

/** A page's address on the view the one on screen is showing; the first view is bare. */
export function withTab(path: string, tab: string | null) {
  return tab && tab !== "overview" ? `${path}?tab=${encodeURIComponent(tab)}` : path
}

function AccountRow({
  user,
  current,
  href,
  onOpen,
}: {
  user: SystemUser
  current: boolean
  href: string
  onOpen?: (name: string) => void
}) {
  const signIn = signInSummary(user)
  const root = rootSummary(user)
  const reaches = reachesRoot(user)
  return (
    <RailRow
      item={user.username}
      name={user.username}
      title={user.comment || undefined}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(user.username))}
      quiet={user.disabled}
      mark={<AccountMark user={user} size="sm" />}
      end={
        reaches && (
          <ShieldCheck
            aria-label={root.label}
            className={cn("size-3.5 shrink-0", ROOT_INK[root.tone])}
          />
        )
      }
    >
      <RailName current={current} mono>
        {user.username}
      </RailName>
      <RailLine ink={railInk(signIn.tone)}>{signIn.label}</RailLine>
    </RailRow>
  )
}
