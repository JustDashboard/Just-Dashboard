"use client"

import { useMemo, useState } from "react"
import {
  ArrowCircleUp,
  CloudDownload,
  Puzzle,
  RefreshClockwise,
  RotateCounterClockwise,
  ShieldOff,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { InstalledPackage, Job, PackageInventory, UpdateReport } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { InstallPanel } from "@/components/packages/install-panel"
import { PackageSheet } from "@/components/packages/package-sheet"
import { Page, PageHeader, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Everything installed on this server, and everything that could be.
 *
 * The old page answered one question — which packages are behind — which is
 * the question you have *after* you already know what is on the machine and
 * how to add to it. Both of those sent the operator to an SSH session, which
 * is the thing this product exists to avoid, and neither is harder to answer
 * than the one that was already here.
 *
 * The dashboard's own version is deliberately not on this page any more. It
 * shares nothing with apt but the word "update", and having the two together
 * meant the release notes for a root-equivalent panel lived under a table of
 * library versions.
 *
 * Drawn the way the host Overview is (design-system.md §15): a facts row
 * under the title, four figures as tiles, and three views under one strip of
 * tabs, each a plain panel — a toolbar, a hairline and a table that starts on
 * the page's own edge. The three things worth acting on before reading any
 * of that — security updates waiting, a reboot owed, an index too old to
 * trust — are notices, each carrying its own button, rather than a framed box
 * with a header and nothing in it.
 */

/** Rows rendered at once. See the footer below for why there is a cap at all. */
const MAX_ROWS = 400

type Scope = "explicit" | "all" | "upgradable"
type View = "installed" | "updates" | "install"

const VIEWS: { key: View; label: string }[] = [
  { key: "installed", label: "Installed" },
  { key: "updates", label: "Updates" },
  { key: "install", label: "Add software" },
]

export default function PackagesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  // The search box is kept for the tab; how the inventory is arranged, and
  // which view you were on, for good.
  const [filter, setFilter] = useSessionState("packages.query", "")
  const [view, setView] = useViewState<View>("packages.tab", "installed")
  const [scope, setScope] = useViewState<Scope>("packages.scope", "explicit")
  const [bySize, setBySize] = useViewState("packages.by-size", false)
  const [inspect, setInspect] = useState<string | null>(null)
  const [applying, setApplying] = useState(false)

  const inventory = usePoll(
    (signal) => get<PackageInventory>("/packages/", undefined, signal),
    120000,
  )
  const updates = usePoll(
    (signal) => get<UpdateReport>("/packages/updates", undefined, signal),
    300000,
  )

  // Both lists are only right once a run has finished — refresh on the
  // running → succeeded edge from the console itself, not from an effect
  // watching its status.
  const console_ = useJobConsole({
    onSuccess: () => {
      inventory.refresh()
      updates.refresh()
    },
  })

  const data = inventory.data
  const report = updates.data

  // A manager that cannot tell what was installed on purpose reports none, and
  // defaulting to a filter that hides everything is worse than showing a long
  // list. zypper is the one that cannot; apt, dnf, pacman and apk all can.
  const knowsExplicit = (data?.explicitCount ?? 0) > 0
  const effectiveScope: Scope = scope === "explicit" && !knowsExplicit ? "all" : scope

  const visible = useMemo(() => {
    const list = data?.packages ?? []
    const needle = filter.trim().toLowerCase()
    const matched = list.filter((p) => {
      if (effectiveScope === "explicit" && !p.explicit) return false
      if (effectiveScope === "upgradable" && !p.upgradable) return false
      if (!needle) return true
      return (
        p.name.toLowerCase().includes(needle) ||
        (p.summary ?? "").toLowerCase().includes(needle) ||
        (p.section ?? "").toLowerCase().includes(needle)
      )
    })
    if (bySize) {
      return [...matched].sort((a, b) => (b.size ?? 0) - (a.size ?? 0))
    }
    return matched
  }, [data, filter, effectiveScope, bySize])

  // The one package worth naming on the disk tile: a host whose 4 GB of
  // packages is mostly one toolchain wants to know which.
  const largest = useMemo(
    () =>
      (data?.packages ?? []).reduce<InstalledPackage | undefined>(
        (top, p) => (p.size && (!top?.size || p.size > top.size) ? p : top),
        undefined,
      ),
    [data],
  )

  const upgrade = (securityOnly: boolean) =>
    confirm({
      title: securityOnly ? "Install security updates" : "Upgrade all packages",
      phrase: securityOnly ? "install security updates" : "upgrade packages",
      confirmLabel: securityOnly ? "Install" : "Upgrade",
      description: (
        <>
          <p>
            Runs the host&rsquo;s package manager
            {securityOnly ? ", restricted to the security pocket" : ""}. Services whose packages
            change are restarted.
          </p>
          <p className="text-muted-foreground">
            New packages are never installed and nothing is removed. The output appears below as it
            happens, and the upgrade keeps running if you close this page.
          </p>
        </>
      ),
      action: async (phrase) => {
        setApplying(true)
        try {
          const job = await post<Job>("/packages/upgrade", undefined, {
            confirm: phrase,
            query: { security: securityOnly },
          })
          console_.attach(job)
        } finally {
          setApplying(false)
        }
      },
    })

  // Fetching a new index is not an upgrade and asks for no confirmation: it
  // writes a cache of signed metadata and changes nothing that is installed.
  const refreshIndex = async () => {
    setApplying(true)
    try {
      console_.attach(await post<Job>("/packages/refresh", {}))
    } catch (err) {
      notify.error("Could not refresh the package index", err)
    } finally {
      setApplying(false)
    }
  }

  const canRefresh = Boolean(data?.canRefresh) && can("service.control")
  const canUpgrade = can("destructive") && data?.available && (data.upgradeCount ?? 0) > 0
  const securityWaiting = Boolean(report?.securityFiltering) && (report?.securityCount ?? 0) > 0
  // A week is where an index stops being current enough to trust for "what is
  // available": the archive moves daily, and the timer that keeps it fresh is
  // the first thing to stop on a server nobody logs into.
  //
  // Measured against the server's own reading of the inventory rather than
  // against this browser's clock — both timestamps then come from the same
  // machine, so a laptop with the wrong date does not put a warning on a host
  // that refreshed an hour ago.
  const indexStale = Boolean(
    data?.indexAge &&
    new Date(data.readAt).getTime() - new Date(data.indexAge).getTime() > 7 * 24 * 60 * 60 * 1000,
  )

  const upgradeCount = data?.upgradeCount ?? 0
  const securityCount = data?.securityCount ?? 0

  return (
    <Page>
      {dialog}
      <PageHeader
        eyebrow="System"
        title="Packages"
        actions={
          <>
            <RecentJobs kinds={["updates.", "packages."]} onOpen={console_.open} />
            {canRefresh && (
              <Button
                variant="outline"
                size="sm"
                disabled={applying}
                onClick={() => void refreshIndex()}
              >
                <CloudDownload className="size-4" />
                Refresh index
              </Button>
            )}
            <Button
              variant="outline"
              size="sm"
              disabled={applying}
              onClick={() => {
                inventory.refresh()
                updates.refresh()
              }}
            >
              <RefreshClockwise className="size-4" />
              Re-read
            </Button>
          </>
        }
      />

      {/* What manages this host and how fresh the answer is. These were the
          hints under two tiles, where a fact about the whole page sat under
          one figure; here they are the subject of the page, as its own row. */}
      {data?.available && (
        <div className="flex min-w-0 animate-rise flex-wrap items-center gap-x-2 gap-y-1 text-body text-muted-foreground">
          <span className="font-medium text-foreground">{data.manager}</span>
          {data.indexAge && (
            <>
              <Dot />
              <span>index refreshed {relativeTime(data.indexAge)}</span>
            </>
          )}
          <Dot />
          <span>read {relativeTime(data.readAt)}</span>
          <Dot />
          {report?.rebootRequired ? (
            <Status verdict="warning" label="Reboot required" />
          ) : securityCount > 0 ? (
            <Status
              verdict="warning"
              label={`${securityCount} security update${securityCount === 1 ? "" : "s"}`}
            />
          ) : upgradeCount > 0 ? (
            <Status
              verdict="notice"
              label={`${upgradeCount} update${upgradeCount === 1 ? "" : "s"} waiting`}
            />
          ) : (
            <Status verdict="ok" label="Up to date" />
          )}
        </div>
      )}

      <StatGrid columns={4}>
        <StatTile
          label="Installed"
          value={
            <Figure settled={Boolean(data)}>
              {data?.available ? data.packages.length.toLocaleString() : "—"}
            </Figure>
          }
          hint={
            data?.available
              ? knowsExplicit
                ? `${(data.packages.length - data.explicitCount).toLocaleString()} arrived as dependencies`
                : undefined
              : data
                ? "no supported package manager"
                : undefined
          }
        />
        <StatTile
          label="Installed by hand"
          value={
            <Figure settled={Boolean(data)}>
              {knowsExplicit ? data!.explicitCount.toLocaleString() : "—"}
            </Figure>
          }
          hint={
            knowsExplicit
              ? "asked for, not pulled in"
              : data?.available
                ? `${data.manager} does not record this`
                : undefined
          }
        />
        <StatTile
          label="Updates"
          value={<Figure settled={Boolean(data)}>{data?.available ? upgradeCount : "—"}</Figure>}
          tone={
            securityCount > 0
              ? "warning"
              : upgradeCount > 0
                ? "default"
                : data?.available
                  ? "success"
                  : "default"
          }
          hint={
            data?.available
              ? securityCount > 0
                ? `${securityCount} security`
                : upgradeCount > 0
                  ? report?.securityFiltering
                    ? "none are security updates"
                    : `${data.manager} publishes no advisory data`
                  : "everything is current"
              : undefined
          }
        />
        <StatTile
          label="On disk"
          value={
            <Figure settled={Boolean(data)}>{data?.totalSize ? bytes(data.totalSize) : "—"}</Figure>
          }
          hint={
            largest?.size
              ? `${largest.name} is the largest at ${bytes(largest.size)}`
              : data?.available
                ? "what the installed packages occupy"
                : undefined
          }
        />
      </StatGrid>

      {/* The decision. Security updates are the reason to be on this page in a
          hurry, and the button to act is here rather than three tabs in. */}
      {canUpgrade && securityWaiting && (
        <Notice
          tone="warning"
          icon={ShieldOff}
          title={`${report!.securityCount} security update${report!.securityCount === 1 ? "" : "s"} waiting`}
        >
          Installing only these leaves everything else at the version it is on now. Services whose
          packages change are restarted.
          <div className="pt-2">
            <Button size="sm" disabled={applying} onClick={() => upgrade(true)}>
              <ShieldOff className="size-4" />
              Install security updates
            </Button>
          </div>
        </Notice>
      )}

      {report?.rebootRequired && (
        <Notice tone="warning" icon={RotateCounterClockwise} title="This server needs a reboot">
          An installed update cannot take effect until the machine restarts
          {report.rebootPackages?.length ? `: ${report.rebootPackages.slice(0, 6).join(", ")}` : ""}
          . Reboot from the Terminal when it suits you — the dashboard will not do it for you.
        </Notice>
      )}

      {indexStale && (
        <Notice tone="warning" icon={CloudDownload} title="The package index is out of date">
          This host last fetched its repository list {relativeTime(data!.indexAge)}, and everything
          on this page — what is available, what is behind — is read from it. Refresh it to search
          against what the repositories actually carry now.
          {canRefresh && (
            <div className="pt-2">
              <Button
                size="sm"
                variant="outline"
                disabled={applying}
                onClick={() => void refreshIndex()}
              >
                <CloudDownload className="size-4" />
                Refresh index
              </Button>
            </div>
          )}
        </Notice>
      )}

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />

      {inventory.error && <ErrorState error={inventory.error} />}
      {inventory.loading && !data && <LoadingPanel />}

      {data && !data.available && (
        <EmptyState
          icon={Puzzle}
          title="No supported package manager"
          description="This host does not appear to use apt, dnf, yum, zypper, pacman or apk, so there is nothing here to list, add or remove."
        />
      )}

      {data?.available && data.error && (
        <Notice tone="warning" icon={ShieldOff} title="Could not read the package database">
          <span className="font-mono text-xs">{data.error}</span>
        </Notice>
      )}

      {data?.available && (
        <div className="flex min-w-0 animate-rise flex-col gap-4">
          {/* The same underlined strip every switcher in the product wears:
              the brand underline says where you are, and the label stays
              ink. The pill-shaped tab list it replaces was a control with a
              face on a page that had just stopped drawing boxes. */}
          <nav
            aria-label="Package views"
            className="flex gap-1 overflow-x-auto border-b border-hairline"
          >
            {VIEWS.map((entry) => (
              <button
                key={entry.key}
                type="button"
                aria-pressed={view === entry.key}
                onClick={() => setView(entry.key)}
                className={tabClasses(view === entry.key, "h-10")}
              >
                {entry.label}
                {entry.key === "updates" && upgradeCount > 0 && (
                  <span
                    className={cn(
                      "numeric text-hint font-medium",
                      securityCount > 0 ? "text-warning" : "text-muted-foreground",
                    )}
                  >
                    {upgradeCount}
                  </span>
                )}
              </button>
            ))}
          </nav>

          {view === "installed" && (
            <Panel>
              <PanelToolbar>
                <SearchInput
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  placeholder="Name, description or section"
                  containerClassName="sm:w-72"
                />
                <div className="flex min-w-0 flex-wrap items-center gap-1">
                  {knowsExplicit && (
                    <FilterChip
                      selected={effectiveScope === "explicit"}
                      onClick={() => setScope("explicit")}
                    >
                      Installed by hand <ChipCount>{data.explicitCount}</ChipCount>
                    </FilterChip>
                  )}
                  <FilterChip selected={effectiveScope === "all"} onClick={() => setScope("all")}>
                    Everything <ChipCount>{data.packages.length}</ChipCount>
                  </FilterChip>
                  <FilterChip
                    selected={effectiveScope === "upgradable"}
                    onClick={() => setScope("upgradable")}
                  >
                    Behind <ChipCount>{upgradeCount}</ChipCount>
                  </FilterChip>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="ml-auto text-muted-foreground"
                  aria-pressed={bySize}
                  onClick={() => setBySize((v) => !v)}
                >
                  {bySize ? "Largest first" : "By name"}
                </Button>
              </PanelToolbar>
              <PanelBody flush>
                {visible.length === 0 ? (
                  <EmptyState
                    icon={Puzzle}
                    className="mt-4"
                    title={
                      effectiveScope === "upgradable" && !filter
                        ? "Everything is up to date"
                        : "Nothing matches that"
                    }
                    description={
                      effectiveScope === "upgradable" && !filter
                        ? report?.securityFiltering === false
                          ? `${data.manager} publishes no advisory data, so a clean list here means only that nothing at all is pending.`
                          : "No installed package has a newer version waiting."
                        : "Try a shorter word, or switch the filter to Everything — most of what is installed arrived as a dependency."
                    }
                  />
                ) : (
                  <div className="group-data-[plain]/panel:-mx-4 min-w-0">
                    <PackageTable packages={visible.slice(0, MAX_ROWS)} onInspect={setInspect} />
                  </div>
                )}
              </PanelBody>
              {visible.length > MAX_ROWS && (
                <PanelFooter className="justify-center">
                  {/* Rendering two thousand rows makes the tab unusable long
                      before it runs out of memory, and a list that long is not
                      read — it is searched. */}
                  <p className="text-xs text-muted-foreground">
                    Showing the first {MAX_ROWS} of {visible.length.toLocaleString()} — narrow the
                    filter to see the rest.
                  </p>
                </PanelFooter>
              )}
            </Panel>
          )}

          {view === "updates" && (
            <Panel>
              <PanelToolbar className="min-h-12">
                <p className="text-body text-muted-foreground">
                  {!report
                    ? "Reading what is behind…"
                    : report.packages.length === 0
                      ? "Nothing is waiting to be upgraded"
                      : `${report.packages.length} package${report.packages.length === 1 ? "" : "s"} behind${
                          report.securityFiltering ? ` · ${report.securityCount} security` : ""
                        }`}
                </p>
                {canUpgrade && (
                  <Button
                    size="sm"
                    variant="outline"
                    className="ml-auto"
                    disabled={applying}
                    onClick={() => upgrade(false)}
                  >
                    <ArrowCircleUp className="size-4" />
                    Upgrade all {report?.packages.length ?? upgradeCount}
                  </Button>
                )}
              </PanelToolbar>
              <PanelBody flush>
                {updates.loading && !report ? (
                  <LoadingPanel className="mt-4" />
                ) : !report || report.packages.length === 0 ? (
                  <EmptyState
                    icon={Puzzle}
                    className="mt-4"
                    title="Everything is up to date"
                    description={
                      report?.securityFiltering
                        ? "No packages are waiting to be upgraded."
                        : `${data.manager} publishes no advisory data, so a clean list here means only that nothing at all is pending.`
                    }
                  />
                ) : (
                  <div className="group-data-[plain]/panel:-mx-4 min-w-0">
                    <Table containerClassName="max-h-[calc(100svh-30rem)]">
                      <TableHeader className={stickyTableHeader}>
                        <TableRow>
                          <TableHead>Package</TableHead>
                          <TableHead>Installed</TableHead>
                          <TableHead>Available</TableHead>
                          <TableHead className="hidden w-full md:table-cell">Origin</TableHead>
                          <TableHead className="w-px" />
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {report.packages.map((p) => (
                          <TableRow key={p.name} onActivate={() => setInspect(p.name)}>
                            <TableCell>
                              <RowLink
                                mono
                                className="text-body"
                                onClick={() => setInspect(p.name)}
                              >
                                {p.name}
                              </RowLink>
                            </TableCell>
                            <TableCell className="font-mono text-muted-foreground">
                              {p.current || "—"}
                            </TableCell>
                            <TableCell className={cn("font-mono", p.security && "text-warning")}>
                              {p.candidate}
                            </TableCell>
                            <TableCell className="hidden md:table-cell">
                              <p
                                className="max-w-[18rem] truncate font-mono text-hint text-muted-foreground"
                                title={p.origin}
                              >
                                {p.origin || "—"}
                              </p>
                            </TableCell>
                            <TableCell className="text-right">
                              {p.security && <Tag tone="warning">security</Tag>}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}
              </PanelBody>
            </Panel>
          )}

          {view === "install" && (
            <InstallPanel manager={data.manager} onJob={console_.attach} onInspect={setInspect} />
          )}
        </div>
      )}

      <PackageSheet
        name={inspect}
        canPurge={Boolean(data?.canPurge)}
        onOpenChange={(open) => !open && setInspect(null)}
        onJob={console_.attach}
      />
    </Page>
  )
}

function Dot() {
  return <span className="text-muted-foreground/40">·</span>
}

/**
 * A tile's figure, rising once when the inventory lands: the key swaps
 * between the skeleton and the value so the number remounts rather than
 * flickering from bone to digits.
 */
function Figure({ settled, children }: { settled: boolean; children: React.ReactNode }) {
  return (
    <span
      key={settled ? "figure" : "skeleton"}
      className={cn("inline-block max-w-full truncate align-bottom", settled && "animate-rise")}
    >
      {settled ? children : <Skeleton className="my-1.5 h-5 w-16" />}
    </span>
  )
}

function PackageTable({
  packages,
  onInspect,
}: {
  packages: InstalledPackage[]
  onInspect: (name: string) => void
}) {
  return (
    <Table containerClassName="max-h-[calc(100svh-30rem)]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead>Package</TableHead>
          <TableHead className="w-full">What it is</TableHead>
          <TableHead>Version</TableHead>
          <TableHead className="hidden md:table-cell">Available</TableHead>
          <TableHead className="hidden text-right md:table-cell">Size</TableHead>
          <TableHead className="w-px" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {packages.map((p) => (
          <TableRow key={p.name} onActivate={() => onInspect(p.name)}>
            <TableCell>
              <RowLink mono className="text-body" onClick={() => onInspect(p.name)}>
                {p.name}
              </RowLink>
            </TableCell>
            <TableCell>
              <p className="max-w-[38rem] truncate text-xs text-muted-foreground">
                {p.summary || "—"}
              </p>
            </TableCell>
            <TableCell className="font-mono text-muted-foreground">{p.version}</TableCell>
            {/* The pending version beside the installed one, coloured only
                where it is a security fix: a column that is amber for every
                behind package says nothing about which ones matter. */}
            <TableCell
              className={cn("hidden font-mono md:table-cell", p.security && "text-warning")}
            >
              {p.upgradable}
            </TableCell>
            <TableCell className="numeric hidden text-right text-muted-foreground md:table-cell">
              {p.size ? bytes(p.size) : "—"}
            </TableCell>
            {/* The row's fixed properties at its edge, in a column, rather
                than interrupting the name at ten different points. */}
            <TableCell className="text-right">
              <span className="inline-flex items-center gap-2">
                {p.security && <Tag tone="warning">security</Tag>}
                {p.essential && <Tag>essential</Tag>}
              </span>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
