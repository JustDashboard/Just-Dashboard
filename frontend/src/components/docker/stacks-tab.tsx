"use client"

import { useEffect, useMemo, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import Link from "next/link"
import {
  Code,
  FolderOpen,
  FolderPlus,
  Layers,
  MoreHorizontal,
  Play,
  Terminal,
  Warning,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { ComposeService, ComposeStack } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { SearchInput } from "@/components/page"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { ProductLogos, imageProducts } from "@/components/product-logo"
import { ChipCount, FilterChip } from "@/components/tabs"
import { cn } from "@/lib/utils"
import { PortLink } from "@/components/docker/shared"
import { MenuItemBody } from "@/components/docker/container-actions"
import { StackSummary, stackTone } from "@/components/docker/stack-state"
import { ExplainIcon, Field, Term } from "@/components/docker/explain"
import { Modal } from "@/components/modal"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

/**
 * The stack list, which is a way in rather than the whole feature.
 *
 * Everything a stack can do moved into its own panel, because a card with six
 * buttons on it is a card nobody reads and there was nowhere to put the compose
 * file, the merged logs, or the output of the command you just ran. What is
 * left here is the question the list should answer at a glance: which
 * applications exist, are they up, and where do I reach them.
 *
 * It is one panel of rows rather than a grid of bordered cards. A stack is a
 * row in a list — name, state, the services under it — and a phone is where
 * somebody checks whether the thing they just deployed is alive. The search box
 * and the state chips are the same pair the containers page opens with, because
 * "which of these is down" is the same question asked of the same server.
 */

type StateFilter = "all" | "running" | "stopped" | "attention"

const FILTER_LABEL: Record<StateFilter, string> = {
  all: "All",
  running: "Running",
  stopped: "Not running",
  attention: "Needs attention",
}

/** A stack the dashboard has something to act on: a bad service or a leftover. */
function needsAttention(stack: ComposeStack) {
  return (
    stack.orphans.length > 0 ||
    stack.services.some((s) => s.health === "unhealthy") ||
    stack.state === "degraded" ||
    stack.state === "partial"
  )
}

export function StacksTab({
  creating: externalCreating,
  onCreatingChange,
}: {
  creating?: boolean
  onCreatingChange?: (open: boolean) => void
}) {
  const { can } = useAuth()
  const router = useRouter()

  /*
    `?stack=` opened a sheet on this page until 2026-09-21, and a container
    managed by compose linked to exactly that address. Those links outlive the
    panel, so they land on the stack.
  */
  const legacy = useSearchParams().get("stack")
  useEffect(() => {
    if (legacy) router.replace(`/docker/stacks/${encodeURIComponent(legacy)}`)
  }, [legacy, router])
  const [internalCreating, setInternalCreating] = useState(false)
  const creating = externalCreating ?? internalCreating
  const setCreating = onCreatingChange ?? setInternalCreating
  const [filter, setFilter] = useSessionState("docker.stacks.query", "")
  const [state, setState] = useSessionState<StateFilter>("docker.stacks.state", "all")

  const { data, error, loading, refresh } = usePoll(
    (signal) => get<ComposeStack[]>("/docker/stacks/", undefined, signal),
    15000,
  )

  const stacks = useMemo(() => data ?? [], [data])

  const counts = useMemo(
    () => ({
      all: stacks.length,
      running: stacks.filter((s) => s.running > 0).length,
      stopped: stacks.filter((s) => s.running === 0).length,
      attention: stacks.filter(needsAttention).length,
    }),
    [stacks],
  )

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    return stacks.filter((stack) => {
      if (state === "running" && stack.running === 0) return false
      if (state === "stopped" && stack.running > 0) return false
      if (state === "attention" && !needsAttention(stack)) return false
      if (!needle) return true
      return (
        stack.name.toLowerCase().includes(needle) ||
        stack.services.some(
          (s) => s.name.toLowerCase().includes(needle) || s.image.toLowerCase().includes(needle),
        )
      )
    })
  }, [stacks, filter, state])

  const newStack = can("system.admin") && can("file.write") && (
    <Button size="sm" onClick={() => setCreating(true)}>
      <FolderPlus className="size-4" />
      Create stack
    </Button>
  )

  const filtered = filter.trim().length > 0 || state !== "all"
  const hasRows = !loading && !error && visible.length > 0

  return (
    <div className="space-y-4">
      {/* Plain: the list is the page. */}
      <Panel plain>
        <PanelHeader
          title={
            <span className="inline-flex items-center gap-1.5">
              Stacks
              <ExplainIcon name="stack" />
            </span>
          }
        />

        {stacks.length > 0 && (
          <PanelToolbar>
            <SearchInput
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter by name, service or image"
            />
            <div className="flex min-w-0 flex-wrap gap-1">
              {(["all", "running", "stopped", "attention"] as const).map((key) =>
                key === "all" || counts[key] > 0 ? (
                  <FilterChip
                    key={key}
                    selected={state === key}
                    onClick={() => setState(key)}
                    className={
                      key === "attention" && counts.attention > 0
                        ? "text-warning hover:text-warning"
                        : undefined
                    }
                  >
                    {FILTER_LABEL[key]}
                    <ChipCount>{counts[key]}</ChipCount>
                  </FilterChip>
                ) : null,
              )}
            </div>
          </PanelToolbar>
        )}

        <PanelBody flush={hasRows}>
          {loading && !data ? (
            <LoadingRows rows={3} />
          ) : error ? (
            <ErrorState error={error} />
          ) : stacks.length === 0 ? (
            <EmptyState
              icon={Layers}
              title="No compose stacks found"
              description={
                <>
                  A <Term name="stack">stack</Term> is a directory with a compose file in it. The
                  dashboard finds them by the labels compose puts on the containers it creates, and
                  by looking under the configured compose directories.
                </>
              }
              action={newStack}
            />
          ) : visible.length === 0 ? (
            <EmptyState
              icon={Warning}
              title="Nothing matches those filters"
              description="Clear the filter, or look under a different state."
              action={
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    setFilter("")
                    setState("all")
                  }}
                >
                  Clear filters
                </Button>
              }
            />
          ) : (
            <ChoiceList aria-label="Stacks" className="animate-rise">
              {visible.map((stack) => (
                <StackRow
                  key={stack.name}
                  stack={stack}
                  onOpen={() => router.push(`/docker/stacks/${encodeURIComponent(stack.name)}`)}
                  onChanged={refresh}
                />
              ))}
            </ChoiceList>
          )}
          {/* The filters narrowed everything away to nothing rather than the
              server having nothing to show; the count is the difference. */}
          {filtered && !loading && !error && visible.length > 0 && (
            <p className="border-t border-hairline py-2 text-hint text-muted-foreground">
              {visible.length} of {stacks.length} stacks.
            </p>
          )}
        </PanelBody>
      </Panel>

      <NewStackDialog
        open={creating && can("system.admin") && can("file.write")}
        onOpenChange={setCreating}
        onCreated={(name) => {
          refresh()
          router.push(`/docker/stacks/${encodeURIComponent(name)}`)
        }}
      />
    </div>
  )
}

