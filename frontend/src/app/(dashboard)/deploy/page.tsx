"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArchivedDeployments } from "@/components/deploy/deployment-archive"
import {
  ArrowRight,
  CloudUpload,
  GitBranch,
  MoreHorizontal,
  Filter,
  GridSquare,
  ListUnordered,
  Plus,
  StopCircle,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DeploymentActiveWork, DeploymentFleet, DeploymentSummary } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader, SearchInput, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  DeploymentStatus,
  HealthStatus,
  humanize,
  reachableAt,
  releaseLabel,
  deploymentURL,
  WorkloadMark,
} from "@/components/deploy/deployment-ui"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Checkbox } from "@/components/ui/checkbox"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * The fleet: every project, and what is happening to it right now.
 *
 * One row of controls above a grid of cards. It used to be a section heading,
 * a count, a view switch, a toolbar and then the grid — four bands of chrome
 * before the first project — and every card closed with a strip of three
 * statuses whether or not any of them had something to say. A card now says
 * what it is, where it lives, what is deployed and how the last deployment
 * went; health and pending changes appear only when they are news.
 */
export default function DeployPage() {
  const { can } = useAuth()
  const archived = useSearchParams().get("view") === "archived"
  const fleet = usePoll(
    (signal) => get<DeploymentFleet>("/deploy/", { view: "fleet" }, signal),
    5000,
    [],
    { enabled: !archived },
  )
  const [query, setQuery] = useState("")
  const [profile, setProfile] = useState("all")
  const [state, setState] = useState("all")
  const [environment, setEnvironment] = useState("all")
  const [pendingOnly, setPendingOnly] = useState(false)
  const [layout, setLayout] = useState<"grid" | "list">("grid")

  const deployments = useMemo(() => {
    const search = query.trim().toLowerCase()
    return (fleet.data?.deployments ?? []).filter((deployment) => {
      if (
        search &&
        !`${deployment.name} ${deployment.endpoint ?? ""} ${deployment.sourceRef ?? ""}`
          .toLowerCase()
          .includes(search)
      )
        return false
      if (profile !== "all" && deployment.profile !== profile) return false
      if (environment !== "all" && deployment.environmentKind !== environment) return false
      if (state === "active" && !deployment.activeRun) return false
      if (state === "failed" && deployment.lastRun?.state !== "failed") return false
      if (state === "not-observed" && deployment.health !== "unavailable") return false
      if (pendingOnly && !deployment.pendingChanges) return false
      return true
    })
  }, [environment, fleet.data?.deployments, pendingOnly, profile, query, state])

  const filtered =
    profile !== "all" || state !== "all" || environment !== "all" || pendingOnly
  const filterCount =
    Number(profile !== "all") +
    Number(state !== "all") +
    Number(environment !== "all") +
    Number(pendingOnly)

  const cancel = async (work: DeploymentActiveWork) => {
    try {
      await post(`/deploy/${work.run.projectId}/runs/${work.run.id}/cancel`, {})
      notify.success(`Cancelling ${work.projectName}`, {
        description: "The run page will show cleanup progress.",
      })
      fleet.refresh()
    } catch (error) {
      notify.error("Could not cancel deployment", error)
    }
  }

  if (archived) return <ArchivedDeployments />

  return (
    <Page>
      <PageHeader
        eyebrow="Apps"
        title="Deployments"
        actions={
          <>
            <Button variant="ghost" size="sm" asChild>
              <Link href="/deploy?view=archived">Archived</Link>
            </Button>
            {can("system.admin") && (
              <Button size="sm" asChild>
                <Link href="/deploy/new">
                  <Plus className="size-4" />
                  New project
                </Link>
              </Button>
            )}
          </>
        }
      />

      {fleet.loading && !fleet.data && <LoadingPanel rows={5} />}
      {fleet.error && !fleet.data && (
        <div className="space-y-3">
          <ErrorState error={fleet.error} />
          <Button variant="outline" size="sm" onClick={fleet.refresh}>
            Try again
          </Button>
        </div>
      )}

      {fleet.data && fleet.data.activeWork.length > 0 && (
        <ActiveWork
          work={fleet.data.activeWork}
          slots={fleet.data.slots}
          canCancel={can("service.control")}
          onCancel={cancel}
        />
      )}

      {fleet.data && (
        <div className="space-y-5">
          <Toolbar className="gap-2">
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search deployments"
              aria-label="Search deployments"
              containerClassName="min-w-0 flex-1 sm:w-auto sm:max-w-sm"
            />
            <Popover>
              <PopoverTrigger asChild>
                <Button variant="ghost" size="sm" className="text-muted-foreground">
                  <Filter className="size-3.5" /> Filters
                  {filtered && <span className="numeric text-foreground">{filterCount}</span>}
                </Button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-72 space-y-3">
                <p className="text-sm font-medium">Filter projects</p>
                <Select value={state} onValueChange={setState}>
                  <SelectTrigger size="sm" className="w-full" aria-label="Filter by status">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All statuses</SelectItem>
                    <SelectItem value="active">Active work</SelectItem>
                    <SelectItem value="failed">Failed</SelectItem>
                    <SelectItem value="not-observed">Not observed</SelectItem>
                  </SelectContent>
                </Select>
                <Select value={profile} onValueChange={setProfile}>
                  <SelectTrigger size="sm" className="w-full" aria-label="Filter by type">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All types</SelectItem>
                    {(
                      [
                        "web",
                        "static",
                        "worker",
                        "image",
                        "compose",
                        "service",
                        "game",
                        "imported",
                      ] as const
                    ).map((value) => (
                      <SelectItem key={value} value={value}>
                        {humanize(value)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <Select value={environment} onValueChange={setEnvironment}>
                  <SelectTrigger size="sm" className="w-full" aria-label="Filter by environment">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="all">All environments</SelectItem>
                    <SelectItem value="production">Production</SelectItem>
                    <SelectItem value="staging">Staging</SelectItem>
                    <SelectItem value="preview">Preview</SelectItem>
                  </SelectContent>
                </Select>
                <Label className="flex min-h-8 items-center gap-2 rounded-md px-1.5 text-xs">
                  <Checkbox
                    checked={pendingOnly}
                    onCheckedChange={(checked) => setPendingOnly(checked === true)}
                  />
                  Pending only
                </Label>
              </PopoverContent>
            </Popover>
            {(query || filtered) && (
              <Button
                size="sm"
                variant="ghost"
                className="text-muted-foreground"
                onClick={() => {
                  setQuery("")
                  setProfile("all")
                  setState("all")
                  setEnvironment("all")
                  setPendingOnly(false)
                }}
              >
                Clear
              </Button>
            )}
            <span className="flex-1" />
            <span className="numeric text-hint text-muted-foreground">
              {deployments.length} of {fleet.data.deployments.length}
            </span>
            <div className="flex items-center gap-0.5" role="group" aria-label="Deployment layout">
              <Button
                variant={layout === "grid" ? "secondary" : "ghost"}
                size="icon-sm"
                aria-label="Grid view"
                aria-pressed={layout === "grid"}
                onClick={() => setLayout("grid")}
              >
                <GridSquare className="size-4" />
              </Button>
              <Button
                variant={layout === "list" ? "secondary" : "ghost"}
                size="icon-sm"
                aria-label="List view"
                aria-pressed={layout === "list"}
                onClick={() => setLayout("list")}
              >
                <ListUnordered className="size-4" />
              </Button>
            </div>
          </Toolbar>

          {deployments.length === 0 ? (
            <EmptyState
              icon={CloudUpload}
              title={
                fleet.data.deployments.length === 0
                  ? "Your next project starts here"
                  : "No deployments match"
              }
              description={
                fleet.data.deployments.length === 0
                  ? "Import a repository, launch a database, or start from an application template. Everything you deploy has a home here."
                  : "Change or clear the filters to see the rest of the fleet."
              }
              action={
                fleet.data.deployments.length === 0 ? (
                  <div className="flex flex-wrap items-center justify-center gap-2">
                    {can("system.admin") && (
                      <Button size="sm" asChild>
                        <Link href="/deploy/new">New project</Link>
                      </Button>
                    )}
                    <Button size="sm" variant="outline" asChild>
                      <Link href="/docker/stacks">See what is already running</Link>
                    </Button>
                  </div>
                ) : undefined
              }
            />
          ) : (
            <>
              {layout === "list" && <FleetTable deployments={deployments} />}
              <FleetCards deployments={deployments} list={layout === "list"} />
            </>
          )}
        </div>
      )}
    </Page>
  )
}

/**
 * Runs in flight, as a plain list above the fleet. It is the one thing on the
 * page that changes under the reader, so it gets the top and no frame.
 */
function ActiveWork({
  work,
  slots,
  canCancel,
  onCancel,
}: {
  work: DeploymentActiveWork[]
  slots: DeploymentFleet["slots"]
  canCancel: boolean
  onCancel: (work: DeploymentActiveWork) => void
}) {
  return (
    <Panel plain aria-labelledby="active-work-title">
      <PanelHeader
        title={<span id="active-work-title">Active work</span>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {slots.heavyUsed}/{slots.heavyCapacity} build · {slots.lightUsed}/
            {slots.lightCapacity} control
          </span>
        }
      />
      <PanelBody flush>
        <RowList aria-live="polite" aria-atomic="true">
          {work.map((item) => (
            <Row
              key={item.run.id}
              leading={<DeploymentStatus state={item.run.state} className="w-24" />}
              title={
                <Link
                  href={`/deploy/${item.run.projectId}/runs/${item.run.id}`}
                  className="rounded-sm focus-ring hover:underline"
                >
                  {item.projectName}
                </Link>
              }
              subtitle={`${item.environment} · ${
                item.currentStep ? humanize(item.currentStep) : "Waiting for next step"
              }`}
              trailing={
                <>
                  <span className="numeric text-hint text-muted-foreground">
                    {item.queuePosition
                      ? `Queue ${item.queuePosition}`
                      : relativeTime(item.run.claimedAt ?? item.run.requestedAt)}
                  </span>
                  <Button variant="outline" size="xs" asChild>
                    <Link href={`/deploy/${item.run.projectId}/runs/${item.run.id}`}>View</Link>
                  </Button>
                  {canCancel && !item.run.cancelRequested && (
                    <Button
                      variant="ghost"
                      size="xs"
                      className="text-destructive"
                      onClick={() => onCancel(item)}
                    >
                      <StopCircle className="size-3" />
                      Cancel
                    </Button>
                  )}
                </>
              }
            />
          ))}
        </RowList>
      </PanelBody>
    </Panel>
  )
}

function FleetTable({ deployments }: { deployments: DeploymentSummary[] }) {
  return (
    <Panel className="hidden md:block">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Deployment</TableHead>
            <TableHead>Environment</TableHead>
            <TableHead>Live release</TableHead>
            <TableHead>Reachable at</TableHead>
            <TableHead>Runtime health</TableHead>
            <TableHead>Last deployment</TableHead>
            <TableHead>Changes</TableHead>
            <TableHead>
              <span className="sr-only">Open</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {deployments.map((deployment) => (
            <TableRow key={deployment.id}>
              <TableCell>
                <Link href={`/deploy/${deployment.id}`} className="block min-w-0 hover:underline">
                  <span className="block truncate text-body font-medium">{deployment.name}</span>
                  <span className="block text-hint text-muted-foreground">
                    {humanize(deployment.profile)}
                  </span>
                </Link>
              </TableCell>
              <TableCell>{deployment.environmentName}</TableCell>
              <TableCell className="max-w-40 font-mono" title={deployment.sourceRevision}>
                {releaseLabel(deployment)}
              </TableCell>
              <TableCell className="max-w-48 truncate font-mono" title={deployment.endpoint}>
                {reachableAt(deployment)}
              </TableCell>
              <TableCell>
                <HealthStatus health={deployment.health} />
              </TableCell>
              <TableCell>
                {deployment.lastRun ? (
                  <DeploymentStatus state={deployment.lastRun.state} />
                ) : (
                  <span className="text-xs text-muted-foreground">Never</span>
                )}
              </TableCell>
              <TableCell>
                {deployment.pendingChanges ? (
                  <Status verdict="warning" label="Pending deployment" />
                ) : (
                  <Status state="running" label="Live" />
                )}
              </TableCell>
              <TableCell>
                <Button variant="ghost" size="icon-sm" asChild>
                  <Link href={`/deploy/${deployment.id}`} aria-label={`Open ${deployment.name}`}>
                    <ArrowRight className="size-4" />
                  </Link>
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Panel>
  )
}

/** Health worth a word: a healthy service is the normal case and says nothing. */
function healthIsNews(health: string) {
  return health !== "healthy" && health !== "passed"
}

function FleetCards({ deployments, list }: { deployments: DeploymentSummary[]; list: boolean }) {
  return (
    <ul
      aria-label="Deployment projects"
      className={cn("grid gap-5", list ? "md:hidden" : "md:grid-cols-2 2xl:grid-cols-3")}
    >
      {deployments.map((deployment) => {
        const url = deploymentURL(deployment.endpoint)
        const run = deployment.activeRun ?? deployment.lastRun
        return (
          <li key={deployment.id} className="min-w-0">
            <Panel interactive className="h-full">
              <div className="flex flex-1 flex-col gap-5 p-5">
                <div className="flex items-start gap-3">
                  <WorkloadMark profile={deployment.profile} />
                  <div className="min-w-0 flex-1">
                    <Link
                      href={`/deploy/${deployment.id}`}
                      className="block truncate rounded-sm text-title font-medium focus-ring hover:underline"
                    >
                      {deployment.name}
                    </Link>
                    {url ? (
                      <a
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="mt-0.5 block truncate rounded-sm text-xs text-muted-foreground focus-ring hover:text-foreground"
                      >
                        {new URL(url).host}
                      </a>
                    ) : (
                      <p className="mt-0.5 text-xs text-muted-foreground">
                        {deployment.liveReleaseId ? "Private service" : "Not deployed yet"}
                      </p>
                    )}
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-sm"
                        className="-mt-1 -mr-2 text-muted-foreground"
                        aria-label={`Actions for ${deployment.name}`}
                      >
                        <MoreHorizontal className="size-4" />
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem asChild>
                        <Link href={`/deploy/${deployment.id}`}>Open project</Link>
                      </DropdownMenuItem>
                      <DropdownMenuItem asChild>
                        <Link href={`/deploy/${deployment.id}?tab=logs`}>View runtime logs</Link>
                      </DropdownMenuItem>
                      <DropdownMenuItem asChild>
                        <Link href={`/deploy/${deployment.id}?tab=configuration`}>
                          Project settings
                        </Link>
                      </DropdownMenuItem>
                      {url && (
                        <DropdownMenuItem asChild>
                          <a href={url} target="_blank" rel="noopener noreferrer">
                            Visit website
                          </a>
                        </DropdownMenuItem>
                      )}
                    </DropdownMenuContent>
                  </DropdownMenu>
                </div>

                {/* The rest of the card is one link: what is deployed, and how
                    the last deployment went. */}
                <Link
                  href={`/deploy/${deployment.id}`}
                  className="-mx-2 -mb-2 flex flex-col gap-2 rounded-md px-2 py-2 text-xs focus-ring-inset hover:bg-row-hover"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate" title={deployment.sourceRef}>
                      {deployment.sourceRef || humanize(deployment.profile)}
                    </span>
                    {deployment.sourceRevision && (
                      <span className="ml-auto shrink-0 font-mono text-hint text-muted-foreground">
                        {releaseLabel(deployment)}
                      </span>
                    )}
                  </span>
                  <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-muted-foreground">
                    {run ? (
                      <DeploymentStatus state={run.state} />
                    ) : (
                      <span className="text-xs">Ready for first deploy</span>
                    )}
                    <span aria-hidden="true">·</span>
                    <span>
                      {relativeTime(deployment.lastRun?.requestedAt ?? deployment.updatedAt)}
                    </span>
                    <span aria-hidden="true">·</span>
                    <span>{deployment.environmentName}</span>
                    {healthIsNews(deployment.health) && (
                      <>
                        <span aria-hidden="true">·</span>
                        <HealthStatus health={deployment.health} />
                      </>
                    )}
                  </span>
                </Link>
              </div>
              {deployment.pendingChanges && (
                <div className="flex items-center border-t border-hairline px-5 py-2.5">
                  <Status verdict="warning" label="Pending changes" />
                </div>
              )}
            </Panel>
          </li>
        )
      })}
    </ul>
  )
}
