"use client"

import { useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { useSearchParams } from "next/navigation"
import { FileText } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { Metric, MetricStrip, Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const PAGE_SIZE = 100

export default function AuditPage() {
  // `?action=` is how the Docker event feed hands off: an event correlated to
  // an audit entry offers a link, and a link that lands on an unfiltered list
  // of everything the dashboard has ever done is not the entry it promised.
  //
  // Read once as an initial value rather than kept in sync, like the other
  // deep links in this product — the URL is where the reader arrived, not
  // where they are now, and re-applying it on every keystroke would fight the
  // filter box.
  const initialAction = useSearchParams().get("action") ?? ""
  const [username, setUsername] = useSessionState("audit.username", "")
  const [action, setAction] = useSessionState("audit.action", "", initialAction || undefined)
  const [onlyFailed, setOnlyFailed] = useSessionState("audit.failed", false)
  const [offset, setOffset] = useSessionState("audit.offset", 0)

  const { data, error, loading } = usePoll(
    (signal) =>
      get<{ entries: AuditEntry[]; total: number }>(
        "/audit/",
        { username, action, failed: onlyFailed, limit: PAGE_SIZE, offset },
        signal,
      ),
    15000,
    [username, action, onlyFailed, offset],
  )

  // The header's figure outlives a filter change: a new filter empties `data`
  // while it loads, and a total that blinked out on every keystroke would
  // read as the log emptying. Adjusted during render rather than in an
  // effect, so the figure never paints a frame behind the rows.
  const [total, setTotal] = useState<number>()
  if (data && data.total !== total) setTotal(data.total)

  const filtered = username !== "" || action !== "" || onlyFailed
  const entries = data?.entries ?? []

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Advanced"
        title="Audit log"
        actions={
          total !== undefined && (
            <MetricStrip>
              <Metric
                label={filtered ? "Matching" : "Recorded"}
                value={
                  <span key={total} className="inline-block animate-rise">
                    {total.toLocaleString()}
                  </span>
                }
              />
            </MetricStrip>
          )
        }
      />

      {/* The table is the whole of the page, and it is framed: a grid that owns
          its own scrolling takes an edge, or a row whose actions sit past the
          right of it reads as a row with no actions (§2). The toolbar stays
          mounted across a filter change so the box being typed into never
          loses its caret to a skeleton. */}
      <Panel>
        <PanelHeader title="Recorded requests" />
        <PanelToolbar>
          <SearchInput
            containerClassName="sm:w-48"
            value={username}
            onChange={(e) => {
              setUsername(e.target.value)
              setOffset(0)
            }}
            placeholder="User"
          />
          <Input
            value={action}
            onChange={(e) => {
              setAction(e.target.value)
              setOffset(0)
            }}
            placeholder="Action, e.g. docker.container"
            className="h-8 w-full text-body sm:w-64"
          />
          <FilterChip
            selected={onlyFailed}
            onClick={() => {
              setOnlyFailed(!onlyFailed)
              setOffset(0)
            }}
          >
            Failures only
          </FilterChip>
        </PanelToolbar>

        <PanelBody flush>
          {loading && !data && <LoadingPanel rows={8} />}
          {error && !data && <ErrorState error={error} />}
          {data && (
            <div key="rows" className="animate-rise">
              {/* The outer columns take the gutter from their own cell padding,
                  so the first column starts in the title's column; the `-mx`
                  bleed that does the same on a plain panel is gated to it (§2). */}
              <div className="group-data-[plain]/panel:-mx-4 hidden min-w-0 lg:block">
                <Table containerClassName="max-h-[calc(100svh-22rem)]">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-44">When</TableHead>
                      <TableHead>Who</TableHead>
                      <TableHead>Action</TableHead>
                      <TableHead className="w-full">Target</TableHead>
                      <TableHead>Result</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {entries.map((entry) => (
                      <TableRow key={entry.id}>
                        <TableCell>
                          <div>{timestamp(entry.ts)}</div>
                          <p className="text-hint text-muted-foreground">
                            {relativeTime(entry.ts)}
                          </p>
                        </TableCell>
                        <TableCell>
                          <div className="text-body">
                            {entry.username || (
                              <span className="text-muted-foreground">anonymous</span>
                            )}
                          </div>
                          <p className="font-mono text-hint text-muted-foreground">
                            {[entry.ip, entry.actor].filter(Boolean).join(" · ")}
                          </p>
                        </TableCell>
                        <TableCell>
                          <div className="font-mono text-xs">{entry.action}</div>
                          <p className="font-mono text-hint text-muted-foreground">
                            {entry.method} {entry.path}
                          </p>
                        </TableCell>
                        <TableCell className="max-w-xs">
                          <div className="truncate font-mono text-xs">{entry.target}</div>
                          {entry.detail && (
                            <p
                              className="truncate text-hint text-muted-foreground"
                              title={entry.detail}
                            >
                              {entry.detail}
                            </p>
                          )}
                        </TableCell>
                        <TableCell>
                          <Result entry={entry} />
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {/* Below `lg` the same entries are drawn down the row instead of
                  across it, so the phone still sees who did what to what, and
                  whether it worked. */}
              <ul className="divide-y divide-hairline lg:hidden">
                {entries.map((entry) => (
                  <li
                    key={entry.id}
                    className={cn("flex min-w-0 items-start gap-3 py-3", ROW_BLEED)}
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex min-w-0 items-baseline gap-2">
                        <span className="truncate font-mono text-xs font-medium">
                          {entry.action}
                        </span>
                        <span className="shrink-0 text-hint text-muted-foreground">
                          {relativeTime(entry.ts)}
                        </span>
                      </div>
                      <p className="truncate font-mono text-hint text-muted-foreground">
                        {entry.target}
                      </p>
                      <p className="truncate text-hint text-muted-foreground">
                        {entry.username || "anonymous"}
                        {entry.ip && ` · ${entry.ip}`}
                        {entry.detail && ` · ${entry.detail}`}
                      </p>
                    </div>
                    <Result entry={entry} className="shrink-0" />
                  </li>
                ))}
              </ul>
              {entries.length === 0 && (
                <EmptyState
                  icon={FileText}
                  title={
                    data.total === 0 && !filtered ? "Nothing recorded yet" : "No entries match"
                  }
                  description={
                    data.total === 0 && !filtered
                      ? "Every request that changes something on this host is written here."
                      : "Clear a filter, or look further back with the pager."
                  }
                  className="mt-4"
                />
              )}
            </div>
          )}
        </PanelBody>

        <PanelFooter className="justify-between">
          <span className="numeric text-hint text-muted-foreground">
            {!data
              ? "Loading…"
              : data.total === 0
                ? filtered
                  ? "No matching entries"
                  : "Nothing recorded yet"
                : `${offset + 1}–${Math.min(offset + PAGE_SIZE, data.total)} of ${data.total.toLocaleString()}`}
          </span>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={offset === 0}
              onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
            >
              Previous
            </Button>
            <Button
              size="sm"
              variant="outline"
              disabled={!data || offset + PAGE_SIZE >= data.total}
              onClick={() => setOffset((o) => o + PAGE_SIZE)}
            >
              Next
            </Button>
          </div>
        </PanelFooter>
      </Panel>
    </Page>
  )
}

/**
 * The outcome as a reading: a dot and the status code. Red arrives only here,
 * attached to the code that failed, rather than washed across the whole row.
 */
function Result({ entry, className }: { entry: AuditEntry; className?: string }) {
  return (
    <Status
      tone={entry.success ? "running" : "danger"}
      label={<span className="numeric font-mono">{entry.status}</span>}
      className={className}
    />
  )
}
