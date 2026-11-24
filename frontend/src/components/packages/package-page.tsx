"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { Fact, FactLine } from "@/components/fact-line"
import { ArrowCircleUp, ArrowRight, Download, Puzzle, Trash } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { HostInfo, Job, PackageDetail, PackageInventory, PackageUsage } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { DetailPage, useDetailTab, useSiblingSteps, type Step } from "@/components/detail-page"
import { IdentityHeader } from "@/components/identity-header"
import { ProductLogo, platformProduct } from "@/components/product-logo"
import { EmptyState, ErrorState } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status, StatusDot } from "@/components/status-dot"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { PACKAGE_TABS, packageHref, type PackageTab } from "@/components/packages/inventory"
import { PackageLogView } from "@/components/packages/log-view"
import {
  VersionTo,
  managerProduct,
  packageProduct,
  sectionGlyph,
} from "@/components/packages/marks"
import { PackageRail, type RailPackage } from "@/components/packages/package-rail"
import {
  PackageDepends,
  PackageFiles,
  PackageManual,
  PackageOverview,
  PackagePicture,
  filesCount,
  installedLookup,
  needsOf,
} from "@/components/packages/package-views"
import { usePackageFilter } from "@/components/packages/use-package-filter"
import { decodeSegment } from "@/lib/route-params"

const BACK = { href: "/packages", label: "Packages" }

const VIEWS: TabView<PackageTab>[] = [
  { key: "overview", label: "Overview" },
  { key: "files", label: "Files" },
  { key: "manual", label: "Manual" },
  { key: "depends", label: "Depends" },
  { key: "log", label: "Log" },
]

type Reading = {
  name: string
  detail: PackageDetail
  usage: PackageUsage | null
  usageError?: string
}

/**
 * One package, as a place of its own.
 *
 * It was a `SidePanel` over the list until 0.8.7: everything a package says
 * about itself in a drawer's width, closed the moment a verb was pressed, so
 * an install's outcome was read off the list behind it. A package is a
 * destination with a way back now, the way a service and a process are: its
 * identity line with its state and its one command, then its views as the
 * view strip, named for what the two reads hold. Overview is the
 * picture of what it needs and what it gives; Files is what its file list put
 * on the server; Manual its man page; Depends everything it needs, one line
 * each; Log the package manager's own record, narrowed to it. There is no
 * changelog view because no manager is asked for one.
 *
 * The installed list stays beside it from `xl` (`PackageRail`) with the
 * list's own search, scope and order, Alt+↑ and Alt+↓ walk it, and the view
 * on screen goes with you. On a phone the page is the one pane, with the way
 * back and the same two arrows.
 *
 * A verb's output is read here: the console opens under the identity line and
 * the package is read again when the run ends, so the line that said "Not
 * installed" says "Installed" without leaving.
 */
export function PackagePage() {
  const params = useParams<{ name: string }>()
  const router = useRouter()
  const name = decodeSegment(params.name)
  const [tab, setTab] = useDetailTab(PACKAGE_TABS, { remember: "packages.page.tab" })
  const hrefFor = useCallback((next: string) => packageHref(next, tab), [tab])

  const inventory = usePoll(
    (signal) => get<PackageInventory>("/packages/", undefined, signal),
    120000,
  )
  const filter = usePackageFilter(inventory.data)
  const steps = useSiblingSteps(
    filter.rows.map((p) => p.name),
    name,
    (next) => router.push(hrefFor(next)),
  )

  // The two reads are fired together rather than the usage one waiting for the
  // detail: they are independent on the server and the manual is the slower of
  // the two, so serialising them would show a package's own name a second
  // before anything else on the page.
  const load = useCallback(
    async (signal: AbortSignal): Promise<Reading> => {
      const [d, u] = await Promise.allSettled([
        get<PackageDetail>(`/packages/${encodeURIComponent(name)}`, undefined, signal),
        get<PackageUsage>(`/packages/${encodeURIComponent(name)}/usage`, undefined, signal),
      ])
      if (d.status === "rejected") throw d.reason
      // A package that is not installed owns no files, so an unreadable usage
      // is the normal case rather than a failure worth reporting.
      return {
        name,
        detail: d.value,
        usage: u.status === "fulfilled" ? u.value : null,
        usageError: u.status === "rejected" ? errorMessage(u.reason) : undefined,
      }
    },
    [name],
  )
  // Fetched once rather than polled: a package's description and its manual do
  // not change while somebody is reading them, and re-rendering a forty
  // kilobyte man page every five seconds would scroll it out from under them.
  const reading = usePoll(load, 0, [name], { enabled: name !== "" })

  const refreshReading = reading.refresh
  const refreshInventory = inventory.refresh
  const console_ = useJobConsole({
    onSuccess: () => {
      refreshReading()
      refreshInventory()
    },
  })

  const railCurrent: RailPackage | undefined =
    reading.data?.name === name
      ? {
          name,
          version: reading.data.detail.installedVersion,
          section: reading.data.detail.section,
          summary: reading.data.detail.summary,
          size: reading.data.detail.installed ? reading.data.detail.size : undefined,
        }
      : undefined
  const narrowed = filter.narrowed
  const caption = inventory.data
    ? `${
        filter.query || narrowed || filter.rows.length !== filter.total
          ? `${filter.rows.length.toLocaleString()} of ${filter.total.toLocaleString()}`
          : plural(filter.total, "package")
      }${narrowed ? ` · ${narrowed}` : ""}${filter.bySize ? " · largest first" : ""}`
    : "Reading the packages…"

  const frame: Frame = {
    steps,
    refresh: () => {
      reading.refresh()
      inventory.refresh()
    },
    rail: (
      <PackageRail
        packages={filter.rows}
        current={name}
        currentPackage={railCurrent}
        loading={inventory.loading}
        error={inventory.data ? undefined : inventory.error}
        onRetry={inventory.refresh}
        query={filter.query}
        onQuery={filter.setQuery}
        filtered={Boolean(narrowed)}
        onClearFilters={() => {
          filter.setScope("all")
          filter.setSoftware("")
        }}
        caption={caption}
        hrefFor={hrefFor}
      />
    ),
  }

  // Keyed, so another package opens on its own state, never on this one's
  // pending install.
  return (
    <PackageMain
      key={name}
      frame={frame}
      name={name}
      reading={reading}
      inventory={inventory.data}
      console_={console_}
      tab={tab}
      setTab={setTab}
      hrefFor={hrefFor}
    />
  )
}

