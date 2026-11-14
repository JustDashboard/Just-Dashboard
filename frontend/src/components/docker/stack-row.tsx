"use client"

import Link from "next/link"
import { Code, FolderOpen, MoreHorizontal, Play, Terminal } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { DashboardMark } from "@/components/backups/marks"
import { ChoiceRow } from "@/components/flow"
import { ProductLogos, imageProducts } from "@/components/product-logo"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { bytes, percent, plural } from "@/lib/format"
import type { ComposeStack } from "@/lib/types"
import { cn } from "@/lib/utils"
import { crashed } from "@/components/docker/overview"
import { stackLabel, stackTone } from "@/components/docker/stack-state"
import { DASHBOARD_STACK, stackLane, type StackLine } from "@/components/docker/stack-readings"

/**
 * One compose stack as a card you open (`ChoiceRow`): what it is made of as
 * its services' products, its name in its lane's hue and the directory it was
 * found in, then its readings in columns that line up down the list — its
 * services as a strip of their states, its state and how much of it is up,
 * the processor and memory its containers take together, and the ports it
 * publishes — and at the end the one action worth having on a list (Deploy,
 * for a stack that is down) and a menu of the ways into it.
 *
 * One line and one height in every state. The Stacks page drew each stack as
 * a group row with its containers under it, which pushed the rest of the list
 * down by however many services the stack had; the containers are the stack's
 * own page, a press away. The overview draws the same card without the
 * figures, in half the width.
 */
export function StackChoiceRow({
  line,
  figures = true,
  deploying,
  onDeploy,
  matched,
}: {
  line: StackLine
  /** The processor, memory and port columns, and the row's verbs: the Stacks page's. */
  figures?: boolean
  /** The compose command running on it from this list, as its participle. */
  deploying?: string
  onDeploy?: (stack: string) => void
  /** The services a search matched, said in place of the directory. */
  matched?: string[]
}) {
  const { stack } = line
  const href = `/docker/stacks/${encodeURIComponent(stack.name)}`
  const words = stateWords(line)
  const state = deploying ? (
    <span className="inline-flex items-center gap-1.5 text-xs font-medium">
      <StatusDot tone="notice" />
      <TextShimmer>{`${deploying}…`}</TextShimmer>
    </span>
  ) : (
    <Status
      tone={stackTone(stack.state)}
      live={stack.state === "running"}
      label={stackLabel(stack.state)}
    />
  )
  return (
    <ChoiceRow
      href={href}
      verb={`Open ${stack.name}`}
      workspaceItem={{ id: `stack:${stack.name}`, name: stack.name }}
      busy={Boolean(deploying)}
      leading={
        // A fixed box, so every name starts on one edge whether the stack is
        // one product or four.
        <span className="flex w-9 items-center sm:w-[4.75rem]">
          <span className="sm:hidden">
            <ProductLogos
              ids={stackProducts(stack).slice(0, 1)}
              lead={stackLead(stack)}
              ring="ring-choice-surface"
            />
          </span>
          <span className="max-sm:hidden">
            <ProductLogos
              ids={stackProducts(stack)}
              lead={stackLead(stack)}
              ring="ring-choice-surface"
            />
          </span>
        </span>
      }
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span
            aria-hidden
            className="size-1.5 shrink-0 rounded-full"
            style={{ background: stackLane(stack.name) }}
          />
          <span className="truncate">{stack.name}</span>
          <StackTags stack={stack} />
          {/* A phone has no room for the columns: what the stack holds in
              memory ends the name's line there, and the state is under it. */}
          {figures && stack.running > 0 && line.memory > 0 && (
            <span className="numeric ml-auto shrink-0 pl-1 font-mono text-hint font-normal text-muted-foreground md:hidden">
              {bytes(line.memory)}
            </span>
          )}
        </span>
      }
      description={
        <>
          <span className="numeric md:hidden">
            <span className={TONE_TEXT[words.tone]}>
              {deploying ? `${deploying}…` : words.word}
            </span>
            {" · "}
            {words.short}
          </span>
          <span className="max-md:hidden">
            {matched && matched.length > 0 ? (
              <>matches {matched.join(", ")}</>
            ) : (
              <span className="font-mono" title={stack.workingDir}>
                {stack.workingDir || "no compose directory"}
              </span>
            )}
          </span>
        </>
      }
      trailing={
        figures ? (
          <>
            <span className="flex items-center gap-4 max-md:hidden">
              <span className="w-24 max-lg:hidden">
                <ServiceStrip stack={stack} />
              </span>
              <span className="w-40 min-w-0" data-stack-state>
                {state}
                <span className="numeric block truncate text-hint text-muted-foreground">
                  {words.detail}
                  {words.unhealthy > 0 && (
                    <span className="text-destructive"> · {words.unhealthy} unhealthy</span>
                  )}
                </span>
              </span>
              <span className="w-14 text-right" data-stack-cpu>
                <StackFigure line={line} kind="cpu" />
              </span>
              <span className="w-[4.5rem] text-right" data-stack-memory>
                <StackFigure line={line} kind="memory" />
              </span>
              <span className="numeric w-28 truncate text-hint text-muted-foreground max-xl:hidden">
                {publishedWords(line)}
              </span>
            </span>
            {/* A box of one width whether or not the stack can be deployed
                from here, so the columns before it line up down the list. */}
            <span
              className="flex justify-end md:w-[4.75rem]"
              aria-busy={deploying ? true : undefined}
            >
              <DeployButton line={line} deploying={deploying} onDeploy={onDeploy} />
            </span>
          </>
        ) : (
          // Half a page wide, on the overview: the state, how much of it is
          // up, and the strip under them in place of a second line of words.
          <span className="w-40 min-w-0 max-md:hidden" data-stack-state>
            <span className="flex items-center justify-between gap-2">
              {state}
              <span className="numeric text-hint text-muted-foreground">{words.short}</span>
            </span>
            <span className="mt-2 block">
              <ServiceStrip stack={stack} />
            </span>
          </span>
        )
      }
      actions={figures ? <StackMenu stack={stack} /> : undefined}
    />
  )
}

