"use client"

import type { EgressGroup } from "@/lib/network/egress"
import { plural } from "@/lib/format"
import { Rail, RailLine, RailName, RailRow } from "@/components/rail"
import { EgressMark, TONE_TEXT } from "@/components/network/egress/marks"
import { egressCarrying, egressHref } from "@/components/network/egress/reading"

/**
 * The list beside the group it opened, from `xl`: each group as its mark,
 * its name and the member carrying it in that state's colour, the one on
 * screen filled. There are at most six groups and the list has no filter, so
 * the rail has none either; the view on screen goes with you to the next.
 */
export function EgressRail({
  groups,
  current,
  tab,
  loading,
  failed,
  error,
  onRetry,
  className,
}: {
  groups: EgressGroup[]
  current?: number
  tab: string
  loading: boolean
  /** The first read failed: the column says so instead of counting nothing. */
  failed?: boolean
  /** The failure itself, where the page has it. */
  error?: Error
  onRetry?: () => void
  className?: string
}) {
  const failure = error ?? (failed ? new Error("The egress groups could not be read.") : undefined)
  return (
    <Rail
      slot="egress-rail"
      label="Egress groups"
      listLabel="Groups to open"
      caption={
        failure
          ? undefined
          : loading && groups.length === 0
            ? "Reading the groups…"
            : plural(groups.length, "group")
      }
      rows={groups}
      keyOf={(group) => String(group.id)}
      current={(group) => group.id === current}
      row={(group, here) => {
        const carrying = egressCarrying(group)
        return (
          <RailRow
            item={String(group.id)}
            name={group.name}
            current={here}
            href={egressHref(group.name, tab)}
            mark={<EgressMark group={group} size="sm" />}
          >
            <RailName current={here}>{group.name}</RailName>
            <RailLine ink={TONE_TEXT[carrying.tone]}>{carrying.label}</RailLine>
          </RailRow>
        )
      }}
      loading={loading}
      loadingRows={3}
      error={failure}
      onRetry={onRetry}
      className={className}
    />
  )
}
