"use client"

import { useState } from "react"
import { plural, relativeTime } from "@/lib/format"
import type { BlockedEntry, BlocksView } from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { Address } from "@/components/security/marks"
import { sourceLabel } from "@/components/security/blocks"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const SHOWN = 25

const ENGINE_NAME = { fail2ban: "fail2ban", crowdsec: "CrowdSec", firewall: "Firewall" } as const

/**
 * Every refused address once, whichever engine refuses it.
 *
 * fail2ban, CrowdSec and the firewall each keep their own list, and a host
 * running two of them holds the same brute-forcer twice — banned by the sshd
 * jail an hour ago, a CrowdSec decision since, inside a /24 somebody denied
 * last month. Each section below shows its own list; this one folds them by
 * address so a duplicate, and a ban a broader block already covers, can be
 * seen before another is added. CrowdSec's community decisions that touch
 * nothing else are counted, not listed.
 */
export function BlocksPanel({
  data,
  error,
  loading,
  onRetry,
}: {
  data?: BlocksView
  error?: Error
  loading: boolean
  onRetry: () => void
}) {
  const [which, setWhich] = useState<"all" | "twice" | "covered">("all")
  const [all, setAll] = useState(false)

  if (loading && !data) {
    return (
      <Panel plain>
        <PanelHeader title="Blocked across engines" />
        <PanelBody>
          <Skeleton className="h-24 w-full" />
        </PanelBody>
      </Panel>
    )
  }
  if (error && !data) return <ErrorState error={error} onRetry={onRetry} />
  if (!data) return null

  const twice = data.entries.filter((e) => e.sources.length > 1)
  const covered = data.entries.filter((e) => (e.coveredBy?.length ?? 0) > 0)
  const shown = which === "twice" ? twice : which === "covered" ? covered : data.entries
  const rows = all ? shown : shown.slice(0, SHOWN)
  const unread = data.engines.filter((e) => !e.read)

  // Framed only around the table it scrolls (§2); a sentence stands plain.
  return (
    <Panel plain={shown.length === 0}>
      <PanelHeader
        title="Blocked across engines"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(data.distinct, "address", "addresses")} ·{" "}
            {data.engines
              .filter((e) => e.read)
              .map((e) => `${ENGINE_NAME[e.engine]} ${e.count}`)
              .join(" · ")}
          </span>
        }
      />
      <PanelToolbar>
        <ChipStrip aria-label="Which blocked addresses to show">
          <FilterChip selected={which === "all"} onClick={() => setWhich("all")}>
            Listed <ChipCount>{data.entries.length}</ChipCount>
          </FilterChip>
          <FilterChip selected={which === "twice"} onClick={() => setWhich("twice")}>
            Held more than once <ChipCount>{data.duplicated}</ChipCount>
          </FilterChip>
          <FilterChip selected={which === "covered"} onClick={() => setWhich("covered")}>
            Inside a broader block <ChipCount>{data.covered}</ChipCount>
          </FilterChip>
        </ChipStrip>
      </PanelToolbar>
      <PanelBody flush>
        {unread.length > 0 && (
          <p className="border-b border-hairline px-4 py-2.5 text-hint text-muted-foreground">
            Not read:{" "}
            {unread.map((e) => `${ENGINE_NAME[e.engine]} (${e.note ?? "unavailable"})`).join(", ")}.
            An engine that was not read is not an engine holding nothing.
          </p>
        )}
        {shown.length === 0 ? (
          <p className="px-4 py-4 text-body text-muted-foreground">
            {data.entries.length === 0
              ? "No engine is refusing an address right now."
              : "Nothing under this filter."}
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Address</TableHead>
                <TableHead>Held by</TableHead>
                <TableHead>Inside</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((entry) => (
                <BlockRow key={entry.value} entry={entry} />
              ))}
            </TableBody>
          </Table>
        )}
        {(shown.length > rows.length || data.communityOnly > 0 || data.truncated) && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline px-4 py-2.5">
            <span className="numeric text-hint text-muted-foreground">
              {shown.length > rows.length && `${rows.length} of ${shown.length}`}
              {data.communityOnly > 0 &&
                ` ${plural(data.communityOnly, "community decision")} touch nothing else and are not listed.`}
              {data.truncated && " The list stops at 500 entries."}
            </span>
            {shown.length > rows.length && (
              <Button size="xs" variant="ghost" onClick={() => setAll(true)}>
                Show all {shown.length}
              </Button>
            )}
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

function BlockRow({ entry }: { entry: BlockedEntry }) {
  const [base, bits] = entry.value.split("/")
  return (
    <TableRow>
      <TableCell className="py-3">
        <span className="inline-flex min-w-0 items-baseline">
          <Address ip={base} className="text-body font-medium" />
          {bits && <span className="font-mono text-body font-medium">/{bits}</span>}
        </span>
        {entry.sources.length > 1 && (
          <span className="mt-1 block text-hint text-warning">
            held {entry.sources.length} times
          </span>
        )}
      </TableCell>
      <TableCell className="py-3">
        <span className="flex flex-wrap gap-1.5">
          {entry.sources.map((source) => (
            <Tag
              key={`${source.engine}-${source.ref}`}
              title={[source.detail, source.until && `until ${relativeTime(source.until)}`]
                .filter(Boolean)
                .join(" · ")}
            >
              {sourceLabel(source)}
            </Tag>
          ))}
        </span>
      </TableCell>
      <TableCell className="py-3 font-mono text-hint text-muted-foreground">
        {entry.coveredBy?.join(", ") ?? "—"}
      </TableCell>
    </TableRow>
  )
}