const TONE_TEXT: Record<string, string> = {
  running: "text-success",
  warning: "text-warning",
  danger: "text-destructive",
  stopped: "text-muted-foreground",
  unknown: "text-muted-foreground",
  notice: "text-muted-foreground",
}

/**
 * The declared services that are up. `running` counts containers, so a stack
 * still running a service its file dropped read "3 of 2 services up".
 */
function declaredUp(stack: ComposeStack) {
  return stack.services.filter((s) => s.state === "running" && !stack.orphans.includes(s.name))
    .length
}

/** A stack's state in the row's words: the word, how much of it is up, and what is failing a check. */
export function stateWords(line: StackLine) {
  const { stack } = line
  return {
    word: stackLabel(stack.state),
    tone: stackTone(stack.state),
    detail: !stack.deployed
      ? `${plural(stack.total, "service")} defined`
      : `${declaredUp(stack)} of ${plural(stack.total, "service")} up`,
    short: !stack.deployed ? `${stack.total} defined` : `${declaredUp(stack)}/${stack.total} up`,
    unhealthy: line.lines.filter((s) => s.health === "unhealthy").length,
  }
}

function publishedWords(line: StackLine) {
  const ports = line.lines.reduce(
    (n, s) => n + (s.state === "running" ? s.service.ports.filter((p) => p.publicPort).length : 0),
    0,
  )
  return ports === 0 ? "" : `${plural(ports, "port")} published`
}

export function stackProducts(stack: ComposeStack) {
  const images = stack.services.map((s) => s.image).filter(Boolean)
  return images.length > 0 ? imageProducts(images) : ["docker-compose"]
}

/** The dashboard's own stack is drawn as the dashboard: its images alone read as a Caddy. */
function stackLead(stack: ComposeStack) {
  return stack.name === DASHBOARD_STACK ? <DashboardMark /> : undefined
}

/** What the stack is beyond its state: a leftover it runs, or no file to act from. */
function StackTags({ stack }: { stack: ComposeStack }) {
  return (
    <>
      {stack.orphans.length > 0 && (
        <Tag
          tone="warning"
          title={`${stack.orphans.join(", ")} ${stack.orphans.length === 1 ? "is" : "are"} running under this project name and no longer in the compose file. A deploy removes ${stack.orphans.length === 1 ? "it" : "them"}.`}
        >
          {plural(stack.orphans.length, "orphan")}
        </Tag>
      )}
      {!stack.managed && (
        <Tag title="No compose file reachable from this dashboard, so this stack is read-only here.">
          read-only
        </Tag>
      )}
    </>
  )
}

