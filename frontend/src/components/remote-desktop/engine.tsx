"use client"

import { useState } from "react"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { ProductLogos } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Download, Play, Warning } from "@/components/icons"
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
 */
export function EngineNotice({ engine, onReady }: { engine: RdEngine; onReady: () => void }) {
  const { console_, install, busy } = useEngineInstall(onReady)
  const fixable = engine.state === "missing" || engine.state === "stopped"
  const title =
    engine.state === "stopped"
      ? "The Remote Desktop engine is stopped"
      : engine.state === "missing"
        ? "The Remote Desktop engine is not installed"
        : engine.state === "foreign"
          ? "Another container holds the engine's name"
          : "Docker is not reachable"
  return (
    <div className="flex flex-col gap-3">
      <Notice tone="warning" icon={Warning} title={title}>
        <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
          <p className="min-w-0 text-body text-muted-foreground">
            {fixable
              ? "No session can open until guacd runs again. Your computers and their sign-ins are kept."
              : engine.error}
          </p>
          {fixable && (
            <Button size="sm" pending={busy} onClick={() => void install()}>
              <Play className="size-3.5" />
              {engine.state === "stopped" ? "Start the engine" : "Install the engine"}
            </Button>
          )}
        </div>
      </Notice>
      {console_.job && <JobConsole job={console_.job} lines={console_.lines} className="h-56" />}
    </div>
  )
}
