"use client"

import { useEffect, useRef } from "react"
import Link from "next/link"
import { plural } from "@/lib/format"
import { cn } from "@/lib/utils"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { AccountMark, AccountName } from "@/components/database/ops/access-parts"

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
 * mark's corner and the count at its end.
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
  /** The list's own search, where the list has one. */
  search?: { value: string; onChange: (value: string) => void; placeholder: string }
  caption: string
  filtered: boolean
  onClearFilters: () => void
  onLeave: (href: string) => boolean
  className?: string
}) {
  const listed = accounts.some((account) => account.key === current)
  const first = !listed ? pinned : undefined
  const list = useRef<HTMLOListElement>(null)
  // The account on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label={label}
      data-slot="account-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        {search && (
          <SearchInput
            value={search.value}
            onChange={(event) => search.onChange(event.target.value)}
            placeholder={search.placeholder}
            aria-label={`Search the ${label.toLowerCase()}`}
            containerClassName="sm:w-full"
          />
        )}
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered && (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filters
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label={`${label} to open`}
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {first && <RailRow account={first} current onLeave={onLeave} />}
        {first && accounts.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {accounts.map((account) => (
          <RailRow
            key={account.key}
            account={account}
            current={account.key === current}
            onLeave={onLeave}
          />
        ))}
        {loading && accounts.length === 0 && !first && <LoadingRows rows={6} />}
        {!loading && accounts.length === 0 && !first && (
          <li className="px-3 py-4 text-hint text-muted-foreground">Nothing matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
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
    <li className="min-w-0">
      <Link
        href={account.href}
        data-workspace-item={account.key}
        data-workspace-name={label}
        aria-current={current ? "page" : undefined}
        aria-label={label}
        // Not scrolled: the column keeps its place while the page beside it changes.
        scroll={false}
        onClick={(event) => {
          if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
          if (onLeave(account.href)) event.preventDefault()
        }}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <span className="relative flex shrink-0">
          <AccountMark name={account.name} size="sm" />
          {account.live && (
            <span
              aria-hidden
              className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
            >
              <StatusDot tone="running" className="size-1.5" />
            </span>
          )}
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate">
            <AccountName
              name={account.name}
              host={account.host}
              className={cn("text-body", current ? "font-semibold" : "font-medium")}
            />
          </span>
          <span
            className={cn(
              "block truncate text-hint",
              account.tone === "warning" ? "text-warning" : "text-muted-foreground",
            )}
          >
            {account.line}
          </span>
        </span>
        {account.sessions !== undefined && account.sessions > 0 && (
          <span
            className="numeric shrink-0 font-mono text-hint text-muted-foreground"
            title={plural(account.sessions, "session")}
          >
            {account.sessions}
          </span>
        )}
      </Link>
    </li>
  )
}
