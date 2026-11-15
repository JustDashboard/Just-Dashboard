"use client"

import { get } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import type { ContainerDetail } from "@/lib/types"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { InfoTip } from "@/components/form"
import { Pane, PaneFooter } from "@/components/panel"
import { ProductGlyph } from "@/components/product-logo"
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
import { EmptyStep, ProjectEmpty } from "@/components/deploy/project-empty"
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
 * Where a shell cannot open, the page says so as Runtime does for a release
 * with no containers — the project's mark, one sentence, where to look next —
 * in the height the shell would take, so the page is one shape either way.
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

  const base = `/deploy/${project.projectId}`
  if (!can("terminal")) {
    return (
      <ProjectEmpty className={CONSOLE_HEIGHT} title="A shell here needs the terminal capability">
        <EmptyStep href={`${base}/logs`}>Logs</EmptyStep>
      </ProjectEmpty>
    )
  }
  if (runtime?.status !== "available") {
    return (
      <ProjectEmpty
        className={CONSOLE_HEIGHT}
        title="Docker did not answer, so there is no container to open"
        reason={runtime?.reason}
      >
        <EmptyStep href={`${base}/runtime`}>Runtime</EmptyStep>
        <EmptyStep href="/docker">Docker</EmptyStep>
      </ProjectEmpty>
    )
  }
  if (!service) {
    return (
      <ProjectEmpty
        className={CONSOLE_HEIGHT}
        title="No running container"
        reason="A shell opens inside the live release once one is running."
      >
        <EmptyStep href={`${base}/runtime`}>Runtime</EmptyStep>
      </ProjectEmpty>
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
          {/* One line: where the shell is. That it is not the host, and that
              the next release takes the container and this shell with it, is
              behind the mark beside it rather than a paragraph under every
              session. */}
          <p className="flex min-w-0 flex-1 items-center gap-1.5 text-hint text-muted-foreground">
            <span className="min-w-0 truncate">
              Inside <span className="font-mono text-foreground/80">{service.name}</span>
              {number !== undefined && <span className="numeric"> · release #{number}</span>}
            </span>
            <InfoTip label="What this shell reaches">
              Commands run inside the container, not on the host. The next release replaces this
              container, and this shell and anything written in it go with it.
            </InfoTip>
          </p>
          <div className="flex shrink-0 items-center gap-1">
            <Button size="xs" variant="ghost" asChild>
              <Link href={`${base}/logs?service=${service.containerId}`}>Logs</Link>
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
