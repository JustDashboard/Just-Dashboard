"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { External, GitHubMark } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GitHubConnection, GitHubDeviceStart, GitHubDeviceState } from "@/lib/github-types"
import { Modal } from "@/components/modal"
import { CopyButton } from "@/components/copy"
import { Notice } from "@/components/state"
import { Spinner } from "@/components/loading"
import { Button } from "@/components/ui/button"

type Stage =
  | { kind: "starting" }
  | { kind: "code"; start: GitHubDeviceStart }
  | { kind: "failed"; message: string; disabled?: boolean }

/**
 * Signing in with a code: GitHub's device flow, for a dashboard reached at
 * an address GitHub cannot send a browser back to (an IP, a tailnet name).
 * The server holds the device code and polls GitHub; the page holds only an
 * opaque flow id and asks the server how it stands at GitHub's `interval`.
 * The code is large and copyable, the countdown says when it expires, and an
 * App without "Enable Device Flow" ticked says where to tick it.
 */
export function DeviceSignIn(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The App's settings on GitHub, where device flow is switched on. */
  settingsUrl?: string
  onConnected: (connection: GitHubConnection) => void
}) {
  // A fresh flow per opening: a code from an earlier attempt is never shown again.
  if (!props.open) return null
  return <DeviceBody {...props} />
}

function DeviceBody({
  open,
  onOpenChange,
  settingsUrl,
  onConnected,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  settingsUrl?: string
  onConnected: (connection: GitHubConnection) => void
}) {
  const [stage, setStage] = useState<Stage>({ kind: "starting" })
  const [remaining, setRemaining] = useState(0)
  const doneRef = useRef(onConnected)
  useEffect(() => {
    doneRef.current = onConnected
  })

  const start = useCallback(async () => {
    setStage({ kind: "starting" })
    try {
      const res = await post<GitHubDeviceStart>("/github/connections/device")
      setStage({ kind: "code", start: res })
      setRemaining(res.expiresIn)
    } catch (error) {
      setStage({
        kind: "failed",
        message: errorMessage(error),
        disabled: error instanceof ApiError && error.code === "github_device_flow_disabled",
      })
    }
  }, [])

  // Started once per opening; the effect's second run under strict mode
  // finds the flow already asked for.
  const asked = useRef(false)
  useEffect(() => {
    if (asked.current) return
    asked.current = true
    void start()
  }, [start])

  // A chain of timeouts rather than an interval: the next ask waits for the
  // answer to the last, so a slow server never doubles GitHub's pace.
  useEffect(() => {
    if (stage.kind !== "code") return
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    const wait = Math.max(1, stage.start.interval) * 1000
    const tick = async () => {
      try {
        const res = await get<GitHubDeviceState>(`/github/connections/device/${stage.start.id}`)
        if (!alive) return
        if (res.status === "complete" && res.connection) {
          notify.success(`Connected @${res.connection.login}`)
          doneRef.current(res.connection)
          return
        }
        if (res.status !== "pending") {
          setStage({
            kind: "failed",
            message:
              res.message ??
              (res.status === "denied"
                ? "The code was refused on GitHub, so nothing changed."
                : res.status === "expired"
                  ? "The code expired before it was entered."
                  : "GitHub did not finish the sign-in."),
          })
          return
        }
      } catch (error) {
        if (!alive) return
        setStage({ kind: "failed", message: errorMessage(error) })
        return
      }
      timer = setTimeout(tick, wait)
    }
    timer = setTimeout(tick, wait)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [stage])

  useEffect(() => {
    if (stage.kind !== "code") return
    const timer = setInterval(() => setRemaining((left) => Math.max(0, left - 1)), 1000)
    return () => clearInterval(timer)
  }, [stage])

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Sign in with a code"
      description="Enter the code on GitHub to connect your account here."
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {stage.kind === "failed" && !stage.disabled && (
            <Button onClick={() => void start()}>Try again</Button>
          )}
        </>
      }
    >
      {stage.kind === "failed" ? (
        <Notice
          title={stage.disabled ? "Code sign-in is off for this App" : "Not connected"}
          tone={stage.disabled ? "warning" : "danger"}
          live="alert"
        >
          <span className="block">{stage.message}</span>
          {stage.disabled && settingsUrl && (
            <Button size="xs" variant="outline" className="mt-2" asChild>
              <a href={settingsUrl} target="_blank" rel="noreferrer">
                <External className="size-3.5" />
                App settings
              </a>
            </Button>
          )}
        </Notice>
      ) : stage.kind === "starting" ? (
        <div className="flex h-36 items-center justify-center" aria-busy>
          <Spinner label="Asking GitHub for a code" />
        </div>
      ) : (
        <div className="space-y-3">
          <div className="flex h-20 items-center justify-center rounded-lg border border-hairline bg-surface-sunken">
            <code
              data-slot="device-code"
              className="font-mono text-2xl tracking-[0.3em] text-foreground"
            >
              {stage.start.userCode}
            </code>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <CopyButton text={stage.start.userCode} label="Copy code" size="sm" />
            <Button size="sm" asChild>
              <a href={stage.start.verificationUri} target="_blank" rel="noreferrer">
                <GitHubMark className="size-3.5" />
                Open GitHub
              </a>
            </Button>
            <span
              role="status"
              className="ml-auto flex items-center gap-2 text-xs text-muted-foreground"
            >
              <Spinner className="size-3.5" />
              Waiting
              <span className="numeric font-mono">
                {Math.floor(remaining / 60)}:{String(remaining % 60).padStart(2, "0")}
              </span>
            </span>
          </div>
        </div>
      )}
    </Modal>
  )
}
