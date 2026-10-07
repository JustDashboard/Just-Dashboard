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
import type { HostInfo, InstalledPackage, Job, PackageInventory, UpdateReport } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { FactDot, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { InstallPanel } from "@/components/packages/install-panel"
import { PackageLogView } from "@/components/packages/log-view"
import { OriginFact, PackageMark, VersionTo, managerProduct } from "@/components/packages/marks"
import { HUE } from "@/components/overview/readings"
import { SoftwareBand } from "@/components/packages/software-band"
import { softwareKey, softwareName } from "@/components/packages/inventory"
import { PackageSheet } from "@/components/packages/package-sheet"
import { useQuerySelection } from "@/hooks/use-query-selection"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Page, PageContext, RowLink, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
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
 * Reading register, design-system §15. The four summary tiles each moved to
 * what they describe: installed to the identity and view count, by-hand and
 * dependencies to scope chips, updates to their queue and view, and disk use
 * to the software band. Like Processes, the band answers who, and pressing a
 * software group narrows the whole inventory, including its dependencies.
 */

/** Rows rendered at once. See the footer below for why there is a cap at all. */
const MAX_ROWS = 400

type Scope = "explicit" | "all" | "dependencies" | "upgradable" | "security"
type View = "installed" | "updates" | "install" | "log"

const VIEWS: { key: View; label: string }[] = [
  { key: "installed", label: "Installed" },
  { key: "updates", label: "Updates" },
  { key: "install", label: "Add software" },
  { key: "log", label: "Log" },
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
  const [inspect, setInspect] = useQuerySelection("package")
  const [software, setSoftware] = useSessionState("packages.software", "")
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
      if (software && softwareKey(p) !== software) return false
      if (effectiveScope === "dependencies" && p.explicit) return false
      if (effectiveScope === "security" && (!p.upgradable || !p.security)) return false
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
  }, [data, filter, effectiveScope, bySize, software])

  // The upgrade report names no section, so its rows borrow the inventory's.
  const sections = useMemo(
    () => new Map((data?.packages ?? []).map((p) => [p.name, p.section])),
    [data],
  )

  // Read once: which distribution the manager belongs to does not change
  // while the page is open.
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0).data

  const upgrade = (securityOnly: boolean) =>
    confirm({
      title: securityOnly ? "Install security updates" : "Upgrade all packages",
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
    <Workspace
      name="Packages"
      refresh={() => {
        inventory.refresh()
        updates.refresh()
      }}
      escape={() => {
        if (software) {
          setSoftware("")
          return true
        }
        if (!filter) return false
        setFilter("")
        return true
      }}
    >
      <Page>
        {dialog}
        <PageContext eyebrow="Advanced" title="Packages" />

        {/* What manages this host and how fresh the answer is, as the line the
          Overview opens on: the distribution the manager belongs to as the
          mark, the manager beside its name, the index's age and the read as
          facts, and whether anything is owed as the verdict — with the
          index's verbs beside it, because they change what the line says. */}
        {data?.available && (
          <HostIdentity
            className="animate-rise sm:[&>div:first-child]:min-w-64 [&>div:last-child]:max-w-full [&>div:last-child]:min-w-0 [&>div:last-child]:shrink"
            mark={platformProduct(host?.platform) ?? managerProduct(data.manager)}
            fallback={Puzzle}
            title={
              <>
                {host?.platform ? platformName(host) : data.manager}{" "}
                {host?.platform && (
                  <span className="ml-2 font-mono text-body font-normal text-muted-foreground">
                    {data.manager}
                  </span>
                )}
              </>
            }
            facts={
              <>
                <span className="numeric font-medium text-foreground">
                  {data.packages.length.toLocaleString()} packages
                </span>
                {data.indexAge && (
                  <>
                    <FactDot />
                    <span className={cn(indexStale && "text-warning")}>
                      index refreshed {relativeTime(data.indexAge)}
                    </span>
                  </>
                )}
                <FactDot />
                <span>read {relativeTime(data.readAt)}</span>
              </>
            }
            aside={
              <div className="flex max-w-full flex-wrap items-center gap-2">
                <span className="w-full text-body sm:mr-2 sm:w-auto">
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
                </span>
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
                <WorkspaceHelp compact />
              </div>
            }
          />
        )}

        {data?.available && !data.error && (
          <SoftwareBand
            inventory={data}
            report={report}
            error={updates.error}
            selected={software}
            onSelect={(key) => {
              setSoftware(key)
              setScope("all")
              setFilter("")
              setView("installed")
            }}
            onInspect={setInspect}
            onUpdates={() => setView("updates")}
            onUpgrade={upgrade}
            canUpgrade={Boolean(canUpgrade)}
            busy={applying}
          />
        )}

        {report?.rebootRequired && (
          <Notice tone="warning" icon={RotateCounterClockwise} title="This server needs a reboot">
            An installed update cannot take effect until the machine restarts
            {report.rebootPackages?.length
              ? `: ${report.rebootPackages.slice(0, 6).join(", ")}`
              : ""}
            . Reboot from the Terminal when it suits you — the dashboard will not do it for you.
          </Notice>
        )}

        {indexStale && (
          <Notice tone="warning" icon={CloudDownload} title="The package index is out of date">
            This host last fetched its repository list {relativeTime(data!.indexAge)}, and
            everything on this page — what is available, what is behind — is read from it. Refresh
            it to search against what the repositories actually carry now.
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
        {/* A package database that cannot be read is usually a transaction
          that did not finish, and apt's own log is where it says why — so
          the log stays on the page when the views it sits among cannot. */}
        {inventory.error && !data && (
          <Panel plain>
            <PanelHeader title="Package log" />
            <PanelBody flush className="pt-3">
              <PackageLogView product={platformProduct(host?.platform)} />
            </PanelBody>
          </Panel>
        )}
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
                  {(entry.key === "updates" || entry.key === "installed") && (
                    <span
                      className={cn(
                        "numeric text-hint font-medium",
                        entry.key === "updates" && securityCount > 0
                          ? "text-warning"
                          : "text-muted-foreground",
                      )}
                    >
                      {entry.key === "installed"
                        ? data.packages.length.toLocaleString()
                        : upgradeCount}
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
                    aria-label="Find installed packages"
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
                    {knowsExplicit && (
                      <FilterChip
                        selected={effectiveScope === "dependencies"}
                        onClick={() => setScope("dependencies")}
                      >
                        Dependencies{" "}
                        <ChipCount>{data.packages.length - data.explicitCount}</ChipCount>
                      </FilterChip>
                    )}
                    {securityCount > 0 && (
                      <FilterChip
                        selected={effectiveScope === "security"}
                        onClick={() => setScope("security")}
                      >
                        Security{" "}
                        <ChipCount className="text-warning opacity-100">{securityCount}</ChipCount>
                      </FilterChip>
                    )}
                    <FilterChip
                      selected={effectiveScope === "upgradable"}
                      onClick={() => setScope("upgradable")}
                    >
                      Behind <ChipCount>{upgradeCount}</ChipCount>
                    </FilterChip>
                  </div>
                  {software && (
                    <FilterChip
                      selected
                      onClick={() => setSoftware("")}
                      aria-label="Clear software filter"
                    >
                      {softwareName(software)} ×
                    </FilterChip>
                  )}
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
                    <div className="min-w-0 group-data-[plain]/panel:-mx-4">
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
                    {updates.error || report?.error
                      ? "Could not read updates"
                      : !report
                        ? "Reading what is behind…"
                        : report.packages.length === 0
                          ? "Nothing is waiting to be upgraded"
                          : `${report.packages.length} package${report.packages.length === 1 ? "" : "s"} behind${
                              report.securityFiltering ? ` · ${report.securityCount} security` : ""
                            }`}
                  </p>
                  {canUpgrade && !updates.error && !report?.error && (
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
                  {updates.error || report?.error ? (
                    <ErrorState
                      error={updates.error ?? new Error(report?.error)}
                      onRetry={updates.refresh}
                    />
                  ) : updates.loading && !report ? (
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
                    <div className="min-w-0 group-data-[plain]/panel:-mx-4">
                      <Table
                        className="table-fixed"
                        containerClassName="max-h-[max(20rem,calc(100svh-26rem))]"
                      >
                        <TableHeader className={stickyTableHeader}>
                          <TableRow>
                            <TableHead className="w-[45%] xl:w-[25%]">Package</TableHead>
                            <TableHead className="hidden w-[22%] xl:table-cell">
                              Installed
                            </TableHead>
                            <TableHead className="w-[55%] xl:w-[23%]">Available</TableHead>
                            <TableHead className="hidden w-[20%] xl:table-cell">Origin</TableHead>
                            <TableHead className="hidden w-[10%] xl:table-cell" />
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {report.packages.map((p) => (
                            <TableRow
                              key={p.name}
                              data-workspace-item={p.name}
                              data-workspace-name={p.name}
                              onActivate={() => setInspect(p.name)}
                            >
                              <TableCell>
                                <PackageName
                                  name={p.name}
                                  section={sections.get(p.name)}
                                  onInspect={setInspect}
                                />
                              </TableCell>
                              <TableCell
                                className="hidden truncate font-mono text-muted-foreground xl:table-cell"
                                title={p.current}
                              >
                                {p.current || "—"}
                              </TableCell>
                              <TableCell>
                                <VersionTo
                                  from={p.current}
                                  to={p.candidate}
                                  security={p.security}
                                  className="block truncate"
                                />
                              </TableCell>
                              <TableCell className="hidden xl:table-cell">
                                <OriginFact
                                  origin={p.origin}
                                  className="max-w-[18rem] text-hint text-muted-foreground"
                                />
                              </TableCell>
                              <TableCell className="hidden text-right xl:table-cell">
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

            {view === "log" && (
              <PackageLogView
                product={platformProduct(host?.platform) ?? managerProduct(data.manager)}
              />
            )}
          </div>
        )}

        <PackageSheet
          name={inspect}
          canPurge={Boolean(data?.canPurge)}
          onInspect={setInspect}
          onOpenChange={(open) => !open && setInspect(null)}
          onJob={console_.attach}
        />
      </Page>
    </Workspace>
  )
}

/** A package as the software it is, then its name — the one control on its row. */
function PackageName({
  name,
  section,
  onInspect,
}: {
  name: string
  section?: string
  onInspect: (name: string) => void
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5">
      <PackageMark name={name} section={section} />
      <RowLink mono className="truncate text-body" onClick={() => onInspect(name)}>
        {name}
      </RowLink>
    </div>
  )
}

function PackageTable({
  packages,
  onInspect,
}: {
  packages: InstalledPackage[]
  onInspect: (name: string) => void
}) {
  const largestSize = Math.max(...packages.map((p) => p.size ?? 0), 1)
  return (
    <Table className="table-fixed" containerClassName="max-h-[max(20rem,calc(100svh-26rem))]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead className="w-[65%] xl:w-[36%]">Package</TableHead>
          <TableHead className="w-[35%] xl:w-[21%]">Version</TableHead>
          <TableHead className="hidden w-[21%] xl:table-cell">Available</TableHead>
          <TableHead className="hidden w-[12%] text-right xl:table-cell">Size</TableHead>
          <TableHead className="hidden w-[10%] xl:table-cell" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {packages.map((p) => (
          <TableRow
            key={p.name}
            data-workspace-item={p.name}
            data-workspace-name={p.name}
            onActivate={() => onInspect(p.name)}
          >
            <TableCell>
              <PackageName name={p.name} section={p.section} onInspect={onInspect} />
              <p className="mt-1 truncate pl-8 text-hint text-muted-foreground" title={p.summary}>
                {p.summary || "—"}
              </p>
            </TableCell>
            <TableCell className="truncate font-mono text-muted-foreground" title={p.version}>
              {p.version}
            </TableCell>
            {/* The pending version beside the installed one, its changed part
                in ink and amber only where it is a security fix: a column that
                is amber for every behind package says nothing about which
                ones matter. */}
            <TableCell className="hidden xl:table-cell">
              {p.upgradable && (
                <VersionTo
                  from={p.version}
                  to={p.upgradable}
                  security={p.security}
                  className="block truncate"
                />
              )}
            </TableCell>
            <TableCell className="numeric hidden text-right text-muted-foreground xl:table-cell">
              {p.size ? (
                <span className="inline-flex w-full flex-col items-end gap-1.5">
                  <span>{bytes(p.size)}</span>
                  <span
                    aria-hidden
                    className="h-0.5 w-14 overflow-hidden rounded-full bg-meter-track"
                  >
                    <span
                      className="block h-full"
                      style={{
                        width: `${(p.size / largestSize) * 100}%`,
                        background: HUE.disk,
                      }}
                    />
                  </span>
                </span>
              ) : (
                "—"
              )}
            </TableCell>
            {/* The row's fixed properties at its edge, in a column, rather
                than interrupting the name at ten different points. */}
            <TableCell className="hidden text-right xl:table-cell">
              <span className="inline-flex flex-col items-end gap-1">
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
