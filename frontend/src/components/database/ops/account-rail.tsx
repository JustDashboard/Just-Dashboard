"use client"

import { plural } from "@/lib/format"
import { StatusDot } from "@/components/status-dot"
import { Rail, RailFigure, RailLine, RailRow } from "@/components/rail"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"
import { cn } from "@/lib/utils"

/** One account as the rail draws it, whatever the engine calls it. */
export type RailAccount = {
  key: string
  name: string
  /** A MySQL host, or the database a document user is kept in. */
  host?: string
  /** What it is or holds, in the rail's few words. */
  line: string
  /** The line is something to look at: locked, any password. */
  tone?: "warning"
  /** It holds a session now. */
  live?: boolean
  /** How many sessions it holds, where the server says. */
  sessions?: number
  href: string
}

/**
 * The Access list, folded into a column beside the account it opened: the
 * list's own filter and order, so the next account to check is one press
 * away. Each row is the account's mark and name over what it holds; the one
 * on screen is filled, and one that holds a session carries a dot in its
 * mark's corner and says how many at its end.
 *
 * A row is a link, so it opens in a new tab like any other; `onLeave` is asked
 * first when the page holds unsaved work, and answers whether it took the
 * press.
 */
export function AccountRail({
  label,
  accounts,
  current,
  pinned,
  loading,
  error,
  onRetry,
  search,
  caption,
  filtered,
  onClearFilters,
  onLeave,
  className,
}: {
  /** "Accounts" or "Users", as the list is called. */
  label: string
  accounts: RailAccount[]
  /** The key of the account on screen. */
  current: string
  /** The account on screen, drawn first when the filtered list does not hold it. */
  pinned?: RailAccount
  loading: boolean
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  /** The list's own search, where the list has one. */
  search?: { value: string; onChange: (value: string) => void; placeholder: string }
  caption: string
  filtered: boolean
  onClearFilters: () => void
  onLeave: (href: string) => boolean
  className?: string
}) {
  return (
    <Rail
      slot="account-rail"
      label={label}
      search={
        search && { placeholder: search.placeholder, query: search.value, onQuery: search.onChange }
      }
      caption={caption}
      rows={accounts}
      keyOf={(account) => account.key}
      current={(account) => account.key === current}
      pinned={pinned}
      row={(account, on) => <AccountRow account={account} current={on} onLeave={onLeave} />}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty={`No ${label === "Users" ? "user" : "account"} matches.`}
      filtered={filtered}
      onClearFilters={onClearFilters}
      className={className}
    />
  )
}

function AccountRow({
  account,
  current,
  onLeave,
}: {
  account: RailAccount
  current: boolean
  onLeave: (href: string) => boolean
}) {
  const label = account.host ? `${account.name}@${account.host}` : account.name
  return (
    <RailRow
      item={account.key}
      name={label}
      current={current}
      href={account.href}
      // The page asks first while it holds unsaved work; otherwise the link follows.
      onOpen={() => onLeave(account.href)}
      mark={<AccountMark name={account.name} size="sm" />}
      dot={account.live ? <StatusDot tone="running" className="size-1.5" /> : null}
      end={
        account.sessions !== undefined &&
        account.sessions > 0 && (
          <RailFigure mono={false}>{plural(account.sessions, "session")}</RailFigure>
        )
      }
    >
      <span className="block truncate">
        <AccountName
          name={account.name}
          host={account.host}
          className={cn("text-body", current ? "font-semibold" : "font-medium")}
        />
      </span>
      <RailLine ink={account.tone === "warning" ? "text-warning" : undefined}>
        {account.line}
      </RailLine>
    </RailRow>
  )
}