/** What every state of the page hands the shared frame. */
type Frame = { steps: { previous: Step; next: Step }; refresh: () => void; rail: React.ReactNode }

/** The package's detail frame: the way back, the walk and the rail (`DetailPage`). */
function PackageFrame({
  frame,
  title,
  identity,
  children,
}: {
  frame: Frame
  title: string
  identity?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <DetailPage
      name="Package"
      what="package"
      stateKey="package.page"
      refresh={frame.refresh}
      steps={frame.steps}
      back={BACK}
      title={title}
      rail={frame.rail}
      identity={identity}
    >
      {children}
    </DetailPage>
  )
}

function PackageMain({
  frame,
  name,
  reading,
  inventory,
  console_,
  tab,
  setTab,
  hrefFor,
}: {
  frame: Frame
  name: string
  reading: ReturnType<typeof usePoll<Reading>>
  inventory?: PackageInventory
  console_: ReturnType<typeof useJobConsole>
  tab: PackageTab
  setTab: (tab: string) => void
  hrefFor: (name: string) => string
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  // usePoll keeps the previous answer across a deps change, which is right for
  // a table that should not blank between refreshes and wrong here: it would
  // draw the last package's manual under this one's name.
  const current = reading.data?.name === name ? reading.data : undefined
  const detail = current?.detail
  const usage = current?.usage ?? null
  const error = current ? undefined : reading.error

  // Read once: which distribution the manager belongs to does not change
  // while the page is open.
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0, [], {
    key: "/system/host",
  }).data
  const installedOf = useMemo(() => installedLookup(inventory?.packages), [inventory])
  const row = installedOf(name)

  // A run started somewhere else (the Install on a search result) is still
  // this package's run: the page attaches to it rather than offering a second
  // Install over the first.
  const jobs = usePoll<Job[]>((signal) => get<Job[]>("/jobs/", undefined, signal), 5000, [], {
    key: "/jobs",
  }).data
  const running = (jobs ?? []).find(
    (job) =>
      job.status === "running" &&
      job.kind.startsWith("packages.") &&
      (job.target ?? "").split(", ").includes(name),
  )
  const attach = console_.attach
  // Once each: a run the reader dismissed must not come back because the
  // list of jobs is five seconds behind the console.
  const attachedOnce = useRef(new Set<string>())
  useEffect(() => {
    if (!running || attachedOnce.current.has(running.id)) return
    attachedOnce.current.add(running.id)
    attach(running)
  }, [running, attach])

  const job = console_.job
  const mine = job && (job.target ?? "").split(", ").includes(name) ? job : null
  const acting = mine?.status === "running" ? mine : null

  const start = useCallback(
    async (path: string, body: object) => {
      setBusy(true)
      try {
        const started = await post<Job>(path, body)
        attachedOnce.current.add(started.id)
        attach(started)
      } finally {
        setBusy(false)
      }
    },
    [attach],
  )
  const install = () =>
    void start("/packages/install", { packages: [name] }).catch((err) =>
      notify.error("Could not start the install", err),
    )
  const remove = (purge: boolean) =>
    confirm({
      title: purge ? `Remove ${name} and its configuration` : `Remove ${name}`,
      confirmLabel: "Remove",
      description: purge ? (
        <>
          <p>
            Removes the package <b>and deletes the files it put in /etc</b>. Anything you edited
            there — a config file you spent an afternoon on — goes with it.
          </p>
          <p className="text-muted-foreground">
            Dependencies that were only installed for this are removed too.
          </p>
        </>
      ) : (
        <>
          <p>
            Removes the package and the dependencies that were only there for it. Configuration in
            /etc is left where it is.
          </p>
          <p className="text-muted-foreground">
            Installing it again from the same repository puts it back.
          </p>
        </>
      ),
      action: async () => {
        await start("/packages/remove", { packages: [name], purge })
      },
    })

  if (error) {
    const notFound = error instanceof ApiError && [400, 404].includes(error.status)
    return (
      <PackageFrame frame={frame} title={name || "Package"}>
        {notFound ? (
          <EmptyState
            icon={Puzzle}
            title={`No package named ${name || "that"}`}
            description="Neither this host nor its repositories know the name. It may be misspelt, or the index may be older than the package."
            action={
              <div className="flex flex-wrap items-center justify-center gap-2">
                <Button size="sm" variant="outline" asChild>
                  <Link href={`/packages?q=${encodeURIComponent(name)}`}>Search for it</Link>
                </Button>
                <Link href="/packages" className="text-body font-medium text-brand hover:underline">
                  Back to the packages
                </Link>
              </div>
            }
          />
        ) : (
          <ErrorState error={error} onRetry={reading.refresh} retryable />
        )}
      </PackageFrame>
    )
  }

  const canInstall = can("system.admin")
  // Removing is a loss as well as a run: the route asks for both.
  const canRemove = canInstall && can("destructive")
  const upgradable = detail?.upgradable
  const security = Boolean(upgradable && row?.security)
  const verbs: Verb[] =
    detail?.installed && !detail.protected && canRemove
      ? [
          {
            key: "remove",
            label: "Remove",
            icon: Trash,
            danger: true,
            disabled: busy || Boolean(acting),
            run: () => remove(false),
          },
          ...(inventory?.canPurge
            ? [
                {
                  key: "purge",
                  label: "Remove and purge",
                  icon: Trash,
                  danger: true,
                  disabled: busy || Boolean(acting),
                  run: () => remove(true),
                },
              ]
            : []),
        ]
      : []
  const needs = detail ? needsOf(detail).length : 0
  const counts: Partial<Record<PackageTab, number>> = {
    files: detail?.installed ? filesCount(usage) : 0,
    depends: needs,
  }
  const product = platformProduct(host?.platform) ?? managerProduct(inventory?.manager)
  const picture = (
    <PackagePicture
      detail={detail}
      usage={usage}
      usageError={current?.usageError}
      manager={inventory?.manager}
      installedOf={installedOf}
      inventoryRead={Boolean(inventory)}
      moving={Boolean(acting)}
      hrefFor={hrefFor}
      onTab={setTab}
    />
  )

  return (
    <PackageFrame
      frame={frame}
      title={name}
      identity={
        <>
          <IdentityHeader
            label={`About ${name}`}
            slot="package-identity"
            className="border-b-0 pb-0"
            logo={
              <ProductLogo
                id={packageProduct(name)}
                fallback={sectionGlyph(detail?.section ?? row?.section)}
                className="size-10 rounded-lg [&_img]:size-6"
              />
            }
            title={<span className="min-w-0 truncate font-mono">{name}</span>}
            state={
              !detail ? (
                <TextShimmer className="text-body">Reading the package…</TextShimmer>
              ) : acting ? (
                <span className="inline-flex items-center gap-1.5 text-body font-medium">
                  <StatusDot tone="notice" />
                  <TextShimmer>{`${runWord(acting, detail.installed)}…`}</TextShimmer>
                </span>
              ) : (
                <>
                  <Status
                    verdict={detail.installed ? "ok" : "notice"}
                    label={detail.installed ? "Installed" : "Not installed"}
                    className="text-body"
                  />
                  {upgradable && (
                    <Status
                      verdict={security ? "warning" : "notice"}
                      label={security ? "Security update" : "Update available"}
                      className="text-body"
                    />
                  )}
                </>
              )
            }
            command={
              detail && canInstall && (!detail.installed || upgradable) ? (
                <Button
                  size="sm"
                  pending={busy}
                  disabled={Boolean(acting)}
                  onClick={install}
                  title={upgradable ? `Update to ${upgradable}` : undefined}
                >
                  {upgradable ? (
                    <ArrowCircleUp className="size-3.5" />
                  ) : (
                    <Download className="size-3.5" />
                  )}
                  {upgradable ? "Update" : "Install"}
                </Button>
              ) : undefined
            }
            menu={verbs.length > 0 && <VerbMenu verbs={verbs} label={`Actions on ${name}`} />}
            facts={
              // The same rows whatever a package says about itself, so the views
              // under the line do not move when it lands. One line from `sm`: the
              // version it is at and the one an update lands on, its size, then
              // what it is, which gives way. On a phone the description goes
              // under the figures. Beside the rail at 1280 the line has under
              // 500px, which is why how it came to be installed is in About.
              <span className="flex w-full min-w-0 flex-col gap-y-1 sm:flex-row sm:items-center sm:gap-x-3">
                <FactLine nowrap className="sm:shrink-0">
                  {/* A flex row, as the line it was: the version in mono and the
                      update's arrow stand on one centre line. */}
                  <Fact className="flex items-center">
                    <span className="flex max-w-full min-w-0 items-center gap-x-3">
                      {detail ? (
                        <span
                          className="min-w-0 truncate font-mono text-foreground"
                          title={detail.installedVersion ?? detail.version}
                        >
                          {detail.installedVersion ?? detail.version ?? "—"}
                        </span>
                      ) : (
                        <span>&nbsp;</span>
                      )}
                      {upgradable && (
                        <span
                          className="inline-flex shrink-0 items-center gap-1.5"
                          title={`${security ? "A security update" : "An update"} to ${upgradable} is waiting`}
                        >
                          <ArrowRight aria-hidden className="size-3" />
                          <span className="sr-only">update to</span>
                          <VersionTo
                            from={detail?.installedVersion}
                            to={upgradable}
                            security={security}
                          />
                        </span>
                      )}
                    </span>
                  </Fact>
                  {detail?.size ? (
                    <Fact className="numeric shrink-0">
                      {bytes(detail.size)} {detail.installed ? "on disk" : "once installed"}
                    </Fact>
                  ) : null}
                </FactLine>
                {detail?.summary && (
                  <span aria-hidden className="text-faint max-sm:hidden">
                    ·
                  </span>
                )}
                <span className="min-w-0 truncate" title={detail?.summary}>
                  {detail?.summary || "\u00a0"}
                </span>
              </span>
            }
          />
          <JobConsole
            job={console_.job}
            lines={console_.lines}
            onDismiss={console_.dismiss}
            onCancel={console_.cancel}
          />
        </>
      }
    >
      <Tabs value={tab} onValueChange={setTab} className="gap-0">
        <ViewTabList
          label={`Views of ${name}`}
          views={VIEWS.map((view) => ({ ...view, count: counts[view.key] }))}
          value={tab}
          sticky
        />

        <TabsContent value="overview" className="pt-2">
          {detail ? (
            <PackageOverview
              detail={detail}
              picture={picture}
              came={
                row && inventory && inventory.explicitCount > 0
                  ? row.explicit
                    ? "by hand"
                    : "as a dependency"
                  : undefined
              }
              essential={Boolean(detail.essential || row?.essential)}
            />
          ) : (
            picture
          )}
        </TabsContent>
        <TabsContent value="files" className="pt-6">
          {tab === "files" &&
            (detail ? (
              <PackageFiles
                usage={usage}
                installed={detail.installed}
                error={current?.usageError}
                onRetry={reading.refresh}
                onInstall={canInstall && !acting ? install : undefined}
              />
            ) : (
              <LoadingRows rows={6} />
            ))}
        </TabsContent>
        <TabsContent value="manual" className="pt-6">
          {tab === "manual" &&
            (detail ? (
              <PackageManual
                usage={usage}
                installed={detail.installed}
                error={current?.usageError}
                onRetry={reading.refresh}
                onInstall={canInstall && !acting ? install : undefined}
              />
            ) : (
              <LoadingRows rows={6} />
            ))}
        </TabsContent>
        <TabsContent value="depends" className="pt-6">
          {tab === "depends" &&
            (detail ? (
              <PackageDepends
                detail={detail}
                installedOf={installedOf}
                inventoryRead={Boolean(inventory)}
                hrefFor={hrefFor}
              />
            ) : (
              <LoadingRows rows={6} />
            ))}
        </TabsContent>
        <TabsContent value="log" className="flex flex-col pt-4">
          {/* Keyed on the package so another package starts a clean pane. */}
          {tab === "log" && <PackageLogView key={name} product={product} about={name} />}
        </TabsContent>
      </Tabs>
      {dialog}
    </PackageFrame>
  )
}

/** What the manager is doing to the package, as the identity line says it. */
function runWord(job: Job, installed: boolean) {
  if (job.kind === "packages.remove") return "Removing"
  return installed ? "Updating" : "Installing"
}
