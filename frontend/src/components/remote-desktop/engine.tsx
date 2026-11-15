"use client"

import Link from "next/link"
import { useState } from "react"
import { DockerMissing } from "@/components/docker/unreachable-state"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { MissingThing } from "@/components/missing-thing"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { ArrowRight, Download, Play, RefreshClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"
import type { RdEngine } from "@/lib/remote-desktop"

/**
 * Starting the engine: one press that pulls guacd, makes its container and
 * waits for it to answer, with the install's own output under it. The same
 * command starts an engine that was stopped, since installing over a stopped
 * one is starting it.
 */
export function useEngineInstall(onReady: () => void) {
  const console_ = useJobConsole({ onSuccess: onReady })
  const [starting, setStarting] = useState(false)
  const install = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/remote-desktop/engine"))
    } catch (err) {
      notify.error("Could not start the install", err)
    } finally {
      setStarting(false)
    }
  }
  return { console_, install, busy: starting || console_.running }
}

/** The first screen of a server that has never had Remote Desktop. */
export function EngineSetup({ onReady }: { onReady: () => void }) {
  const { console_, install, busy } = useEngineInstall(onReady)
  return (
    <div className="flex flex-col gap-4">
      <EmptyState
        mark={<ProductLogos ids={["windows", "apple", "linux"]} size="md" />}
        title="Control your computers from here"
        description="Remote Desktop installs guacd, Apache Guacamole's remote desktop engine, as a container on this server. It listens on this server only, and reaches your computers over Tailscale, so nothing is opened to the internet."
        action={
          <Button pending={busy} onClick={() => void install()}>
            <Download className="size-4" />
            Install Remote Desktop
          </Button>
        }
      />
      {console_.job && <JobConsole job={console_.job} lines={console_.lines} className="h-64" />}
    </div>
  )
}

/**
 * The engine when it is not running, said above everything it stops: the
 * computers are still listed, and none of them can be opened until it is.
 *
 * Drawn on the product's one screen for a missing thing, as a block of the
 * page: guacd's own mark, what is wrong in plain words, and the step that
 * fixes it. Docker not answering is Docker's fault and not the engine's, so
 * that case is the Docker screen itself, which says which fault it is and
 * starts or installs the daemon. It used to be a warning box whose body was
 * the client's own question, "Is the docker daemon running?".
 */
export function EngineNotice({ engine, onReady }: { engine: RdEngine; onReady: () => void }) {
  if (engine.state === "unavailable") return <EngineUnread engine={engine} onReady={onReady} />
  if (engine.state === "foreign")
    return (
      <MissingThing
        thing="guacamole"
        size="block"
        reason="engine-foreign"
        title="Another container has the engine's name"
        raw={engine.error}
        step={
          <Button size="sm" asChild>
            <Link href="/docker/containers">
              Open Containers
              <ArrowRight />
            </Link>
          </Button>
        }
        more={<CheckAgain onClick={onReady} />}
      >
        The dashboard did not make it, so it is left alone. Rename or remove it, and Remote Desktop
        can install its own. Your computers and their sign-ins are kept.
      </MissingThing>
    )
  return <EngineStopped engine={engine} onReady={onReady} />
}

function CheckAgain({ onClick }: { onClick: () => void }) {
  return (
    <Button size="sm" variant="outline" onClick={onClick}>
      <RefreshClockwise />
      Check again
    </Button>
  )
}

/** Stopped, or removed with its computers kept: one press starts it, with the job under it. */
function EngineStopped({ engine, onReady }: { engine: RdEngine; onReady: () => void }) {
  const { console_, install, busy } = useEngineInstall(onReady)
  const stopped = engine.state === "stopped"
  return (
    <MissingThing
      thing="guacamole"
      size="block"
      reason={stopped ? "engine-stopped" : "engine-missing"}
      title={
        stopped
          ? "The Remote Desktop engine is stopped"
          : "The Remote Desktop engine is not installed"
      }
      step={
        <Button size="sm" pending={busy} onClick={() => void install()}>
          {stopped ? <Play /> : <Download />}
          {stopped ? "Start the engine" : "Install the engine"}
        </Button>
      }
      after={
        console_.job && <JobConsole job={console_.job} lines={console_.lines} className="h-56" />
      }
    >
      No session can open until guacd runs again. Your computers and their sign-ins are kept.
    </MissingThing>
  )
}

/**
 * The engine could not be read from Docker. Almost always that is Docker
 * itself, so Docker is asked: while it does not answer, its own screen is
 * drawn. Once it does and the engine is still unread, the fault is the
 * container's, and what Docker said about it is kept behind the fold.
 */
function EngineUnread({ engine, onReady }: { engine: RdEngine; onReady: () => void }) {
  const [answers, setAnswers] = useState(false)
  if (!answers)
    return (
      <DockerMissing
        size="block"
        onAnswer={() => {
          setAnswers(true)
          onReady()
        }}
      />
    )
  return (
    <MissingThing
      thing="guacamole"
      size="block"
      reason="engine-unread"
      title="The Remote Desktop engine could not be read"
      raw={engine.error}
      step={<CheckAgain onClick={onReady} />}
    >
      Docker answers, and its word on the engine&apos;s container did not come back. No session can
      open until it does. Your computers and their sign-ins are kept.
    </MissingThing>
  )
}
