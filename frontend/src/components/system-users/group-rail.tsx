"use client"

import { useSearchParams } from "next/navigation"
import { Rail, RailAction, RailFigure, RailLine, RailName, RailRow } from "@/components/rail"
import { GroupMark, REACH_INK } from "@/components/system-users/group-parts"
import { groupHref, groupPeople, groupReach } from "@/components/system-users/groups"
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
  error,
  onRetry,
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
  /** The list could not be read: said in place of the rows. */
  error?: Error
  onRetry?: () => void
  query: string
  onQuery: (value: string) => void
  showSystem: boolean
  onShowSystem: (show: boolean) => void
  /** The list was narrowed to what reaches root before the group was opened. */
  filtered: boolean
  onClearFilters: () => void
  caption: string
  /** Opens a group carrying the page's view; the row's link does the same without it. */
  onOpen?: (name: string) => void
  className?: string
}) {
  const tab = useSearchParams().get("tab") ?? undefined
  return (
    <Rail
      slot="group-rail"
      label="Groups"
      search={{ placeholder: "Group or member", query, onQuery }}
      rows={groups}
      keyOf={(group) => group.name}
      current={(group) => group.name === current}
      pinned={currentGroup}
      row={(group, on) => (
        <GroupRow group={group} current={on} href={groupHref(group.name, tab)} onOpen={onOpen} />
      )}
      loading={loading}
      error={error}
      onRetry={onRetry}
      empty="No group matches."
      filtered={filtered}
      onClearFilters={onClearFilters}
      action={
        <RailAction pressed={showSystem} onClick={() => onShowSystem(!showSystem)}>
          {showSystem ? "Hide system groups" : "System groups"}
        </RailAction>
      }
      caption={caption}
      className={className}
    />
  )
}

function GroupRow({
  group,
  current,
  href,
  onOpen,
}: {
  group: SystemGroup
  current: boolean
  href: string
  onOpen?: (name: string) => void
}) {
  const reach = groupReach(group)
  const people = groupPeople(group).length
  return (
    <RailRow
      item={group.name}
      name={group.name}
      title={reach.text || undefined}
      current={current}
      href={href}
      onOpen={onOpen && (() => onOpen(group.name))}
      mark={<GroupMark group={group} size="sm" />}
      end={<RailFigure>{people}</RailFigure>}
    >
      <RailName current={current} mono>
        {group.name}
      </RailName>
      <RailLine
        ink={reach.label && reach.tone !== "default" ? REACH_INK[reach.tone] : undefined}
        className={cn(!reach.label && "numeric")}
      >
        {reach.label || `gid ${group.gid}`}
      </RailLine>
    </RailRow>
  )
}
