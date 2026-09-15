"use client"

import { useMemo, useState } from "react"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArchivedDeployments } from "@/components/deploy/deployment-archive"
import {
  ArrowRight,
  CloudUpload,
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
import { Detail, DetailList, Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  DeploymentStatus,
  HealthStatus,
  humanize,
  reachableAt,
  releaseLabel,
} from "@/components/deploy/deployment-ui"
import { Button } from "@/components/ui/button"
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
                  Deploy something
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
        <Panel>
          <PanelHeader
            title="Deployment fleet"
            actions={
              <div className="flex flex-wrap items-center gap-3">
                <span className="numeric text-xs text-muted-foreground">
                  {deployments.length} of {fleet.data.deployments.length}
                </span>
                <div
                  className="hidden items-center gap-1 xl:flex"
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
          />
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search deployments"
              aria-label="Search deployments"
              containerClassName="sm:w-64"
            />
            <Select value={state} onValueChange={setState}>
              <SelectTrigger size="sm" className="w-[9.5rem]" aria-label="Filter by status">
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
              <SelectTrigger size="sm" className="w-[9.5rem]" aria-label="Filter by type">
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
              <SelectTrigger size="sm" className="w-[9.5rem]" aria-label="Filter by environment">
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
          </PanelToolbar>
          <PanelBody flush>
            {deployments.length === 0 ? (
              <EmptyState
                icon={CloudUpload}
                title={
                  fleet.data.deployments.length === 0
                    ? "Nothing has been deployed through this dashboard yet"
                    : "No deployments match"
                }
                description={
                  fleet.data.deployments.length === 0
                    ? "This page covers workloads the deploy engine created and tracks. Containers and Compose stacks already running on the host are not listed here until you adopt one — start from a repository, image, Compose stack, game server, or an existing workload."
                    : "Change or clear the filters to see the rest of the fleet."
                }
                action={
                  fleet.data.deployments.length === 0 ? (
                    <div className="flex flex-wrap items-center justify-center gap-2">
                      {can("system.admin") && (
                        <Button size="sm" asChild>
                          <Link href="/deploy/new">Deploy something</Link>
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
          </PanelBody>
        </Panel>
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
    <div className="hidden xl:block">
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
    </div>
  )
}

function FleetCards({ deployments, list }: { deployments: DeploymentSummary[]; list: boolean }) {
  return (
    <ul
      aria-label="Deployment projects"
      className={cn(
        "grid gap-px bg-hairline",
        list ? "xl:hidden" : "lg:grid-cols-2 2xl:grid-cols-3",
      )}
    >
      {deployments.map((deployment) => (
        <li key={deployment.id} className="min-w-0 bg-card">
          <Link
            href={`/deploy/${deployment.id}`}
            className="flex min-h-40 min-w-0 flex-col gap-3 p-4 focus-ring-inset hover:bg-row-hover"
          >
            <div className="flex min-w-0 items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate text-title font-medium">{deployment.name}</p>
                <p className="text-hint text-muted-foreground">
                  {humanize(deployment.profile)} · {deployment.environmentName}
                </p>
              </div>
              {deployment.pendingChanges && <Status verdict="warning" label="Pending" />}
            </div>
            <DetailList>
              <Detail label="Release">
                <span className="block truncate font-mono" title={deployment.sourceRevision}>
                  {releaseLabel(deployment)}
                </span>
              </Detail>
              <Detail label="Reachable at">
                <span className="block truncate font-mono" title={deployment.endpoint}>
                  {reachableAt(deployment)}
                </span>
              </Detail>
              <Detail label="Health">
                <HealthStatus health={deployment.health} />
              </Detail>
              <Detail label="Last deploy">
                {deployment.lastRun ? (
                  <DeploymentStatus state={deployment.lastRun.state} />
                ) : (
                  "Never"
                )}
              </Detail>
            </DetailList>
          </Link>
        </li>
      ))}
    </ul>
  )
}
