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
import { Page, PageHeader, SearchInput, Section, Toolbar } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
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
        eyebrow="Operations"
        title="Deployments"
        actions={
          <>
            <Button variant="outline" size="sm" asChild>
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
        <ActiveWorkStrip
          work={fleet.data.activeWork}
          slots={fleet.data.slots}
          canCancel={can("service.control")}
          onCancel={cancel}
        />
      )}

      {fleet.data && (
        <div className="space-y-5">
          <Section
            title="Projects"
            actions={
              <div className="flex flex-wrap items-center gap-3">
                <span className="numeric text-xs text-muted-foreground">
                  {deployments.length} of {fleet.data.deployments.length}
                </span>
                <div
                  className="flex items-center gap-1"
                  role="group"
                  aria-label="Deployment layout"
                >
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
              </div>
            }
          >
            {null}
          </Section>
          <Toolbar className="gap-3">
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search deployments"
              aria-label="Search deployments"
              containerClassName="min-w-0 flex-1 sm:w-auto"
            />
            <Popover>
              <PopoverTrigger asChild>
                <Button variant="outline" size="sm">
                  <Filter className="size-3.5" /> Filters
                  {(profile !== "all" ||
                    state !== "all" ||
                    environment !== "all" ||
                    pendingOnly) && (
                    <span className="numeric">
                      {Number(profile !== "all") +
                        Number(state !== "all") +
                        Number(environment !== "all") +
                        Number(pendingOnly)}
                    </span>
                  )}
                </Button>
              </PopoverTrigger>
              <PopoverContent align="end" className="w-72 space-y-3">
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
            {(query ||
              profile !== "all" ||
              state !== "all" ||
              environment !== "all" ||
              pendingOnly) && (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setQuery("")
                  setProfile("all")
                  setState("all")
                  setEnvironment("all")
                  setPendingOnly(false)
                }}
              >
                <Filter className="size-3.5" />
                Clear
              </Button>
            )}
          </Toolbar>
          <div>
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
        </div>
      )}
    </Page>
  )
}

function ActiveWorkStrip({
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
    <Panel aria-labelledby="active-work-title">
      <PanelHeader
        title={<span id="active-work-title">Active work</span>}
        actions={
          <span className="numeric text-hint text-muted-foreground">
            Build slots {slots.heavyUsed}/{slots.heavyCapacity} · control slots {slots.lightUsed}/
            {slots.lightCapacity}
          </span>
        }
      />
      <PanelBody flush>
        <ul className="divide-y divide-hairline" aria-live="polite" aria-atomic="true">
          {work.map((item) => (
            <li
              key={item.run.id}
              className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5"
            >
              <div className="min-w-40 flex-1">
                <Link
                  href={`/deploy/${item.run.projectId}/runs/${item.run.id}`}
                  className="text-body font-medium hover:underline"
                >
                  {item.projectName}
                </Link>
                <p className="truncate text-hint text-muted-foreground">
                  {item.environment} ·{" "}
                  {item.currentStep ? humanize(item.currentStep) : "Waiting for next step"}
                </p>
              </div>
              <DeploymentStatus state={item.run.state} />
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
            </li>
          ))}
        </ul>
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

function FleetCards({ deployments, list }: { deployments: DeploymentSummary[]; list: boolean }) {
  return (
    <ul
      aria-label="Deployment projects"
      className={cn("grid gap-4", list ? "md:hidden" : "md:grid-cols-2 2xl:grid-cols-3")}
    >
      {deployments.map((deployment) => {
        const url = deploymentURL(deployment.endpoint)
        return (
          <li key={deployment.id} className="min-w-0">
            <Panel className="group h-full transition-colors hover:border-muted-foreground/50">
              <div className="flex items-start gap-3 p-5 pb-3">
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
                      className="mt-1 block truncate rounded-sm text-xs text-muted-foreground focus-ring hover:text-foreground"
                    >
                      {new URL(url).host}
                    </a>
                  ) : (
                    <p className="mt-1 text-xs text-muted-foreground">
                      {deployment.liveReleaseId ? "Private service" : "No production deployment"}
                    </p>
                  )}
                </div>
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon-sm"
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
              <Link
                href={`/deploy/${deployment.id}`}
                className="flex min-h-20 flex-1 flex-col justify-end gap-2 px-5 pb-5 focus-ring-inset hover:bg-row-hover"
              >
                <div className="flex items-center gap-2 text-xs">
                  <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                  <span className="truncate" title={deployment.sourceRef}>
                    {deployment.sourceRef || humanize(deployment.profile)}
                  </span>
                  {deployment.sourceRevision && (
                    <span className="ml-auto font-mono text-hint text-muted-foreground">
                      {releaseLabel(deployment)}
                    </span>
                  )}
                </div>
                <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  <span>{deployment.environmentName}</span>
                  <span aria-hidden="true">·</span>
                  <span>
                    {relativeTime(deployment.lastRun?.requestedAt ?? deployment.updatedAt)}
                  </span>
                </div>
              </Link>
              <div className="flex min-h-11 flex-wrap items-center justify-between gap-2 border-t border-hairline px-5 py-3">
                {deployment.activeRun ? (
                  <DeploymentStatus state={deployment.activeRun.state} />
                ) : deployment.lastRun ? (
                  <DeploymentStatus state={deployment.lastRun.state} />
                ) : (
                  <span className="text-xs text-muted-foreground">Ready for first deploy</span>
                )}
                <div className="flex flex-wrap items-center gap-3">
                  <HealthStatus health={deployment.health} />
                  {deployment.pendingChanges && (
                    <Status verdict="warning" label="Pending changes" />
                  )}
                </div>
              </div>
            </Panel>
          </li>
        )
      })}
    </ul>
  )
}
