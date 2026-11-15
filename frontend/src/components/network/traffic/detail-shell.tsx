"use client"

import { useEffect, useRef } from "react"
import {
  RailColumn,
  RailLine,
  RailName,
  RailNote,
  RailRow as SharedRailRow,
} from "@/components/rail"
import { DetailPage, siblingSteps, type Step } from "@/components/detail-page"

/**
 * The shape a container's traffic and an eBPF program share was the first
 * detail-with-rail frame written once; it is `components/detail-page.tsx` now,
 * and its rail is `components/rail.tsx`. These names stay for the two pages
 * until they move onto `DetailPage` and `Rail` (wave 2).
 */
export { BackLink, StepArrows } from "@/components/detail-page"
export type { Step } from "@/components/detail-page"
export { RailGhosts } from "@/components/rail"

/** @deprecated use `useSiblingSteps` from `@/components/detail-page`. */
export const steps = siblingSteps

/** @deprecated use `DetailPage` from `@/components/detail-page`. */
export function DetailShell({
  name,
  what,
  stateKey,
  refresh,
  previous,
  next,
  rail,
  header,
  children,
}: {
  name: string
  what: string
  stateKey: string
  refresh: () => void
  previous: Step
  next: Step
  rail: React.ReactNode
  header: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <DetailPage
      name={name}
      what={what}
      stateKey={stateKey}
      refresh={refresh}
      steps={{ previous, next }}
      rail={rail}
      head={header}
    >
      {children}
    </DetailPage>
  )
}

/**
 * The list a page was opened from, its rows drawn by the page.
 *
 * @deprecated use `Rail` from `@/components/rail`.
 */
export function DetailRail({
  label,
  slot,
  current,
  query,
  onQuery,
  placeholder,
  caption,
  filter,
  empty,
  children,
}: {
  label: string
  slot: string
  current: string | number | null
  query: string
  onQuery: (value: string) => void
  placeholder: string
  caption: React.ReactNode
  filter?: React.ReactNode
  empty?: React.ReactNode
  children: React.ReactNode
}) {
  const list = useRef<HTMLOListElement>(null)
  // Opened from a link or by Alt+↓ past the column's edge, the row on screen
  // would otherwise be off its own list.
  useEffect(() => {
    list.current?.querySelector<HTMLElement>("[aria-current='page']")?.scrollIntoView({
      block: "nearest",
    })
  }, [current])
  return (
    <RailColumn
      slot={slot}
      label={label}
      listRef={list}
      search={{ placeholder, query, onQuery }}
      caption={caption}
      action={filter}
    >
      {children}
      {empty && <RailNote>{empty}</RailNote>}
    </RailColumn>
  )
}

/** @deprecated use `RailRow` from `@/components/rail`, with an `href`. */
export function RailRow({
  id,
  name,
  label,
  current,
  onOpen,
  leading,
  detail,
  trailing,
}: {
  id: string
  name: string
  label: string
  current: boolean
  onOpen: () => void
  leading: React.ReactNode
  detail: React.ReactNode
  trailing?: React.ReactNode
}) {
  return (
    <SharedRailRow
      item={id}
      name={name}
      label={label}
      current={current}
      onOpen={onOpen}
      mark={leading}
      end={
        trailing && (
          <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
            {trailing}
          </span>
        )
      }
    >
      <RailName current={current} mono>
        {name}
      </RailName>
      <RailLine>{detail}</RailLine>
    </SharedRailRow>
  )
}
