"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowRight,
  ArrowUpRight,
  Box,
  Download,
  Layers,
  Logs,
  Play,
  RefreshClockwise,
} from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job, SystemdUnitDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { MissingThing, type MissingGain } from "@/components/missing-thing"
import { usePackageKnown } from "@/components/network/install"
import { ErrorState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { DOCKER_UNIT, dockerUnreachable, type DockerPing } from "@/components/docker/unreachable"

/** What the section draws once the daemon answers, said before it does. */
const GAINS: MissingGain[] = [
  {
    icon: Box,
    title: "Containers",
    text: "What runs, what it uses and what it is wired to, with its logs and a shell.",
  },
  {
    icon: Layers,
    title: "Stacks",
    text: "Compose projects as one thing: deploy, edit, bring up and down.",
  },
  {
    icon: Logs,
    title: "Images, volumes and networks",
    text: "What is on disk, what holds it there and what can be cleaned up.",
  },
]

/** Where Docker's own instructions are, for a server whose package manager does not carry it. */
const DOCKER_INSTALL_DOCS = "https://docs.docker.com/engine/install/"

/**
 * What every Docker page shows while the daemon cannot be asked: which of
 * the faults it is, where that one is fixed, and the step that fixes it
 * (Start where starting the unit does, Install where there is no Docker at
 * all). The client's own sentence is one press away, never the description.
 *
 * It is the product's one screen for a missing thing (`MissingThing`), so a
 * stopped Docker reads the way a server without nginx does.
 *
 * systemd is asked about the daemon's unit before anything is drawn, so the
 * screen does not open on "not answering" and change its title, and grow a
 * button, a moment later.
 */
export function DockerUnreachableState({
  ping,
  onCheck,
  size = "page",
}: {
  ping: DockerPing
  onCheck: () => void
  /** `block` inside a page that has other things on it. */
  size?: "page" | "block"
}) {
  const { can } = useAuth()
  const unit = usePoll(
    (signal) =>
      get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(DOCKER_UNIT)}`, undefined, signal),
    15_000,
  )
  const [starting, setStarting] = useState(false)
  const console_ = useJobConsole({
    onSuccess: () => {
      notify.success("Docker is installed")
      unit.refresh()
      onCheck()
    },
  })

  if (unit.loading && !unit.data && !unit.error) return <LoadingPanel />

  // A server with no systemd, or an account that may not read units, leaves
  // the unit unread: the screen then says only what the client's error does.
  const state = dockerUnreachable(ping, unit.error ? undefined : unit.data?.unit)
  const start = async () => {
    setStarting(true)
    try {
      await post(`/systemd/${encodeURIComponent(DOCKER_UNIT)}/start`)
      unit.refresh()
      onCheck()
    } catch (err) {
      notify.error("Could not start Docker", err)
    } finally {
      setStarting(false)
    }
  }

  const page = size === "page"
  const startable = state.startable && can("service.control")
  const installable = state.fault === "absent" && can("system.admin")
  // The poll asks again on its own half-minute timer; the button is for the
  // moment after the fix, when thirty seconds is a long time to look at a
  // page that says no.
  const check = (lead: boolean) => (
    <Button
      size={page ? (lead ? "lg" : "default") : "sm"}
      variant={lead ? "default" : "outline"}
      onClick={onCheck}
    >
      <RefreshClockwise />
      Check again
    </Button>
  )

  return (
    <MissingThing
      thing="docker"
      icon={Box}
      title={state.title}
      raw={state.raw}
      reason={`docker-${state.fault}`}
      size={size}
      gains={page ? GAINS : undefined}
      gainsLabel="What these pages show once Docker answers"
      step={
        startable ? (
          <Button
            size={page ? "lg" : "sm"}
            onClick={() => void start()}
            pending={starting}
            disabled={starting}
          >
            <Play />
            Start Docker
          </Button>
        ) : installable ? (
          <InstallDocker
            size={size}
            running={console_.running}
            onStart={(job) => console_.attach(job)}
          />
        ) : (
          check(true)
        )
      }
      more={
        <>
          {(startable || installable) && check(false)}
          {(state.fault === "stopped" || state.fault === "elsewhere") && (
            <Link
              href={`/processes/services/${encodeURIComponent(DOCKER_UNIT)}`}
              className="inline-flex items-center gap-1 rounded-sm text-body font-medium focus-ring hover:underline"
            >
              Service details
              <ArrowRight className="size-3" />
            </Link>
          )}
        </>
      }
      after={
        <JobConsole
          job={console_.job}
          lines={console_.lines}
          onDismiss={console_.dismiss}
          onCancel={console_.cancel}
        />
      }
    >
      {state.description}
      {state.startable && !startable && " An account that may control services can start it."}
      {state.fault === "absent" && !installable && " An administrator can install it."}
    </MissingThing>
  )
}

/** The names Docker's engine goes by: Debian's and Ubuntu's, then everybody else's. */
const DOCKER_PACKAGES = ["docker.io", "docker"] as const

/**
 * Install Docker, where this server's package manager carries it: through
 * the Packages page's own install route, as nginx and the network tools go
 * in. Where it carries neither name the step is Docker's own instructions,
 * because a button that ends in "Unable to locate package" is worse than a
 * link.
 */
function InstallDocker({
  size,
  running,
  onStart,
}: {
  size: "page" | "block"
  running: boolean
  onStart: (job: Job) => void
}) {
  const debian = usePackageKnown(DOCKER_PACKAGES[0])
  const other = usePackageKnown(DOCKER_PACKAGES[1])
  const [starting, setStarting] = useState(false)
  const asking = debian === "asking" || (debian !== "known" && other === "asking")
  const pkg = debian === "known" ? DOCKER_PACKAGES[0] : other === "known" ? DOCKER_PACKAGES[1] : ""
  const button = size === "page" ? "lg" : "sm"

  if (!asking && !pkg) {
    return (
      <Button size={button} asChild>
        <a href={DOCKER_INSTALL_DOCS} target="_blank" rel="noreferrer">
          How to install Docker
          <ArrowUpRight />
        </a>
      </Button>
    )
  }

  const install = async () => {
    setStarting(true)
    try {
      onStart(await post<Job>("/packages/install", { packages: [pkg] }))
    } catch (err) {
      notify.error("Could not start installing Docker", err)
    } finally {
      setStarting(false)
    }
  }
  const busy = starting || running
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2">
      <Button
        size={button}
        onClick={() => void install()}
        pending={busy || asking}
        disabled={busy || asking}
      >
        <Download />
        {running ? "Installing…" : "Install Docker"}
      </Button>
      {pkg && (
        <code className="min-w-0 font-mono text-hint break-all text-muted-foreground">{pkg}</code>
      )}
    </span>
  )
}

/**
 * The same screen for a page outside the Docker section that cannot do its
 * work without the daemon (Remote Desktop, a database's container): it asks
 * the daemon itself, so the page only says where it goes. `onAnswer` is
 * called when Docker answers again, for the page to read again.
 */
export function DockerMissing({
  size = "block",
  onAnswer,
}: {
  size?: "page" | "block"
  onAnswer?: () => void
}) {
  const ping = usePoll((signal) => get<DockerPing>("/docker/ping", undefined, signal), 30_000)
  const available = ping.data?.available === true
  // The latest `onAnswer` is kept beside the effect, so a parent drawing
  // again does not tell it twice for one recovery.
  const answer = useRef(onAnswer)
  useEffect(() => {
    answer.current = onAnswer
  })
  useEffect(() => {
    if (available) answer.current?.()
  }, [available])
  if (ping.loading && !ping.data) return <LoadingPanel plain />
  if (ping.error && !ping.data) return <ErrorState error={ping.error} onRetry={ping.refresh} />
  if (available) return null
  return (
    <DockerUnreachableState
      ping={ping.data ?? { available: false }}
      onCheck={ping.refresh}
      size={size}
    />
  )
}
