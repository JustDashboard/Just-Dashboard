"use client"

import { useState } from "react"
import { plural, relativeTime } from "@/lib/format"
import type { BlockedEntry, BlocksView } from "@/lib/types"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { sourceLabel } from "@/components/security/blocks"
import { AddressLine, LineCell, LineList, LineRow } from "@/components/security/line-list"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

const SHOWN = 25

const ENGINE_NAME = { fail2ban: "fail2ban", crowdsec: "CrowdSec", firewall: "Firewall" } as const

/** Address, who holds it, the broader block it is inside. */
const COLUMNS = "minmax(0,1.1fr) minmax(0,1.6fr) minmax(0,0.9fr)"

/**
 * Every refused address once, whichever engine refuses it.
 *
 * fail2ban, CrowdSec and the firewall each keep their own list, and a host
 * running two of them holds the same brute-forcer twice: banned by the sshd
 * jail an hour ago, a CrowdSec decision since, inside a /24 somebody denied
 * last month. Each engine's view shows its own list; this one folds them by
 * address so a duplicate, and a ban a broader block already covers, can be
 * seen before another is added. CrowdSec's community decisions that touch
 * nothing else are counted, not listed.
 *
 * It is the page's fourth view. It used to open the page as a framed table
 * above the three engines, with "held 2 times" as a second line under the
 * addresses it was true of; a row is one line now, and an address held more
 * than once says so by carrying more than one name in its second column.
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

  if (error && !data) return <ErrorState error={error} onRetry={onRetry} retryable />

  const entries = data?.entries ?? []
  const twice = entries.filter((e) => e.sources.length > 1)
  const covered = entries.filter((e) => (e.coveredBy?.length ?? 0) > 0)
  const shown = which === "twice" ? twice : which === "covered" ? covered : entries
  const rows = all ? shown : shown.slice(0, SHOWN)
  const unread = data?.engines.filter((e) => !e.read) ?? []

  return (
    <Panel plain>
      <PanelHeader
        title="Blocked across engines"
        actions={
          data && (
            <span className="numeric text-hint text-muted-foreground">
              {plural(data.distinct, "address", "addresses")} ·{" "}
              {data.engines
                .filter((e) => e.read)
                .map((e) => `${ENGINE_NAME[e.engine]} ${e.count}`)
                .join(" · ")}
            </span>
          )
        }
      />
      <PanelToolbar>
        <ChipStrip aria-label="Which blocked addresses to show">
          <FilterChip selected={which === "all"} onClick={() => setWhich("all")}>
            Listed <ChipCount>{entries.length}</ChipCount>
          </FilterChip>
          <FilterChip selected={which === "twice"} onClick={() => setWhich("twice")}>
            Held more than once <ChipCount>{data?.duplicated ?? 0}</ChipCount>
          </FilterChip>
          <FilterChip selected={which === "covered"} onClick={() => setWhich("covered")}>
            Inside a broader block <ChipCount>{data?.covered ?? 0}</ChipCount>
          </FilterChip>
        </ChipStrip>
      </PanelToolbar>
      <PanelBody flush>
        {unread.length > 0 && (
          <p className="border-b border-hairline py-2.5 text-hint text-warning">
            Not read:{" "}
            {unread.map((e) => `${ENGINE_NAME[e.engine]} (${e.note ?? "unavailable"})`).join(", ")}.
            An engine that was not read is not an engine holding nothing.
          </p>
        )}
        {loading && !data ? (
          <LoadingRows rows={4} hold />
        ) : shown.length === 0 ? (
          <EmptyNote>
            {entries.length === 0
              ? "No blocked addresses are listed in this reading."
              : "Nothing under this filter."}
          </EmptyNote>
        ) : (
          <LineList columns={COLUMNS} label="Blocked addresses">
            {rows.map((entry) => (
              <BlockRow key={entry.value} entry={entry} />
            ))}
          </LineList>
        )}
        {data && (shown.length > rows.length || data.communityOnly > 0 || data.truncated) && (
          <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline py-2.5">
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
  const holders = entry.sources.map(sourceLabel).join(", ")
  const inside = entry.coveredBy?.join(", ")
  return (
    <LineRow>
      <LineCell main under={inside ? `${holders} · inside ${inside}` : holders}>
        <AddressLine value={entry.value} />
        {entry.sources.length > 1 && (
          <span className="numeric shrink-0 text-hint text-warning">
            held {entry.sources.length} times
          </span>
        )}
      </LineCell>
      <LineCell wide className="gap-x-3 overflow-hidden">
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
      </LineCell>
      <LineCell wide className="font-mono text-hint text-muted-foreground">
        <span className="truncate" title={inside}>
          {inside ?? "—"}
        </span>
      </LineCell>
    </LineRow>
  )
}
