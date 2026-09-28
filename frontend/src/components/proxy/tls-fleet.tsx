"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { Download, Eye, Inspect, Play } from "@/components/icons"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { csvCell, downloadText } from "@/lib/metrics-export"
import { tlsReportHref, targetLabel } from "@/lib/scan-target"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type { Job } from "@/lib/types"
import type { FleetTarget, FleetView } from "@/lib/proxy/types-tls-fleet"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ROW_BLEED } from "@/components/row-list"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ChipCount, ChipStrip, FilterChip, tabClasses } from "@/components/tabs"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  stickyTableHeader,
} from "@/components/ui/table"
import { TLSReportPage, gradeTone } from "@/components/proxy/tls-report"

/**
 * The TLS page's two views, kept in the address as ?view=fleet so a link to
 * the table opens the table.
 */
export function TLSPage() {
  const params = useSearchParams()
  return params.get("view") === "fleet" ? <TLSFleetPage /> : <TLSReportPage />
}

export function TLSViewTabs({ view }: { view: "report" | "fleet" }) {
  return (
    <nav aria-label="TLS views" className="flex min-w-0 border-b border-hairline">
      <Link
        href="/proxy/tls"
        aria-current={view === "report" ? "page" : undefined}
        className={tabClasses(view === "report", "h-9")}
      >
        One target
      </Link>
      <Link
        href="/proxy/tls?view=fleet"
        aria-current={view === "fleet" ? "page" : undefined}
        className={tabClasses(view === "fleet", "h-9")}
      >
        Every site
      </Link>
    </nav>
  )
}

type Filter = "all" | "below-a" | "expiring" | "unreachable"

const FILTER_LABEL: Record<Filter, string> = {
  all: "All",
  "below-a": "Below A",
  expiring: "Expiring",
  unreachable: "Unreachable",
}

const FILTERS: Record<Filter, (t: FleetTarget) => boolean> = {
  all: () => true,
  "below-a": (t) => t.scan !== undefined && t.scan.grade !== "A+" && t.scan.grade !== "A",
  expiring: (t) => Boolean(t.scan?.expiring || t.scan?.expired),
  unreachable: (t) => t.scan !== undefined && !t.scan.reachable,
}

/**
 * Every name this server serves over TLS, and every watched endpoint, with
 * its latest report. One report answers "is this site right"; after a config
 * change or a renewal run the question is "is any of them wrong", and asking
 * it one name at a time is how the one that is gets missed.
 */
function TLSFleetPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const wide = useMediaQuery("(min-width: 1024px)")

  const console_ = useJobConsole({ onSuccess: () => fleet.refresh() })
  // The scan in flight as the server last said, which also covers one another
  // administrator started.
  const [inFlight, setInFlight] = useState<Job>()
  const running = console_.running || inFlight !== undefined
  // While a scan runs, each report is stored as it finishes, so the table
  // fills in behind the console rather than all at once at the end.
  const fleet = usePoll(
    (signal) => get<FleetView>("/certificates/scans/latest", undefined, signal),
    running ? 4000 : 0,
    [],
  )
  const serverJob = fleet.data?.job
  if (serverJob?.id !== inFlight?.id) setInFlight(serverJob)
  // A page opened while a scan runs follows it, once.
  const followed = useRef(false)
  const { job: attached, attach } = console_
  useEffect(() => {
    if (!serverJob || followed.current || attached) return
    followed.current = true
    attach(serverJob)
  }, [serverJob, attached, attach])

  const [filter, setFilter] = useSessionState<Filter>("proxy.tls.fleet.filter", "all")
  const [search, setSearch] = useSessionState("proxy.tls.fleet.search", "")
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [starting, setStarting] = useState(false)
  const [watching, setWatching] = useState(false)

  const all = useMemo(() => fleet.data?.targets ?? [], [fleet.data])
  const counts = useMemo(
    () =>
      Object.fromEntries(
        (Object.keys(FILTERS) as Filter[]).map((key) => [key, all.filter(FILTERS[key]).length]),
      ) as Record<Filter, number>,
    [all],
  )
  const scanned = all.filter((t) => t.scan).length
  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase()
    return all
      .filter(FILTERS[filter])
      .filter(
        (t) =>
          !needle ||
          targetLabel(t).includes(needle) ||
          t.sites.some((site) => site.toLowerCase().includes(needle)),
      )
      .sort((a, b) => gradeRank(a) - gradeRank(b) || targetLabel(a).localeCompare(targetLabel(b)))
  }, [all, filter, search])
  const chosen = visible.filter((t) => selected.has(targetLabel(t)))
  const toWatch = chosen.filter((t) => !t.watched)

  const scanAll = async () => {
    setStarting(true)
    try {
      const job = await post<Job>("/certificates/scan-all")
      followed.current = true
      console_.attach(job)
      fleet.refresh()
    } catch (err) {
      notify.error("Could not start the scan", err)
    } finally {
      setStarting(false)
    }
  }

  const watchSelected = async () => {
    setWatching(true)
    const results = await Promise.allSettled(
      toWatch.map((t) => post("/certificates/watched", { domain: t.host, port: t.port })),
    )
    setWatching(false)
    const failed = results.filter((r) => r.status === "rejected")
    if (failed.length > 0) {
      notify.error(
        `Could not watch ${failed.length} of ${toWatch.length}`,
        (failed[0] as PromiseRejectedResult).reason,
      )
    } else {
      notify.success(
        `Watching ${toWatch.length} more ${toWatch.length === 1 ? "target" : "targets"}`,
      )
    }
    setSelected(new Set())
    fleet.refresh()
  }

  const exportCsv = () => {
    const header = [
      "domain",
      "port",
      "grade",
      "days_left",
      "protocol",
      "issues",
      "served_by",
      "watched",
      "reachable",
      "checked_at",
    ]
    const lines = visible.map((t) =>
      [
        t.host,
        String(t.port),
        t.scan?.grade ?? "",
        t.scan?.daysLeft === undefined ? "" : String(t.scan.daysLeft),
        t.scan?.negotiated ?? "",
        t.scan ? String(t.scan.issues) : "",
        t.sites.join(" "),
        String(t.watched),
        t.scan ? String(t.scan.reachable) : "",
        t.scan?.checkedAt ?? "",
      ]
        .map(csvCell)
        .join(","),
    )
    downloadText("tls-fleet.csv", [header.join(","), ...lines].join("\n") + "\n")
  }

  const toggle = (t: FleetTarget) =>
    setSelected((current) => {
      const next = new Set(current)
      const key = targetLabel(t)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const allChosen = visible.length > 0 && chosen.length === visible.length

  const header = (
    <>
      <PageContext eyebrow="Proxy" title="TLS report" />
      <TLSViewTabs view="fleet" />
    </>
  )
  if (fleet.loading && !fleet.data) {
    return (
      <Page>
        {header}
        <LoadingPanel />
      </Page>
    )
  }
  if (fleet.error && !fleet.data) {
    return (
      <Page>
        {header}
        <ErrorState error={fleet.error} onRetry={fleet.refresh} />
      </Page>
    )
  }

  return (
    <Page className="animate-rise">
      {header}

      <StatGrid columns={4} dense>
        <StatTile label="Targets" value={all.length} hint={`${scanned} with a stored report`} />
        <StatTile
          label="Below A"
          value={counts["below-a"]}
          tone={counts["below-a"] > 0 ? "warning" : scanned > 0 ? "success" : "default"}
          hint="graded B or worse"
        />
        <StatTile
          label="Expiring"
          value={counts.expiring}
          tone={counts.expiring > 0 ? "warning" : "default"}
          hint="inside the renewal window or expired"
        />
        <StatTile
          label="Unreachable"
          value={counts.unreachable}
          tone={counts.unreachable > 0 ? "danger" : "default"}
          hint="no TLS handshake completed"
        />
      </StatGrid>

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={admin ? console_.cancel : undefined}
      />

      <Panel plain>
        <PanelHeader
          title="Every site"
          actions={
            <div className="flex flex-wrap items-center gap-2">
              {admin && toWatch.length > 0 && (
                <Button size="xs" variant="outline" pending={watching} onClick={watchSelected}>
                  <Eye className="size-3" />
                  Watch {toWatch.length}
                </Button>
              )}
              <Button
                size="xs"
                variant="outline"
                disabled={visible.length === 0}
                onClick={exportCsv}
              >
                <Download className="size-3" />
                CSV
              </Button>
              {admin && (
                <Button size="xs" pending={starting} disabled={running} onClick={scanAll}>
                  <Play className="size-3" />
                  {running ? "Scanning…" : "Scan all sites"}
                </Button>
              )}
            </div>
          }
        />
        <Toolbar className="justify-between gap-x-4">
          <SearchInput
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Domain, port or site"
          />
          <ChipStrip>
            {(Object.keys(FILTER_LABEL) as Filter[])
              .filter((key) => key === "all" || counts[key] > 0)
              .map((key) => (
                <FilterChip key={key} selected={filter === key} onClick={() => setFilter(key)}>
                  {FILTER_LABEL[key]} <ChipCount>{counts[key]}</ChipCount>
                </FilterChip>
              ))}
          </ChipStrip>
        </Toolbar>
        <PanelBody flush>
          {all.length === 0 ? (
            <EmptyState
              icon={Inspect}
              title="No TLS site or watched endpoint"
              description="An enabled site with a certificate, or a watched endpoint, is scanned here."
              className="mt-4"
            />
          ) : visible.length === 0 ? (
            <EmptyState icon={Inspect} title="No targets match" className="mt-4" />
          ) : wide ? (
            // A table of readings scrolls on its own, so its border marks that boundary.
            <Table
              className="table-fixed"
              containerClassName="max-h-[calc(100svh-24rem)] rounded-xl border bg-card"
            >
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  {admin && (
                    <TableHead className="w-10">
                      <Checkbox
                        aria-label="Select every target shown"
                        checked={allChosen}
                        onCheckedChange={(on) =>
                          setSelected(on ? new Set(visible.map((t) => targetLabel(t))) : new Set())
                        }
                      />
                    </TableHead>
                  )}
                  <TableHead>Domain</TableHead>
                  <TableHead className="w-20">Grade</TableHead>
                  <TableHead className="w-28">Days left</TableHead>
                  <TableHead className="w-24">Protocol</TableHead>
                  <TableHead className="w-20">Issues</TableHead>
                  <TableHead className="w-[22%]">Served by</TableHead>
                  <TableHead className="w-32">Checked</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((t) => (
                  <TableRow key={targetLabel(t)}>
                    {admin && (
                      <TableCell>
                        <Checkbox
                          aria-label={`Select ${targetLabel(t)}`}
                          checked={selected.has(targetLabel(t))}
                          onCheckedChange={() => toggle(t)}
                        />
                      </TableCell>
                    )}
                    <TableCell className="min-w-0">
                      <TargetLink target={t} />
                    </TableCell>
                    <TableCell>
                      <GradeReading target={t} />
                    </TableCell>
                    <TableCell>
                      <DaysLeft target={t} />
                    </TableCell>
                    <TableCell className="text-xs">{t.scan?.negotiated ?? "—"}</TableCell>
                    <TableCell>
                      <Issues target={t} />
                    </TableCell>
                    <TableCell>
                      <ServedBy target={t} />
                    </TableCell>
                    <TableCell className="text-hint text-muted-foreground">
                      {t.scan ? relativeTime(t.scan.checkedAt) : "never"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          ) : (
            <ul className="divide-y divide-hairline">
              {visible.map((t) => (
                <li
                  key={targetLabel(t)}
                  className={cn(
                    "flex min-w-0 items-start gap-3 py-3 transition-colors hover:bg-row-hover",
                    ROW_BLEED,
                  )}
                >
                  {admin && (
                    <Checkbox
                      className="mt-1"
                      aria-label={`Select ${targetLabel(t)}`}
                      checked={selected.has(targetLabel(t))}
                      onCheckedChange={() => toggle(t)}
                    />
                  )}
                  <div className="min-w-0 flex-1 space-y-1">
                    <TargetLink target={t} />
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                      <DaysLeft target={t} />
                      {t.scan?.negotiated && <span>{t.scan.negotiated}</span>}
                      <Issues target={t} />
                    </div>
                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                      <ServedBy target={t} />
                      <span className="text-hint text-muted-foreground">
                        {t.scan ? relativeTime(t.scan.checkedAt) : "never scanned"}
                      </span>
                    </div>
                  </div>
                  <GradeReading target={t} />
                </li>
              ))}
            </ul>
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}

function TargetLink({ target }: { target: FleetTarget }) {
  return (
    <Link
      href={tlsReportHref(target)}
      title={target.scan?.summary}
      className="block max-w-full min-w-0 truncate text-body font-medium focus-ring hover:underline"
    >
      {targetLabel(target)}
    </Link>
  )
}

function GradeReading({ target }: { target: FleetTarget }) {
  if (!target.scan) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <Tag tone={gradeTone(target.scan.grade)} className="numeric">
      {target.scan.grade}
    </Tag>
  )
}

function DaysLeft({ target }: { target: FleetTarget }) {
  const scan = target.scan
  if (!scan) return <span className="text-xs text-muted-foreground">not scanned</span>
  if (!scan.reachable) return <Status verdict="critical" label="unreachable" />
  if (scan.expired) return <Status verdict="critical" label="expired" />
  if (scan.daysLeft === undefined) return <span className="text-xs text-muted-foreground">—</span>
  return (
    <Status
      verdict={scan.expiring ? "warning" : "ok"}
      label={`${scan.daysLeft} ${scan.daysLeft === 1 ? "day" : "days"}`}
    />
  )
}

function Issues({ target }: { target: FleetTarget }) {
  if (!target.scan) return null
  const n = target.scan.issues
  return (
    <span className={cn("numeric text-xs", n > 0 ? "text-warning" : "text-muted-foreground")}>
      {n === 0 ? "none" : `${n} ${n === 1 ? "issue" : "issues"}`}
    </span>
  )
}

function ServedBy({ target }: { target: FleetTarget }) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
      {target.sites.map((site) => (
        <Tag key={site} mono className="max-w-full truncate">
          {site}
        </Tag>
      ))}
      {target.watched && <Tag>watched</Tag>}
    </span>
  )
}

const GRADES = ["F", "E", "D", "C", "B", "A-", "A", "A+"]

/** Worst first, and a target never scanned after every graded one. */
function gradeRank(target: FleetTarget) {
  return target.scan ? Math.max(0, GRADES.indexOf(target.scan.grade)) : GRADES.length
}
