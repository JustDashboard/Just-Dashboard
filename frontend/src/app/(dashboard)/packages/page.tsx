"use client"

import { Suspense, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
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
import { useViewState } from "@/lib/view-state"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { FactDot, HostIdentity, platformName } from "@/components/metrics/host-identity"
import { InstallPanel } from "@/components/packages/install-panel"
import { upgradeDescription } from "@/components/packages/upgrade-description"
import { PackageLogView } from "@/components/packages/log-view"
import { OriginFact, PackageMark, VersionTo, managerProduct } from "@/components/packages/marks"
import { SoftwareBand } from "@/components/packages/software-band"
import { packageHref, softwareName } from "@/components/packages/inventory"
import { usePackageFilter } from "@/components/packages/use-package-filter"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { platformProduct } from "@/components/product-logo"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { ChipCount, FilterChip, tabClasses } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
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

type View = "installed" | "updates" | "install" | "log"

const VIEWS: { key: View; label: string }[] = [
  { key: "installed", label: "Installed" },
  { key: "updates", label: "Updates" },
  { key: "install", label: "Add software" },
  { key: "log", label: "Log" },
]

// The address may name a package or a search, which the App Router only
// hands out inside a Suspense boundary.
export default function PackagesRoute() {
  return (
    <Suspense>
      <PackagesArrival />
    </Suspense>
  )
}

/**
 * What the address asks for before the page is drawn. `?package=` opened the
 * sheet a package's detail used to be; a package has a page now, and the old
 * address lands on it. `?q=` is a search handed over from another page ("Find
 * FRR in Packages"), which opens Add software on it.
 */
function PackagesArrival() {
  const search = useSearchParams()
  const router = useRouter()
  const legacy = search.get("package")
  useEffect(() => {
    if (legacy) router.replace(packageHref(legacy))
  }, [legacy, router])
  if (legacy) return null
  return <PackagesPage asked={search.get("q")} />
}

function PackagesPage({ asked }: { asked: string | null }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const router = useRouter()
  // Which view you were on is kept for good; the search and how the inventory
  // is arranged are the list's own (`usePackageFilter`), shared with the rail
  // beside a package.
  const [remembered, setView] = useViewState<View>("packages.tab", "installed")
  const [applying, setApplying] = useState(false)
  // A search handed over in the address is a complete question: it opens Add
  // software on it, and the address is cleaned so a reload is not the same
  // question again.
  useEffect(() => {
    if (!asked) return
    setView("install")
    const url = new URL(window.location.href)
    url.searchParams.delete("q")
    window.history.replaceState(null, "", `${url.pathname}${url.search}`)
  }, [asked, setView])
  // Until the reader picks a view themselves, the search is what the page is
  // on. Kept here rather than read from the address each time: the address is
  // cleaned before the inventory lands and the search box is drawn.
  const [handed, setHanded] = useState(asked)
  const [seen, setSeen] = useState(asked)
  if (asked !== seen) {
    setSeen(asked)
    if (asked) setHanded(asked)
  }
  const view: View = handed ? "install" : remembered
  const choose = (next: View) => {
    setHanded(null)
    setView(next)
  }
  // From here a table has room for its columns; narrower, each package is a card.
  const wide = useMediaQuery("(min-width: 1024px)")
  const open = (name: string) => router.push(packageHref(name))

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

  const list = usePackageFilter(data)
  const { query: filter, setQuery: setFilter, scope: effectiveScope, setScope } = list
  const { knowsExplicit, bySize, setBySize, software, setSoftware, rows: visible } = list

  // The upgrade report names no section, so its rows borrow the inventory's.
  const sections = useMemo(
    () => new Map((data?.packages ?? []).map((p) => [p.name, p.section])),
    [data],
  )

  // Read once: which distribution the manager belongs to does not change
  // while the page is open.
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0, [], {
    key: "/system/host",
  }).data

  const upgrade = (securityOnly: boolean) =>
    confirm({
      title: securityOnly ? "Install security updates" : "Upgrade all packages",
      confirmLabel: securityOnly ? "Install" : "Upgrade",
      description: (
        <>
          <p>
            {upgradeDescription(data?.manager ?? report?.manager, securityOnly)} Affected services
            may restart.
          </p>
          <p className="text-muted-foreground">
            The output appears below as it happens, and the upgrade keeps running if you close this
            page.
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
  const reread = () => {
    inventory.refresh()
    updates.refresh()
  }
  // The index's verb is the line's command, because it changes what the line
  // says; everything else the page can do to the host is a word in the menu.
  const menu: Verb[] = [
    { key: "reread", label: "Re-read", icon: RefreshClockwise, run: reread },
    ...(canUpgrade && !updates.error && !report?.error
      ? [
          ...(report?.securityFiltering && (report.securityCount ?? 0) > 0
            ? [
                {
                  key: "security",
                  label: "Install security updates",
                  icon: ShieldOff,
                  disabled: applying,
                  run: () => void upgrade(true),
                },
              ]
            : []),
          {
            key: "upgrade",
            label: `Upgrade all ${report?.packages.length ?? upgradeCount}`,
            icon: ArrowCircleUp,
            disabled: applying,
            run: () => void upgrade(false),
          },
        ]
      : []),
  ]
  const reading = inventory.loading && !data
  return (
    <Workspace
      name="Packages"
      refresh={reread}
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

        {/* What manages this host and how fresh the answer is, laid out as a
          project's header: the distribution the manager belongs to as the
          mark, the manager beside its name and whether anything is owed
          after it, the index's age and the read as facts, and the index's
          verb as the one command, because it changes what the line says. */}
        {data?.available && (
          <HostIdentity
            className="animate-rise"
            mark={platformProduct(host?.platform) ?? managerProduct(data.manager)}
            fallback={Puzzle}
            title={
              <>
                <span className="min-w-0 truncate">
                  {host?.platform ? platformName(host) : data.manager}
                </span>
                {host?.platform && (
                  <span className="ml-2 shrink-0 font-mono text-body font-normal text-muted-foreground">
                    {data.manager}
                  </span>
                )}
              </>
            }
            state={
              <>
                {report?.rebootRequired ? (
                  <Status verdict="warning" label="Reboot required" className="text-body" />
                ) : securityCount > 0 ? (
                  <Status
                    verdict="warning"
                    label={`${securityCount} security update${securityCount === 1 ? "" : "s"}`}
                    className="text-body"
                  />
                ) : upgradeCount > 0 ? (
                  <Status
                    verdict="notice"
                    label={`${upgradeCount} update${upgradeCount === 1 ? "" : "s"} waiting`}
                    className="text-body"
                  />
                ) : (
                  <Status verdict="ok" label="Up to date" className="text-body" />
                )}
                <RecentJobs kinds={["updates.", "packages."]} onOpen={console_.open} />
              </>
            }
            command={
              canRefresh ? (
                <Button
                  size="sm"
                  disabled={applying}
                  aria-label="Refresh index"
                  onClick={() => void refreshIndex()}
                >
                  <CloudDownload className="size-3.5" />
                  <span className="max-sm:hidden">Refresh index</span>
                  <span className="sm:hidden">Refresh</span>
                </Button>
              ) : undefined
            }
            menu={
              <>
                <VerbMenu verbs={menu} label="Package actions" />
                <span className="max-sm:hidden">
                  <WorkspaceHelp compact />
                </span>
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
          />
        )}

        {/* Held open while the inventory is out, on the same line the answer
          is drawn on, so nothing under it moves when it lands. A host with no
          package manager is the one answer that has nothing to draw here. */}
        {reading && (
          <HostIdentity
            mark={platformProduct(host?.platform)}
            fallback={Puzzle}
            title={<TextShimmer>Reading the package database…</TextShimmer>}
            state={<span className="text-body">&nbsp;</span>}
            facts={<span>&nbsp;</span>}
          />
        )}

        {(!data || data.available) && (
          <SoftwareBand
            inventory={data}
            report={report}
            error={updates.error}
            selected={software}
            onSelect={(key) => {
              setSoftware(key)
              setScope("all")
              setFilter("")
              choose("installed")
            }}
            onInspect={open}
            onUpdates={() => choose("updates")}
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

        {(data?.available || reading) && (
          <div className="flex min-w-0 animate-rise flex-col gap-4">
            {/* The same underlined strip every switcher in the product wears:
              the brand underline says where you are, and the label stays
              ink. It is drawn while the inventory is still out, so the views
              land under a strip that is already there. */}
            <nav
              aria-label="Package views"
              className="flex gap-1 overflow-x-auto border-b border-hairline"
            >
              {VIEWS.map((entry) => (
                <button
                  key={entry.key}
                  type="button"
                  aria-pressed={view === entry.key}
                  onClick={() => choose(entry.key)}
                  className={tabClasses(view === entry.key, "h-10")}
                >
                  {entry.label}
                  {data && (entry.key === "updates" || entry.key === "installed") && (
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

            {!data && <LoadingPanel />}

            {data && view === "installed" && (
              <Panel plain={!wide}>
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
                  ) : wide ? (
                    <PackageTable packages={visible.slice(0, MAX_ROWS)} onOpen={open} />
                  ) : (
                    <PackageCards packages={visible.slice(0, MAX_ROWS)} />
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

            {data && view === "updates" && (
              <Panel plain={!wide}>
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
                  ) : wide ? (
                    <Table
                      className="table-fixed"
                      containerClassName="max-h-[max(20rem,calc(100svh-26rem))]"
                    >
                      <TableHeader className={stickyTableHeader}>
                        <TableRow>
                          <TableHead>Package</TableHead>
                          <TableHead className="w-[20%]">Installed</TableHead>
                          <TableHead className="w-[20%]">Available</TableHead>
                          <TableHead className="w-[28%]">Origin</TableHead>
                          <TableHead className="w-24" />
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {report.packages.map((p) => (
                          <TableRow
                            key={p.name}
                            data-workspace-item={p.name}
                            data-workspace-name={p.name}
                            onActivate={() => open(p.name)}
                          >
                            <TableCell>
                              <PackageName name={p.name} section={sections.get(p.name)} />
                            </TableCell>
                            <TableCell
                              className="truncate font-mono text-muted-foreground"
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
                            <TableCell>
                              <OriginFact
                                origin={p.origin}
                                className="max-w-full text-hint text-muted-foreground"
                              />
                            </TableCell>
                            <TableCell className="text-right">
                              {p.security && <Tag tone="warning">security</Tag>}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  ) : (
                    <ChoiceList aria-label="Packages behind" className="pt-3">
                      {report.packages.map((p) => (
                        <ChoiceRow
                          key={p.name}
                          workspaceItem={{ id: p.name, name: p.name }}
                          leading={<PackageMark name={p.name} section={sections.get(p.name)} />}
                          title={<span className="font-mono">{p.name}</span>}
                          verb={p.name}
                          description={
                            <VersionTo from={p.current} to={p.candidate} security={p.security} />
                          }
                          href={packageHref(p.name)}
                          trailing={p.security && <Tag tone="warning">security</Tag>}
                        />
                      ))}
                    </ChoiceList>
                  )}
                </PanelBody>
              </Panel>
            )}

            {data && view === "install" && (
              <InstallPanel
                manager={data.manager}
                asked={handed}
                onJob={console_.attach}
                observedJob={console_.job}
                onSettled={reread}
              />
            )}

            {data && view === "log" && (
              <PackageLogView
                product={platformProduct(host?.platform) ?? managerProduct(data.manager)}
              />
            )}
          </div>
        )}
      </Page>
    </Workspace>
  )
}

/** A package as the software it is, then its name: the way into its page. */
function PackageName({
  name,
  section,
  summary,
}: {
  name: string
  section?: string
  summary?: string
}) {
  return (
    <div className="flex min-w-0 items-center gap-2.5" title={summary}>
      <PackageMark name={name} section={section} />
      <Link
        href={packageHref(name)}
        className="max-w-full min-w-0 truncate rounded-sm font-mono text-body font-medium focus-ring hover:underline"
      >
        {name}
      </Link>
    </div>
  )
}

/**
 * The installed packages as a table read down its columns: which are behind,
 * which are large. One line a row. The description a row used to carry under
 * its name is the row's title and the first fact on the package's page, and
 * the size is a figure in its column: a bar under each, scaled to the largest
 * package on screen, re-drew every row when a filter changed.
 */
function PackageTable({
  packages,
  onOpen,
}: {
  packages: InstalledPackage[]
  onOpen: (name: string) => void
}) {
  return (
    <Table className="table-fixed" containerClassName="max-h-[max(20rem,calc(100svh-26rem))]">
      <TableHeader className={stickyTableHeader}>
        <TableRow>
          <TableHead>Package</TableHead>
          <TableHead className="w-[22%]">Version</TableHead>
          <TableHead className="w-[22%]">Available</TableHead>
          <TableHead className="w-24 text-right">Size</TableHead>
          <TableHead className="hidden w-40 xl:table-cell" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {packages.map((p) => (
          <TableRow
            key={p.name}
            data-workspace-item={p.name}
            data-workspace-name={p.name}
            onActivate={() => onOpen(p.name)}
          >
            <TableCell>
              <PackageName name={p.name} section={p.section} summary={p.summary} />
            </TableCell>
            <TableCell className="truncate font-mono text-muted-foreground" title={p.version}>
              {p.version}
            </TableCell>
            {/* The pending version beside the installed one, its changed part
                in ink and amber only where it is a security fix: a column that
                is amber for every behind package says nothing about which
                ones matter. */}
            <TableCell>
              {p.upgradable && (
                <VersionTo
                  from={p.version}
                  to={p.upgradable}
                  security={p.security}
                  className="block truncate"
                />
              )}
            </TableCell>
            <TableCell className="numeric text-right text-muted-foreground">
              {p.size ? bytes(p.size) : "—"}
            </TableCell>
            {/* The row's fixed properties at its edge, side by side on the
                row's one line. */}
            <TableCell className="hidden text-right xl:table-cell">
              <span className="inline-flex items-center justify-end gap-1">
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

/**
 * The same packages where a table has no room for its columns: a card each,
 * one height, the name over the version it is at (or the one it would move
 * to, a dot in the logo's corner saying it is behind) and its size at the
 * edge. The table kept two truncated columns on a phone.
 */
function PackageCards({ packages }: { packages: InstalledPackage[] }) {
  return (
    <ChoiceList aria-label="Installed packages" className="pt-3">
      {packages.map((p) => (
        <ChoiceRow
          key={p.name}
          workspaceItem={{ id: p.name, name: p.name }}
          leading={
            <span className="relative flex">
              <PackageMark name={p.name} section={p.section} />
              {p.upgradable && (
                <span
                  aria-hidden
                  className="absolute -right-0.5 -bottom-0.5 flex size-2.5 items-center justify-center rounded-full bg-background"
                >
                  <StatusDot tone={p.security ? "warning" : "notice"} className="size-1.5" />
                </span>
              )}
            </span>
          }
          title={
            <span className="font-mono" title={p.summary}>
              {p.name}
            </span>
          }
          verb={p.name}
          description={
            p.upgradable ? (
              <>
                <span className="mr-1">to</span>
                <VersionTo from={p.version} to={p.upgradable} security={p.security} />
              </>
            ) : (
              <span className="font-mono">{p.version}</span>
            )
          }
          href={packageHref(p.name)}
          trailing={
            p.size ? (
              <span className="numeric text-hint text-muted-foreground">{bytes(p.size)}</span>
            ) : undefined
          }
        />
      ))}
    </ChoiceList>
  )
}
