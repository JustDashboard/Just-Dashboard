"use client"

import { useMemo, useState } from "react"
import {
  ArrowCircleUp,
  Box,
  CloudDownload,
  Pause,
  Puzzle,
  RefreshClockwise,
  RotateCounterClockwise,
  Servers,
  ShieldOff,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { bytes, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { InstalledPackage, Job, PackageInventory, UpdateReport } from "@/lib/types"
import { useViewState } from "@/lib/view-state"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, RecentJobs, useJobConsole } from "@/components/job-console"
import { InstallPanel } from "@/components/packages/install-panel"
import { PackageSheet } from "@/components/packages/package-sheet"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
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
 */

/** Rows rendered at once. See the footer below for why there is a cap at all. */
const MAX_ROWS = 400

type Scope = "explicit" | "all" | "upgradable"

export default function PackagesPage() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [filter, setFilter] = useState("")
  // How the inventory is arranged, and which half of the page you were on.
  // The search box above is not remembered: it is the question, not the
  // furniture.
  const [tab, setTab] = useViewState("packages.tab", "installed")
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

  return (
    <Page>
      {dialog}
      <PageHeader
        eyebrow="Operations"
        title="Packages"
        actions={
          <>
            <RecentJobs kinds={["updates.", "packages."]} onOpen={console_.open} />
            {data?.canRefresh && can("service.control") && (
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

      <StatGrid columns={4}>
        <StatTile
          label="Installed"
          icon={Box}
          value={data?.available ? data.packages.length.toLocaleString() : data ? "n/a" : "—"}
          hint={
            data?.manager
              ? data.indexAge
                ? `${data.manager} · index ${relativeTime(data.indexAge)}`
                : `managed by ${data.manager}`
              : "no supported package manager"
          }
        />
        <StatTile
          label="Installed by hand"
          icon={Pause}
          value={knowsExplicit ? data!.explicitCount.toLocaleString() : "—"}
          hint={
            knowsExplicit
              ? "the rest came as dependencies"
              : data?.available
                ? `${data.manager} does not record this`
                : undefined
          }
        />
        <StatTile
          label="Updates"
          icon={Puzzle}
          value={data?.available ? (data.upgradeCount ?? 0) : "—"}
          tone={
            (data?.securityCount ?? 0) > 0
              ? "warning"
              : (data?.upgradeCount ?? 0) > 0
                ? "default"
                : data?.available
                  ? "success"
                  : "default"
          }
          hint={
            data?.available
              ? (data.securityCount ?? 0) > 0
                ? `${data.securityCount} security · read ${relativeTime(data.readAt)}`
                : `read ${relativeTime(data.readAt)}`
              : undefined
          }
        />
        <StatTile
          label="On disk"
          icon={Servers}
          value={data?.totalSize ? bytes(data.totalSize) : "—"}
          hint="what the installed packages occupy"
        />
      </StatGrid>

      {/* The decision. Security updates are the reason to be on this page in a
          hurry, and the button to act is here rather than three tabs in. */}
      {canUpgrade && report?.securityFiltering && (report?.securityCount ?? 0) > 0 && (
        <Panel>
          <PanelHeader
            title={`${report.securityCount} security update${report.securityCount === 1 ? "" : "s"} outstanding`}
            actions={
              <Button size="sm" disabled={applying} onClick={() => upgrade(true)}>
                <ShieldOff className="size-4" />
                Install security updates
              </Button>
            }
          />
        </Panel>
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
        </Notice>
      )}

      <JobConsole
        job={console_.job}
        lines={console_.lines}
        onDismiss={console_.dismiss}
        onCancel={console_.cancel}
      />

      {inventory.error && <ErrorState error={inventory.error} />}
      {inventory.loading && !data && (
        <>
          <Skeleton className="h-9 w-64 rounded-lg" />
          <LoadingPanel />
        </>
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

      {data?.available && (
        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="installed">Installed</TabsTrigger>
            <TabsTrigger value="updates">
              Updates
              {(data.upgradeCount ?? 0) > 0 && (
                <span
                  className={cn(
                    "numeric ml-1.5 text-hint font-medium",
                    (data.securityCount ?? 0) > 0 ? "text-warning" : "text-muted-foreground",
                  )}
                >
                  {data.upgradeCount}
                </span>
              )}
            </TabsTrigger>
            <TabsTrigger value="install">Add software</TabsTrigger>
          </TabsList>

          <TabsContent value="installed">
            <Panel>
              <PanelHeader
                title="Installed packages"
                actions={
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => setBySize((v) => !v)}
                    title="Sort by what each package occupies"
                  >
                    <Servers className="size-4" />
                    {bySize ? "By size" : "By name"}
                  </Button>
                }
              />
              <PanelToolbar>
                <SearchInput
                  value={filter}
                  onChange={(e) => setFilter(e.target.value)}
                  placeholder="Filter by name, description or section"
                />
                <ToggleGroup
                  type="single"
                  size="sm"
                  value={effectiveScope}
                  onValueChange={(v) => v && setScope(v as Scope)}
                >
                  {knowsExplicit && (
                    <ToggleGroupItem value="explicit">Installed by hand</ToggleGroupItem>
                  )}
                  <ToggleGroupItem value="all">Everything</ToggleGroupItem>
                  <ToggleGroupItem value="upgradable">Behind</ToggleGroupItem>
                </ToggleGroup>
              </PanelToolbar>
              <PanelBody flush>
                {visible.length === 0 ? (
                  <EmptyState
                    icon={Puzzle}
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
                  <PackageTable packages={visible.slice(0, MAX_ROWS)} onInspect={setInspect} />
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
          </TabsContent>

          <TabsContent value="updates">
            <Panel>
              <PanelHeader
                title="Waiting to be upgraded"
                actions={
                  canUpgrade && (
                    <Button
                      size="sm"
                      variant="outline"
                      disabled={applying}
                      onClick={() => upgrade(false)}
                    >
                      <ArrowCircleUp className="size-4" />
                      Upgrade all {report?.packages.length ?? data.upgradeCount}
                    </Button>
                  )
                }
              />
              <PanelBody flush>
                {updates.loading && !report ? (
                  <LoadingPanel />
                ) : !report || report.packages.length === 0 ? (
                  <EmptyState
                    icon={Puzzle}
                    title="Everything is up to date"
                    description={
                      report?.securityFiltering
                        ? "No packages are waiting to be upgraded."
                        : `${data.manager} publishes no advisory data, so a clean list here means only that nothing at all is pending.`
                    }
                  />
                ) : (
                  <Table containerClassName="max-h-[calc(100svh-30rem)]">
                    <TableHeader className={stickyTableHeader}>
                      <TableRow>
                        <TableHead>Package</TableHead>
                        <TableHead>Installed</TableHead>
                        <TableHead>Available</TableHead>
                        <TableHead className="w-full">Origin</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {report.packages.map((p) => (
                        <TableRow
                          key={p.name}
                          className="cursor-pointer"
                          onClick={() => setInspect(p.name)}
                        >
                          <TableCell>
                            <div className="flex items-center gap-2">
                              <span className="text-body font-medium">{p.name}</span>
                              {p.security && <Tag tone="warning">security</Tag>}
                            </div>
                          </TableCell>
                          <TableCell className="font-mono text-muted-foreground">
                            {p.current || "—"}
                          </TableCell>
                          <TableCell className="font-mono">{p.candidate}</TableCell>
                          <TableCell>
                            <p
                              className="max-w-[18rem] truncate font-mono text-hint text-muted-foreground"
                              title={p.origin}
                            >
                              {p.origin || "—"}
                            </p>
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
              </PanelBody>
            </Panel>
          </TabsContent>

          <TabsContent value="install">
            <InstallPanel manager={data.manager} onJob={console_.attach} onInspect={setInspect} />
          </TabsContent>
        </Tabs>
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
          <TableHead className="text-right">Size</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {packages.map((p) => (
          <TableRow key={p.name} className="cursor-pointer" onClick={() => onInspect(p.name)}>
            <TableCell>
              <div className="flex items-center gap-2">
                <span className="font-mono text-body font-medium">{p.name}</span>
                {p.security ? (
                  <Tag tone="warning">security</Tag>
                ) : p.upgradable ? (
                  <span className="font-mono text-hint text-muted-foreground">{p.upgradable}</span>
                ) : null}
                {p.essential && <Tag>essential</Tag>}
              </div>
            </TableCell>
            <TableCell>
              <p className="max-w-[38rem] truncate text-xs text-muted-foreground">
                {p.summary || "—"}
              </p>
            </TableCell>
            <TableCell
              className={cn(
                "font-mono text-xs",
                p.upgradable ? "text-warning" : "text-muted-foreground",
              )}
            >
              {p.version}
            </TableCell>
            <TableCell className="numeric text-right text-muted-foreground">
              {p.size ? bytes(p.size) : "—"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
