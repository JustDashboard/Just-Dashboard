"use client"

import { useState } from "react"
import { useSearchParams } from "next/navigation"
import { FileText } from "@/components/icons"
import { get } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import type { AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
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
  const [username, setUsername] = useState("")
  const [action, setAction] = useState(initialAction)
  const [onlyFailed, setOnlyFailed] = useState(false)
  const [offset, setOffset] = useState(0)

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

  return (
    <Page>
      <PageHeader eyebrow="Operations" title="Audit log" />

      {error && <ErrorState error={error} />}
      {loading && !data && <LoadingPanel rows={8} />}

      <Panel>
        <PanelHeader title="Recorded requests" />
        <PanelToolbar>
          <SearchInput
            containerClassName="w-40"
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
            className="h-8 w-60 text-body"
          />
          <label className="flex items-center gap-2 text-body text-muted-foreground">
            <Checkbox
              aria-label="Failures only"
              checked={onlyFailed}
              onCheckedChange={(v) => {
                setOnlyFailed(v === true)
                setOffset(0)
              }}
            />
            Failures only
          </label>
        </PanelToolbar>

        <PanelBody flush>
          <Table containerClassName="max-h-[calc(100svh-22rem)]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead className="w-44">When</TableHead>
                <TableHead>Who</TableHead>
                <TableHead>Action</TableHead>
                <TableHead className="w-full">Target</TableHead>
                <TableHead className="w-20">Result</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data?.entries.map((entry) => (
                <TableRow key={entry.id} className={entry.success ? undefined : "bg-wash-danger"}>
                  <TableCell>
                    <div>{timestamp(entry.ts)}</div>
                    <p className="text-hint text-muted-foreground">{relativeTime(entry.ts)}</p>
                  </TableCell>
                  <TableCell>
                    <div className="text-body">{entry.username || <em>anonymous</em>}</div>
                    <p className="font-mono text-hint text-muted-foreground">
                      {entry.ip} · {entry.actor}
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
                      <p className="truncate text-hint text-muted-foreground" title={entry.detail}>
                        {entry.detail}
                      </p>
                    )}
                  </TableCell>
                  <TableCell>
                    <span
                      className={cn(
                        "numeric text-xs",
                        entry.success ? "text-muted-foreground" : "text-destructive",
                      )}
                    >
                      {entry.status}
                    </span>
                  </TableCell>
                </TableRow>
              ))}
              {data?.entries.length === 0 && (
                <TableRow>
                  <TableCell colSpan={5} className="p-0">
                    <EmptyState icon={FileText} title="No entries match" />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>

        <PanelFooter className="justify-between">
          <span className="numeric text-xs text-muted-foreground">
            {!data
              ? "Loading…"
              : data.total === 0
                ? "Nothing recorded yet"
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
