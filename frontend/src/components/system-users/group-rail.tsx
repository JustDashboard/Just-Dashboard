"use client"

import { useEffect, useRef } from "react"
import { SearchInput } from "@/components/page"
import { LoadingRows } from "@/components/state"
import { GroupMark, REACH_INK } from "@/components/system-users/group-parts"
import { groupPeople, groupReach } from "@/components/system-users/groups"
import type { SystemGroup } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * The Groups list, folded into a column beside the group it opened: its search,
 * its switch for what packages made and its order (what reaches root first), so
 * the next group to check is one press away. Each row is the group's mark, its
 * name, what it hands over in a few words and how many are in it; the one on
 * screen is filled.
 */
export function GroupRail({
  groups,
  current,
  currentGroup,
  loading,
  query,
  onQuery,
  showSystem,
  onShowSystem,
  filtered,
  onClearFilters,
  caption,
  onOpen,
  className,
}: {
  groups: SystemGroup[]
  current: string
  /** The group on screen, drawn first when the list does not hold it. */
  currentGroup?: SystemGroup
  loading: boolean
  query: string
  onQuery: (value: string) => void
  showSystem: boolean
  onShowSystem: (show: boolean) => void
  /** The list was narrowed to what reaches root before the group was opened. */
  filtered: boolean
  onClearFilters: () => void
  caption: string
  onOpen: (name: string) => void
  className?: string
}) {
  const listed = groups.some((group) => group.name === current)
  const pinned = !listed && currentGroup ? currentGroup : undefined
  const list = useRef<HTMLOListElement>(null)
  // The group on screen is kept in view: opened from a link or by Alt+↓ past
  // the column's edge, it would otherwise be off its own list.
  useEffect(() => {
    const row = list.current?.querySelector<HTMLElement>("[aria-current='page']")
    row?.scrollIntoView({ block: "nearest" })
  }, [current])

  return (
    <nav
      aria-label="Groups"
      data-slot="group-rail"
      className={cn("min-h-0 flex-col border-r border-hairline pr-4", className)}
    >
      <div className="flex shrink-0 flex-col gap-2 pb-3">
        <SearchInput
          value={query}
          onChange={(event) => onQuery(event.target.value)}
          placeholder="Group or member"
          aria-label="Search the groups"
          containerClassName="sm:w-full"
        />
        <p className="flex min-w-0 items-center justify-between gap-2 text-hint text-muted-foreground">
          <span className="truncate">{caption}</span>
          {filtered ? (
            <button
              type="button"
              onClick={onClearFilters}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              Clear filter
            </button>
          ) : (
            <button
              type="button"
              aria-pressed={showSystem}
              onClick={() => onShowSystem(!showSystem)}
              className="shrink-0 rounded-sm font-medium focus-ring hover:text-foreground"
            >
              {showSystem ? "Hide system groups" : "System groups"}
            </button>
          )}
        </p>
      </div>
      <ol
        ref={list}
        aria-label="Groups to open"
        className="-mx-2 min-h-0 flex-1 [scrollbar-width:thin] space-y-0.5 overflow-y-auto px-2 pb-2"
      >
        {pinned && <RailRow group={pinned} current onOpen={onOpen} />}
        {pinned && groups.length > 0 && (
          <li aria-hidden className="mx-3 my-1.5 border-t border-hairline" />
        )}
        {groups.map((group) => (
          <RailRow
            key={group.name}
            group={group}
            current={group.name === current}
            onOpen={onOpen}
          />
        ))}
        {loading && groups.length === 0 && <LoadingRows rows={6} />}
        {!loading && groups.length === 0 && !pinned && (
          <li className="px-3 py-4 text-hint text-muted-foreground">No group matches.</li>
        )}
      </ol>
    </nav>
  )
}

function RailRow({
  group,
  current,
  onOpen,
}: {
  group: SystemGroup
  current: boolean
  onOpen: (name: string) => void
}) {
  const reach = groupReach(group)
  const people = groupPeople(group).length
  return (
    <li className="min-w-0">
      <button
        type="button"
        data-workspace-item={group.name}
        data-workspace-name={group.name}
        aria-current={current ? "page" : undefined}
        aria-label={group.name}
        title={reach.text || undefined}
        onClick={() => onOpen(group.name)}
        className={cn(
          "flex h-12 w-full min-w-0 items-center gap-2.5 rounded-lg px-2 text-left focus-ring-inset transition-colors",
          current ? "bg-accent" : "hover:bg-row-hover",
        )}
      >
        <GroupMark group={group} size="sm" />
        <span className="min-w-0 flex-1">
          <span
            className={cn(
              "block truncate font-mono text-body",
              current ? "font-semibold" : "font-medium",
            )}
          >
            {group.name}
          </span>
          <span
            className={cn(
              "block truncate text-hint",
              reach.label ? REACH_INK[reach.tone] : "numeric text-muted-foreground",
              reach.tone === "default" && "text-muted-foreground",
            )}
          >
            {reach.label || `gid ${group.gid}`}
          </span>
        </span>
        <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">{people}</span>
      </button>
    </li>
  )
}
