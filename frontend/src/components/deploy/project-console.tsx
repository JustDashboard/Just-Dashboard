"use client"

import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { ContainerDetail } from "@/lib/types"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArrowRight, Slash, Terminal, Warning } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { Pane, PaneFooter } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { XtermPane } from "@/components/xterm-pane"
import { Button } from "@/components/ui/button"
import {
  DEFAULT_CHOICE,
  execQuery,
  healthReading,
  restartsLabel,
  runsAsRoot,
} from "@/components/deploy/console-session"
import { useProject } from "@/components/deploy/project-context"
import { serviceProduct } from "@/components/deploy/service-product"
import { GameConsole } from "@/components/deploy/game/console"

/**
 * The shell's height: the window below the project's header, which is about
 * 17.5rem of it, so the pane's footer and its last rows — where the prompt
 * sits once output fills it — are on screen on a laptop without scrolling
 * the page. Never shorter than a usable terminal, never taller than a
 * readable one. The page's skeleton takes the same.
 */
export const CONSOLE_HEIGHT = "h-[clamp(26rem,calc(100dvh-17.5rem),48rem)]"

/**
 * A shell inside the running release — or, for a game server, the game's own
 * console — never a second way into a container the Docker owner does not
 * already audit.
 */
export function ProjectConsole() {
  const project = useProject()
  if (project.detail.deployment.profile === "game") {
    return <GameConsole projectId={project.projectId} />
  }
  return <DockerConsole />
}

/**
 * The shell, as one working region sized to the window: a single 40px strip
 * says what it is inside — the container drawn as its product, its name and
 * which release it belongs to — and the
 * terminal takes the rest. Two strips stacked before the first line of output
 * were 76px of chrome on a phone.
 *
 * Where a shell cannot open, the reason stands on the page by itself: the
 * project's navigation already names the page, so a "Console" title over a
 * single notice said it twice.
 */
function DockerConsole() {
  const { can } = useAuth()
  const project = useProject()
  const search = useSearchParams()
  const { runtime, deployment } = project.detail
  const services = runtime?.status === "available" ? runtime.services : []
  const running = services.filter((service) => service.state === "running")
  const preferred = running.find((service) => service.liveRelease) ?? running[0]
  const selected = search.get("service")
  const service = running.find((item) => item.containerId === selected) ?? preferred
  // The restart count and the image's user are not on the runtime list; they
  // are the container's own, and the strip says what it shows without them.
  const containerId = service?.containerId
  const detail = usePoll<ContainerDetail>(
    (signal) =>
      get(`/docker/containers/${encodeURIComponent(containerId ?? "")}`, undefined, signal),
    30_000,
    [containerId],
    { enabled: containerId !== undefined && can("terminal") },
  ).data
  // Undefined for a release the list has not brought yet: its id is not its number.
  const releaseNumber = (releaseId: number) =>
    project.releases.find((release) => release.id === releaseId)?.number
  const product = (image: string | undefined) =>
    serviceProduct(image, deployment.sourceKind, project.product)

  if (!can("terminal")) {
    return (
      <Notice title="Terminal access is not granted" icon={Slash}>
        Opening a shell inside a deployment requires the terminal capability.
      </Notice>
    )
  }
  if (runtime?.status !== "available") {
    return (
      <Notice title="Runtime unavailable" tone="warning" icon={Warning}>
        <p>
          {runtime?.reason ??
            "Docker runtime evidence could not be loaded, so there is no container to open."}
        </p>
        <Button size="xs" variant="outline" asChild className="mt-2">
          <Link href="/docker">
            Open Docker <ArrowRight />
          </Link>
        </Button>
      </Notice>
    )
  }
  if (!service) {
    return (
      <EmptyState
        icon={Terminal}
        title="No running container"
        description="A shell opens inside the live release once a deployment is running. Deploy first, or open the Runtime tab to see what Docker reports."
        action={
          <Button size="sm" variant="outline" asChild>
            <Link href={`/deploy/${project.projectId}/runtime`}>
              Runtime <ArrowRight className="size-3.5" />
            </Link>
          </Button>
        }
      />
    )
  }

  const health = healthReading(service.health)
  const started = relativeTime(service.startedAt)
  const number = releaseNumber(service.releaseId)
  const root = runsAsRoot(DEFAULT_CHOICE.runAs, detail?.user)

  return (
    <div className="space-y-3">
      <Pane className={CONSOLE_HEIGHT}>
        <XtermPane
          key={service.containerId}
          flush
          minimalToolbar
          path={`/docker/containers/${service.containerId}/exec`}
          query={execQuery(DEFAULT_CHOICE)}
          className="min-h-0 flex-1"
          headerContent={
            <>
              <ProductGlyph id={product(service.image)} className="mx-1" />
              <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">
                {service.name} · deployment shell
              </span>
              <Status
                tone={service.liveRelease ? "running" : "stopped"}
                label={
                  service.liveRelease
                    ? "Live"
                    : number !== undefined
                      ? `Release #${number}`
                      : "Other release"
                }
                className="mx-1.5 max-sm:hidden"
              />
              {root && <Tag tone="warning">root</Tag>}
              <span className="mx-1.5 flex items-center gap-3 text-xs whitespace-nowrap text-muted-foreground max-md:hidden">
                {health && <Status tone={health.tone} label={health.label} />}
                {started !== "—" && <span className="numeric">Started {started}</span>}
                {detail && (
                  <span className={cn("numeric", detail.restartCount > 0 && "text-warning")}>
                    {restartsLabel(detail.restartCount)}
                  </span>
                )}
              </span>
            </>
          }
        />
        <PaneFooter>
          <p className="min-w-0 flex-1 text-hint text-muted-foreground">
            Commands run inside <span className="font-mono">{service.name}</span>
            {number !== undefined && <>, release #{number},</>} not on the host. The next release
            replaces this container, and this shell and anything written in it go with it.
          </p>
          <div className="flex shrink-0 items-center gap-1">
            <Button size="xs" variant="ghost" asChild>
              <Link href={`/deploy/${project.projectId}/logs?service=${service.containerId}`}>
                Logs
              </Link>
            </Button>
            <Button size="xs" variant="ghost" asChild>
              <Link href={`/docker/containers/${service.containerId}`}>Open in Docker</Link>
            </Button>
          </div>
        </PaneFooter>
      </Pane>
    </div>
  )
}