/**
 * One stack, as a card that opens it.
 *
 * The mark is what the stack is made of: the products of its services' images,
 * overlapping, so a stack of Postgres, Redis and an API reads as those three
 * before its name does. A stack with no image anybody makes a logo for is drawn
 * as Compose, which is at least true of every one of them.
 *
 * The service list under the name is the load-bearing part: a stack is an
 * application made of several containers, and "which of its parts is not
 * running" is what the card exists to answer. Each service keeps its dot, its
 * name, its ports and its health, and the ports are still links, so a phone
 * can reach the thing without opening anything.
 */
function StackRow({
  stack,
  onOpen,
  onChanged,
}: {
  stack: ComposeStack
  onOpen: () => void
  onChanged: () => void
}) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const unhealthy = stack.services.filter((s) => s.health === "unhealthy").length
  const canDeploy =
    can("system.admin") && can("service.control") && stack.managed && stack.state !== "running"
  const images = stack.services.map((service) => service.image).filter(Boolean)
  const products = images.length > 0 ? imageProducts(images) : ["docker-compose"]

  // The one action worth having on the card: an application that is down and
  // should not be. Everything else needs the stack's page, where the output
  // is — and where a deploy can be previewed before it runs.
  const deploy = async () => {
    setBusy(true)
    try {
      await post(`/docker/stacks/${encodeURIComponent(stack.name)}/up`)
      notify.success(`${stack.name} deployed`)
      onChanged()
    } catch (err) {
      notify.error(`Could not deploy ${stack.name}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <ChoiceRow
      verb={stack.name}
      onSelect={onOpen}
      className={cn(busy && "opacity-70")}
      leading={<ProductLogos ids={products} />}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <StatusDot tone={stackTone(stack.state)} live={stack.state === "running"} />
          <span className="truncate">{stack.name}</span>
          {unhealthy > 0 && <Status verdict="critical" label={`${unhealthy} unhealthy`} />}
        </span>
      }
      description={<StackSummary stack={stack} />}
      actions={
        <span className="flex shrink-0 items-center gap-1" aria-busy={busy ? true : undefined}>
          {canDeploy && (
            <Button size="sm" onClick={deploy} pending={busy}>
              <Play className="size-3.5" />
              Deploy
            </Button>
          )}
          <StackRowMenu stack={stack} onOpen={onOpen} />
        </span>
      }
    >
      {(stack.services.length > 0 || stack.orphans.length > 0 || !stack.managed) && (
        <div className="min-w-0 space-y-1.5">
          {stack.services.length > 0 && (
            <ul className="flex flex-wrap items-center gap-x-3 gap-y-1">
              {stack.services.map((service) => (
                <ServiceMarker key={service.container || service.name} service={service} />
              ))}
            </ul>
          )}

          {stack.orphans.length > 0 && (
            <p className="flex items-start gap-1.5 text-hint text-warning">
              <Warning className="mt-0.5 size-3 shrink-0" />
              <span>
                {stack.orphans.join(", ")} {stack.orphans.length === 1 ? "is" : "are"} running under
                this project name and no longer in the compose file. A deploy removes{" "}
                {stack.orphans.length === 1 ? "it" : "them"}.
              </span>
            </p>
          )}

          {!stack.managed && (
            <p className="text-hint text-muted-foreground">
              No compose file reachable from this dashboard, so this stack is read-only here.
            </p>
          )}
        </div>
      )}
    </ChoiceRow>
  )
}

/** One service of a stack: its dot, its name, its health and where it answers. */
function ServiceMarker({ service }: { service: ComposeService }) {
  const ports = service.ports.filter((p) => p.publicPort)
  return (
    // Everything on one centred baseline: the port tags and the "defined" mark
    // are taller than the text beside them, and without a shared line-height
    // the row reads as bumpy.
    <li className="inline-flex min-w-0 items-center gap-1.5 text-hint leading-5">
      <StatusDot state={service.missing ? "unknown" : service.state} />
      <span className={cn("truncate leading-5", service.missing && "text-muted-foreground")}>
        {service.name}
      </span>
      {service.health && service.health !== "healthy" && (
        <span
          className={cn(
            "shrink-0 leading-5 capitalize",
            service.health === "unhealthy" ? "text-destructive" : "text-warning",
          )}
        >
          {service.health}
        </span>
      )}
      {service.missing ? (
        <Tag>defined</Tag>
      ) : (
        <span className="inline-flex items-center gap-1">
          {ports.slice(0, 2).map((p, i) => (
            <PortLink key={i} ip={p.ip} port={p.publicPort ?? 0} target={p.privatePort} />
          ))}
        </span>
      )}
    </li>
  )
}

/** The row's overflow: the things that are navigation rather than an action. */
function StackRowMenu({ stack, onOpen }: { stack: ComposeStack; onOpen: () => void }) {
  const { can } = useAuth()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="More actions"
          onClick={(event) => event.stopPropagation()}
          className="[&_svg:not([class*='size-'])]:size-3.5"
        >
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-68">
        <DropdownMenuItem
          className="items-start gap-2.5 py-1.5"
          onSelect={(event) => {
            event.preventDefault()
            onOpen()
          }}
        >
          <Code className="mt-0.5 size-3.5 shrink-0" />
          <MenuItemBody
            label="View"
            detail="Services, the compose file, deploy history and the merged log feed."
          />
        </DropdownMenuItem>
        {stack.workingDir && (
          <DropdownMenuItem asChild className="items-start gap-2.5 py-1.5">
            <Link href={`/files?path=${encodeURIComponent(stack.workingDir)}`}>
              <FolderOpen className="mt-0.5 size-3.5 shrink-0" />
              <MenuItemBody label="Files" detail="The stack's directory in the file manager." />
            </Link>
          </DropdownMenuItem>
        )}
        {stack.workingDir && can("terminal") && (
          <DropdownMenuItem asChild className="items-start gap-2.5 py-1.5">
            <Link href={`/terminal?cwd=${encodeURIComponent(stack.workingDir)}`}>
              <Terminal className="mt-0.5 size-3.5 shrink-0" />
              <MenuItemBody
                label="Open shell"
                detail="A terminal opened in the stack's directory."
              />
            </Link>
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * A new stack is a directory and a compose file — nothing more, which is the
 * point. It is created with a working starter file rather than an empty one,
 * because the format is exactly the part somebody new does not know, and a
 * blank editor is the least useful thing to hand them.
 */
function NewStackDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (name: string) => void
}) {
  const [name, setName] = useState("")
  const [dir, setDir] = useState("")
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      const res = await post<{ name: string; dir: string }>("/docker/stacks/", {
        name,
        dir: dir.trim() || undefined,
      })
      notify.success(`${res.name} created`, { description: res.dir })
      onOpenChange(false)
      onCreated(res.name)
      setName("")
      setDir("")
    } catch (err) {
      notify.error("Could not create the stack", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      title="Create stack"
      description="Creates a directory with a starter compose file in it. Nothing runs until you bring it
            up."
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={create} disabled={busy || !name.trim()} pending={busy}>
            Create
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field
          label="Name"
          htmlFor="stack-name"
          required
          hint="Lower-case letters, digits, dashes and underscores. Compose uses it to name the containers and the network it creates."
        >
          <Input
            id="stack-name"
            value={name}
            spellCheck={false}
            placeholder="my-app"
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field
          label="Directory"
          htmlFor="stack-dir"
          hint="It has to be under one of the server's configured compose directories, or the dashboard will not find the stack again once it is stopped."
        >
          <Input
            id="stack-dir"
            value={dir}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="leave empty for the default compose directory"
            onChange={(e) => setDir(e.target.value)}
          />
        </Field>
      </div>
    </Modal>
  )
}