/**
 * A stack's sum, in its column: nothing for a stack with nothing running, a
 * dash while its containers have no reading yet.
 */
function StackFigure({ line, kind }: { line: StackLine; kind: "cpu" | "memory" }) {
  if (line.stack.running === 0) return null
  if (kind === "cpu") {
    if (!line.measured) return <span className="font-mono text-muted-foreground/60">—</span>
    return (
      <span
        className="numeric font-mono text-body"
        title={`${percent(line.cpu)} of one core, summed over its containers`}
      >
        {percent(line.cpu)}
      </span>
    )
  }
  if (line.memory === 0) return <span className="font-mono text-muted-foreground/60">—</span>
  return (
    <span className="numeric font-mono text-body" title="Memory, summed over its containers">
      {bytes(line.memory)}
    </span>
  )
}

/**
 * The one action worth having on the list: an application that is down and
 * should not be. Everything else needs the stack's page, where the output is
 * and a deploy can be previewed.
 */
function DeployButton({
  line,
  deploying,
  onDeploy,
}: {
  line: StackLine
  deploying?: string
  onDeploy?: (stack: string) => void
}) {
  const { can } = useAuth()
  const { stack } = line
  if (
    !onDeploy ||
    !can("system.admin") ||
    !can("service.control") ||
    !stack.managed ||
    stack.state === "running"
  ) {
    return null
  }
  return (
    <Button
      size="xs"
      variant={stack.deployed ? "outline" : "default"}
      onClick={() => onDeploy(stack.name)}
      pending={!!deploying}
    >
      <Play className="size-3" />
      {/* The word is the button's name at every width, and drawn from `sm`. */}
      <span className="max-sm:sr-only">Deploy</span>
    </Button>
  )
}

/** The row's overflow: the things that are navigation rather than an action. */
function StackMenu({ stack }: { stack: ComposeStack }) {
  const { can } = useAuth()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={`More actions for ${stack.name}`}
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-44">
        <DropdownMenuItem asChild>
          <Link href={`/docker/stacks/${encodeURIComponent(stack.name)}`}>
            <Code className="size-3.5" />
            View
          </Link>
        </DropdownMenuItem>
        {stack.workingDir && (
          <DropdownMenuItem asChild>
            <Link href={`/files?path=${encodeURIComponent(stack.workingDir)}`}>
              <FolderOpen className="size-3.5" />
              Files
            </Link>
          </DropdownMenuItem>
        )}
        {stack.workingDir && can("terminal") && (
          <DropdownMenuItem asChild>
            <Link href={`/terminal?cwd=${encodeURIComponent(stack.workingDir)}`}>
              <Terminal className="size-3.5" />
              Open shell
            </Link>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * Each service the project declares as one segment, in its state's tone: up,
 * failing, stopped, or not created. A project that was never deployed is a
 * row of empty track, which is what it is.
 */
export function ServiceStrip({ stack }: { stack: ComposeStack }) {
  const byName = new Map(stack.services.map((s) => [s.name, s]))
  const names = [...new Set([...stack.declared, ...stack.services.map((s) => s.name)])]
  if (names.length === 0) return null
  const segments = names.map((name) => {
    const service = byName.get(name)
    if (!service || service.missing) return { name, tone: "bg-meter-track", word: "not created" }
    if (service.state === "running") {
      return service.health === "unhealthy"
        ? { name, tone: "bg-destructive", word: "unhealthy" }
        : service.health === "starting"
          ? { name, tone: "bg-warning", word: "starting" }
          : { name, tone: "bg-success", word: "running" }
    }
    if (service.state === "restarting" || service.state === "dead") {
      return { name, tone: "bg-destructive", word: service.state }
    }
    return crashed(service)
      ? { name, tone: "bg-destructive", word: service.status }
      : { name, tone: "bg-muted-foreground/40", word: service.status || service.state }
  })
  return (
    <span
      role="img"
      aria-label={segments.map((s) => `${s.name} ${s.word}`).join(", ")}
      className="flex h-1 w-full gap-0.5"
    >
      {segments.map((s) => (
        <span
          key={s.name}
          title={`${s.name}: ${s.word}`}
          className={cn("h-full flex-1 rounded-full transition-colors", s.tone)}
        />
      ))}
    </span>
  )
}
