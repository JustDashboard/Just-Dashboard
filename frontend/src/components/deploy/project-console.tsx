"use client"

import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import { useSearchParams } from "next/navigation"
import { ArrowRight, Slash, Terminal, Warning } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { Pane, PaneFooter } from "@/components/panel"
import { Toolbar } from "@/components/page"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { XtermPane } from "@/components/xterm-pane"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
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
 * which release it belongs to, and the switch to another container — and the
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
  // The switch leaves the strip until the content column can hold it beside
  // the name: below this, with the sidebar open, it crushed the one thing
  // saying which container this is to nothing.
  const wide = useMediaQuery("(min-width: 1024px)")
  const { runtime, deployment } = project.detail
  const services = runtime?.status === "available" ? runtime.services : []
  const running = services.filter((service) => service.state === "running")
  const preferred = running.find((service) => service.liveRelease) ?? running[0]
  const [selected, setSelected] = useSessionState<string | undefined>(
    `deploy.${project.projectId}.console.service`,
    undefined,
    search.get("service"),
  )
  const service = running.find((item) => item.containerId === selected) ?? preferred
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

  const switcher = running.length > 1 && (
    <Select value={service.containerId} onValueChange={setSelected}>
      <SelectTrigger size="sm" className="w-full sm:w-56" aria-label="Console container">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {running.map((item) => {
          // The release's number, as Runtime's switch says it: "live" beside
          // every container of the live release told them apart by nothing.
          const number = releaseNumber(item.releaseId)
          return (
            <SelectItem
              key={item.containerId}
              value={item.containerId}
              hint={number !== undefined ? `#${number}` : undefined}
            >
              <ProductGlyph id={product(item.image)} />
              <span className="truncate">{item.service || item.name}</span>
            </SelectItem>
          )
        })}
      </SelectContent>
    </Select>
  )

  return (
    <div className="space-y-3">
      {switcher && !wide && <Toolbar>{switcher}</Toolbar>}
      <Pane className={CONSOLE_HEIGHT}>
        <XtermPane
          key={service.containerId}
          flush
          path={`/docker/containers/${service.containerId}/exec`}
          query={{ rows: 30, cols: 100 }}
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
                    : releaseNumber(service.releaseId) !== undefined
                      ? `Release #${releaseNumber(service.releaseId)}`
                      : "Other release"
                }
                className="mx-1.5 max-sm:hidden"
              />
              {switcher && wide && <span className="ml-auto shrink-0 pr-1">{switcher}</span>}
            </>
          }
        />
        <PaneFooter>
          <p className="text-hint text-muted-foreground">
            Commands run inside <span className="font-mono">{service.name}</span>, not on the host.
            Changes made here disappear when the next release starts from its image.
          </p>
        </PaneFooter>
      </Pane>
    </div>
  )
}
