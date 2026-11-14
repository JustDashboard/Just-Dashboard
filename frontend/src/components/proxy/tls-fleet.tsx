"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { Download, Eye, Globe, LockClosed, LockOpen, Play } from "@/components/icons"
import { get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { csvCell, downloadText } from "@/lib/metrics-export"
import { tlsReportHref, targetLabel } from "@/lib/scan-target"
import { fleetFacts, fleetLife, fleetRank, gradeVerdict, type ChainTone } from "@/lib/tls-chain"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useSessionState } from "@/lib/view-state"
import type { Job } from "@/lib/types"
import type { FleetTarget, FleetView } from "@/lib/proxy/types-tls-fleet"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageContext, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { ProductLogo } from "@/components/product-logo"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { EmptyState, ErrorState, Spinner } from "@/components/state"
import { StatusDot, type DotTone } from "@/components/status-dot"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { VerbMenu } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Skeleton } from "@/components/ui/skeleton"
import { EmptyMark, OverviewEmpty } from "@/components/proxy/overview-empty"
import { TLSTools } from "@/components/proxy/request-tester"
import { TLSReportPage } from "@/components/proxy/tls-report"

/**
 * The TLS page's views, kept in the address: ?view=fleet for every site and
 * ?tool=request for the request tester, so a link to either opens it.
 */
