"use client"

import { useEffect, useRef } from "react"
import { ShieldCheck } from "@/components/icons"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { reachesRoot, rootSummary, signInSummary } from "@/components/system-users/access"
import { AccountMark } from "@/components/system-users/marks"
import type { DotTone } from "@/components/status-dot"
import type { SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"

const LINE_INK: Partial<Record<DotTone, string>> = {
  danger: "text-destructive",
  warning: "text-warning",
}

const ROOT_INK = {
  danger: "text-destructive",
  warning: "text-warning",
  default: "text-muted-foreground",
  muted: "text-muted-foreground/50",
} as const

/**
 * The accounts list, folded into a column beside the account it opened: its
 * search, its filter and its order (root, then people, then what packages
 * made), so the next account to check is one press away. Each row is the
 * account's face or product, its name, whether it can sign in, and a shield in
 * the tone of how far it reaches; the one on screen is filled.
 */
export function AccountRail({
  users,
  current,
  currentUser,
  loading,
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
  query: string
  onQuery: (value: string) => void
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (name: string) => void
  className?: string
}) {
  const listed = users.some((user) => user.username === current)
  const pinned = !listed && currentUser ? currentUser : undefined
  const list = useRef<HTMLOListElement>(null)
  // The account on screen is kept in view: opened from a link or by Alt+↓
  // past the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])
  const firstDaemon = users.findIndex((user) => user.system && user.uid !== 0)

  return (
    <nav
      aria-label="Accounts"
      data-slot="account-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Name or group"
          aria-label="Search the accounts"
          containerClassName="sm:w-full"
        />
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
        aria-label="Accounts to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow user={pinned} current onOpen={onOpen} />}
        {pinned && users.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {users.map((user, index) => (
          <RailRow
            key={user.username}
            user={user}
            current={user.username === current}
            onOpen={onOpen}
            shelf={index === firstDaemon && index > 0}
          />
        ))}
        {loading && users.length === 0 && <LoadingRows rows={6} />}
        {!loading && users.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No account matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  user,
  current,
  onOpen,
  shelf,
}: {
  user: SystemUser
  current: boolean
  onOpen: (name: string) => void
  /** The first of the accounts packages made: a rule stands over it. */
  shelf?: boolean
}) {
  const signIn = signInSummary(user)
  const root = rootSummary(user)
  const reaches = reachesRoot(user)
  return (
    <>
      {shelf && <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />}
      <li className="min-w-0">
        <button
          type="button"
          data-workspace-item={user.username}
          data-workspace-name={user.username}
          aria-current={current ? "page" : undefined}
          aria-label={user.username}
          title={user.comment || undefined}
          onClick={() => onOpen(user.username)}
          className={cn(
            "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
            current ? "bg-accent" : "hover:bg-row-hover",
            user.disabled && "opacity-60",
          )}
        >
          <AccountMark user={user} size="sm" />
          <span className="min-w-0 flex-1">
            <span
              className={cn(
                "block truncate font-mono text-body",
                current ? "font-semibold" : "font-medium",
              )}
            >
              {user.username}
            </span>
            <span
              className={cn(
                "block truncate text-hint",
                LINE_INK[signIn.tone] ?? "text-muted-foreground",
              )}
            >
              {signIn.label}
            </span>
          </span>
          {reaches && (
            <ShieldCheck
              aria-label={root.label}
              className={cn("size-3.5 shrink-0", ROOT_INK[root.tone])}
            />
          )}
        </button>
      </li>
    </>
  )
}