export function TLSPage() {
  const params = useSearchParams()
  return params.get("view") === "fleet" ? <TLSFleetPage /> : <TLSReportPage />
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

const TONE_TEXT: Record<ChainTone, string> = {
  success: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
  neutral: "text-muted-foreground",
}

const TONE_DOT: Record<ChainTone, DotTone> = {
  success: "running",
  warning: "warning",
  danger: "danger",
  neutral: "unknown",
}

/**
 * Every name this server serves over TLS, and every watched endpoint, with
 * its latest report. One report answers "is this site right"; after a config
 * change or a renewal run the question is "is any of them wrong", and asking
 * it one name at a time is how the one that is gets missed.
 *
 * It opens on what it is about, as every page of the section does: the names
 * and how many of them are below A, expiring or unreachable, said once on the
 * identity line with Scan all sites beside them. Those figures were four tiles
 * that restated the filter chips under them. Each name is a one-line card
 * that opens its report, its grade the last word on the line; it was a table
 * whose rows were destinations, and a hand-made list on a phone.
 */
function TLSFleetPage() {
  const { can } = useAuth()
  const admin = can("system.admin")

  const console_ = useJobConsole({ onSuccess: () => fleet.refresh() })
  // The scan in flight as the server last said, which also covers one another
  // administrator started.
  const [inFlight, setInFlight] = useState<Job>()
  const running = console_.running || inFlight !== undefined
  // While a scan runs, each report is stored as it finishes, so the list
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
      .sort((a, b) => fleetRank(a) - fleetRank(b) || targetLabel(a).localeCompare(targetLabel(b)))
  }, [all, filter, search])
  const chosen = visible.filter((t) => selected.has(targetLabel(t)))
  const toWatch = chosen.filter((t) => !t.watched)
  const allChosen = visible.length > 0 && chosen.length === visible.length

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

  const loading = fleet.loading && !fleet.data

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Proxy" title="TLS report" />
      <HostIdentity
        fallback={LockClosed}
        title={
          <span role="heading" aria-level={2}>
            Every site
          </span>
        }
        facts={
          loading ? (
            <span className="inline-flex items-center gap-1.5">
              <Spinner className="size-3" /> reading the stored reports
            </span>
          ) : fleet.error && !fleet.data ? (
            <span className="text-destructive">the stored reports could not be read</span>
          ) : (
            <FleetFacts targets={all} />
          )
        }
        aside={
          <div className="flex items-center gap-2">
            {admin && (
              <Button size="sm" pending={starting} disabled={running || loading} onClick={scanAll}>
                <Play />
                {running ? "Scanning…" : "Scan all sites"}
              </Button>
            )}
            <VerbMenu
              label="More actions for every site"
              verbs={[
                {
                  key: "csv",
                  label: "Export CSV",
                  icon: Download,
                  run: exportCsv,
                  disabled: visible.length === 0,
                },
              ]}
            />
          </div>
        }
      />
      <TLSTools current="fleet" />

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={admin ? console_.cancel : undefined}
      />

      <Panel plain>
        <PanelHeader
          title="Names"
          actions={
            <div className="flex flex-wrap items-center gap-3">
              {admin && toWatch.length > 0 && (
                <Button size="xs" variant="outline" pending={watching} onClick={watchSelected}>
                  <Eye className="size-3" />
                  Watch {toWatch.length}
                </Button>
              )}
              <span className="numeric text-hint text-muted-foreground">
                {visible.length === all.length
                  ? plural(all.length, "name")
                  : `${visible.length} of ${all.length}`}
              </span>
            </div>
          }
        />
        <Toolbar className="justify-between gap-x-4 pt-3">
          <div className="flex min-w-0 items-center gap-3">
            {admin && (
              <Checkbox
                aria-label="Select every target shown"
                checked={allChosen}
                disabled={visible.length === 0}
                onCheckedChange={(on) =>
                  setSelected(on ? new Set(visible.map((t) => targetLabel(t))) : new Set())
                }
                className="ml-3"
              />
            )}
            <SearchInput
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Domain, port or site"
            />
          </div>
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
        <PanelBody className="pt-3">
          {loading ? (
            // The list's own rows, held while the reports are read.
            <ChoiceList aria-busy>
              {[0, 1, 2].map((index) => (
                <li key={index}>
                  <Skeleton className="h-14 w-full rounded-xl" />
                </li>
              ))}
            </ChoiceList>
          ) : fleet.error && !fleet.data ? (
            <ErrorState error={fleet.error} onRetry={fleet.refresh} />
          ) : all.length === 0 ? (
            <OverviewEmpty
              mark={<EmptyMark product="nginx" corner={LockOpen} />}
              title="No name to scan yet"
              action={
                <Button variant="outline" size="xs" asChild>
                  <Link href="/proxy/sites">Sites</Link>
                </Button>
              }
            >
              A site with a certificate, or a watched endpoint, is listed here.
            </OverviewEmpty>
          ) : visible.length === 0 ? (
            <EmptyState icon={Globe} title="No targets match" />
          ) : (
            <ChoiceList aria-label="Every site">
              {visible.map((t, index) => (
                <FleetRow
                  key={targetLabel(t)}
                  target={t}
                  index={index}
                  selectable={admin}
                  selected={selected.has(targetLabel(t))}
                  onToggle={() => toggle(t)}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}

/** The fleet's figures in words: only what is not zero, the alarming ones in their colour. */
function FleetFacts({ targets }: { targets: FleetTarget[] }) {
  const facts = fleetFacts(targets)
  const parts: React.ReactNode[] = [
    <span key="total">{plural(facts.total, "name")}</span>,
    <span key="scanned">
      {facts.scanned === facts.total ? "all scanned" : `${facts.scanned} scanned`}
    </span>,
  ]
  if (facts.belowA > 0)
    parts.push(
      <span key="below" className="font-medium text-warning">
        {facts.belowA} below A
      </span>,
    )
  if (facts.expiring > 0)
    parts.push(
      <span key="expiring" className="font-medium text-warning">
        {facts.expiring} expiring
      </span>,
    )
  if (facts.unreachable > 0)
    parts.push(
      <span key="unreachable" className="font-medium text-destructive">
        {facts.unreachable} unreachable
      </span>,
    )
  if (facts.newest) parts.push(<span key="newest">last report {relativeTime(facts.newest)}</span>)
  return parts.flatMap((part, index) =>
    index === 0 ? [part] : [<FactDot key={`dot-${index}`} />, part],
  )
}

/**
 * One name: drawn as what serves it — nginx for a site, a globe for an
 * endpoint only watched — with its report's verdict in the corner; then its
 * certificate, protocol, issues and age in aligned columns, its grade last.
 * Every state is one line, a target never scanned included.
 */
function FleetRow({
  target,
  index,
  selectable,
  selected,
  onToggle,
}: {
  target: FleetTarget
  index: number
  selectable: boolean
  selected: boolean
  onToggle: () => void
}) {
  const label = targetLabel(target)
  const scan = target.scan
  const verdict = gradeVerdict(scan?.grade)
  const life = fleetLife(target)
  const servedBy = [
    target.sites.length > 0 ? target.sites.join(", ") : undefined,
    target.watched ? "watched" : undefined,
  ].filter(Boolean)
  return (
    <ChoiceRow
      verb={`Open the report for ${label}`}
      href={tlsReportHref(target)}
      index={index}
      leading={
        <span className="relative flex">
          <ProductLogo
            id={target.sites.length > 0 ? "nginx" : undefined}
            fallback={Globe}
            size="sm"
          />
          <span className="absolute -right-1 -bottom-1 flex size-3.5 items-center justify-center rounded-full bg-background">
            <StatusDot tone={TONE_DOT[verdict]} />
          </span>
        </span>
      }
      title={<span className="font-mono text-[0.92em]">{label}</span>}
      description={servedBy.join(" · ") || "not served by a site here"}
      trailing={
        <span className="flex items-center gap-4 text-hint" title={scan?.summary}>
          <span className={cn("w-28 truncate", TONE_TEXT[life.tone])}>{life.words}</span>
          <span className="hidden w-14 truncate text-muted-foreground md:block">
            {scan?.negotiated ?? "—"}
          </span>
          <span
            className={cn(
              "numeric hidden w-16 truncate md:block",
              scan && scan.issues > 0 ? "text-warning" : "text-muted-foreground",
            )}
          >
            {scan ? (scan.issues === 0 ? "no issues" : plural(scan.issues, "issue")) : "—"}
          </span>
          <span className="hidden w-20 truncate text-muted-foreground sm:block">
            {scan ? relativeTime(scan.checkedAt) : "never"}
          </span>
          <span
            data-slot="grade"
            className={cn("numeric w-6 text-right text-body font-semibold", TONE_TEXT[verdict])}
          >
            {scan?.grade || "—"}
          </span>
        </span>
      }
      actions={
        selectable && (
          <Checkbox aria-label={`Select ${label}`} checked={selected} onCheckedChange={onToggle} />
        )
      }
    />
  )
}
