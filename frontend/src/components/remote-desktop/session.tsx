"use client"

import Link from "next/link"
import { useCallback, useEffect, useId, useRef, useState } from "react"
import type * as Guac from "guacamole-common-js"
import {
  ArrowLeft,
  Clipboard,
  CloudUpload,
  Copy,
  Fullscreen,
  FullscreenClose,
  Plus,
  RefreshClockwise,
  SidebarRightClose,
  SidebarRightOpen,
} from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { Page } from "@/components/page"
import { PaneHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu } from "@/components/verbs"
import { Segments } from "@/components/deploy/settings/segments"
import { del, put, wsUrl } from "@/lib/api"
import { bytes, relativeTime } from "@/lib/format"
import {
  combosFor,
  displaySize,
  isoOf,
  latency,
  osLabel,
  protocolLabel,
  reachability,
  REMOTE_DESKTOP_HREF,
  sessionHref,
  sessionProblem,
  STREAM_QUALITIES,
  streamStage,
  type KeyCombo,
  type RdComputerView,
  type RdStream,
  type SessionMode,
  type StreamQuality,
} from "@/lib/remote-desktop"
import { notify } from "@/lib/toast"
import { useViewState } from "@/lib/view-state"
import { cn } from "@/lib/utils"
import { ComputerMark, HueKey, reachTone } from "@/components/remote-desktop/marks"
import { draftOf, inputOf } from "@/components/remote-desktop/computer-form"
import { useRemoteDesktop } from "@/components/remote-desktop/use-remote-desktop"
import { PinCard, StreamPane, useStream } from "@/components/remote-desktop/stream"

type Phase =
  | { kind: "connecting" }
  | { kind: "sign-in"; fields: string[] }
  | { kind: "connected" }
  | { kind: "ended" }
  | { kind: "failed"; code: number; message: string }

type Transfer = {
  key: string
  name: string
  size: number
  sent: number
  direction: "up" | "down"
  state: "moving" | "done" | "failed"
}

type GuacModule = typeof Guac

// guacamole-common-js is published as one default export, which its typings
// describe as named ones; this is the bridge between the two.
async function loadGuacamole(): Promise<GuacModule> {
  const mod = await import("guacamole-common-js")
  return (mod as unknown as { default?: GuacModule }).default ?? mod
}

/** Guacamole.Client.State, which the typings leave as an enum the bundle does not export. */
const CONNECTED = 3
const DISCONNECTED = 5

/**
 * A computer's screen, live, in the page.
 *
 * Laid out as the terminal is: the computers down the left, the session in
 * the middle under a strip of its commands, and its tools — the clipboard,
 * files and the keys a browser keeps for itself — on the right. The screen
 * is drawn by Guacamole's own browser client from the protocol the backend
 * relays; the backend did the connection's handshake, so the password never
 * reached this page. The remote screen is asked to be the size of the frame
 * and follows it as the window changes, and the pointer is the computer's
 * own cursor drawn by the browser, so it moves without waiting for the
 * network.
 *
 * Keys go to the computer while the screen has focus; fullscreen also asks
 * the browser for the keyboard, so Alt+Tab and the Windows key reach the
 * computer instead of this one.
 *
 * Stream mode puts Sunshine's video in the screen's place, through the
 * bridge, and the Control session is closed while it is up. Pairing is
 * typed into Sunshine on the computer, usually through Control, so a PIN
 * that is waiting stays over the Control screen until Sunshine takes it.
 */
export function RemoteSession({
  id,
  mode,
  visible,
  onMode,
  onDisconnect,
  onState,
}: {
  id: string
  mode: SessionMode
  /** A held session on another page stays connected and stops reading the computers. */
  visible: boolean
  onMode: (mode: SessionMode) => void
  onDisconnect: () => void
  /** What the shell shows of a session kept connected while another page is up. */
  onState: (state: { name: string; live: boolean }) => void
}) {
  const { data, hueOf, refresh } = useRemoteDesktop(visible ? 15_000 : 0)
  const computer = data?.computers.find((candidate) => candidate.id === id)
  const workspace = useRef<HTMLDivElement>(null)
  const viewport = useRef<HTMLDivElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const client = useRef<Guac.Client | null>(null)
  const guac = useRef<GuacModule | null>(null)
  const [phase, setPhase] = useState<Phase>({ kind: "connecting" })
  const [attempt, setAttempt] = useState(0)
  const [immersive, setImmersive] = useState(false)
  const [tools, setTools] = useViewState("remote-desktop.tools", true)
  const [remoteClip, setRemoteClip] = useState("")
  const [localClip, setLocalClip] = useState("")
  const [drive, setDrive] = useState(false)
  const [transfers, setTransfers] = useState<Transfer[]>([])
  const [startedAt, setStartedAt] = useState<number>()
  const [quality, setQuality] = useViewState<StreamQuality>(
    "remote-desktop.stream-quality",
    "smooth",
  )
  const [streamAttempt, setStreamAttempt] = useState(0)
  const [pointer, setPointer] = useViewState("remote-desktop.stream-pointer", false)
  const streamFrame = useRef<HTMLIFrameElement>(null)
  const streamPoll = useStream(id, visible && mode === "stream")
  const stream = streamPoll.data
  const pin = stream?.pairing ? stream.pin : undefined
  const known = computer !== undefined
  const control = mode === "control"
  const live = control ? phase.kind === "connected" : streamPoll.live !== undefined
  const name = computer?.name
  useEffect(() => {
    if (name) onState({ name, live })
  }, [name, live, onState])

  const track = useCallback((key: string, change: Partial<Transfer>) => {
    setTransfers((held) =>
      held.map((transfer) => (transfer.key === key ? { ...transfer, ...change } : transfer)),
    )
  }, [])

  useEffect(() => {
    if (!known || !control) return
    let cancelled = false
    let teardown = () => {}
    void (async () => {
      const G = await loadGuacamole()
      const frame = viewport.current
      const holder = stage.current
      if (cancelled || !frame || !holder) return
      guac.current = G
      const size = displaySize(frame.clientWidth, frame.clientHeight, window.devicePixelRatio)
      const session = new G.Client(
        new G.WebSocketTunnel(wsUrl(`/remote-desktop/computers/${id}/session`)),
      )
      client.current = session
      const display = session.getDisplay()
      const node = display.getElement()
      holder.appendChild(node)

      const fit = () => {
        const width = display.getWidth()
        const height = display.getHeight()
        if (!width || !height || !frame.clientWidth) return
        display.scale(Math.min(frame.clientWidth / width, frame.clientHeight / height))
      }
      display.onresize = fit

      const mouse = new G.Mouse(node)
      const touch = new G.Mouse.Touchscreen(node)
      const pointer = (event: Guac.Mouse.Event) => session.sendMouseState(event.state, true)
      mouse.onEach(["mousedown", "mousemove", "mouseup"], pointer as never)
      touch.onEach(["mousedown", "mousemove", "mouseup"], pointer as never)
      // The computer's own cursor, drawn by the browser: it moves at the
      // pointer's speed rather than the network's.
      display.oncursor = (canvas, x, y) => display.showCursor(!mouse.setCursor(canvas, x, y))

      const keyboard = new G.Keyboard(frame)
      keyboard.onkeydown = (keysym) => {
        session.sendKeyEvent(1, keysym)
      }
      keyboard.onkeyup = (keysym) => session.sendKeyEvent(0, keysym)

      // libvncclient crashes on a size update that arrives while it is still
      // signing in, and Apple's sign-in is slow enough for the first resize
      // to land in it: the screen's size follows the frame only once it is up.
      let connected = false
      const followFrame = () => {
        // A session held while another page is up has no frame to follow;
        // the computer keeps the size it had until the session is shown.
        if (!frame.clientWidth || !frame.clientHeight) return
        const next = displaySize(frame.clientWidth, frame.clientHeight, window.devicePixelRatio)
        session.sendSize(next.width, next.height)
        fit()
      }

      session.onstatechange = (state) => {
        if (state === CONNECTED) {
          connected = true
          followFrame()
          setPhase({ kind: "connected" })
          setStartedAt(Math.floor(Date.now() / 1000))
          frame.focus()
          fit()
        }
        if (state === DISCONNECTED) {
          setPhase((held) => (held.kind === "failed" ? held : { kind: "ended" }))
        }
      }
      // A Mac signs in with its account, and a computer asks for what the
      // dashboard does not hold: the page asks in its place and answers. The
      // tunnel reuses the array it hands over, so the names are copied out.
      session.onrequired = (fields) => setPhase({ kind: "sign-in", fields: [...fields] })
      session.onerror = (status) =>
        setPhase({ kind: "failed", code: status.code, message: status.message ?? "" })
      session.onclipboard = (stream, mimetype) => {
        if (!mimetype.startsWith("text/")) return
        const reader = new G.StringReader(stream)
        let text = ""
        reader.ontext = (chunk) => (text += chunk)
        reader.onend = () => {
          setRemoteClip(text)
          navigator.clipboard?.writeText(text).catch(() => {})
        }
      }
      session.onfilesystem = () => setDrive(true)
      // A file the computer drops into the drive's Download folder arrives
      // here as a stream, and is saved the way any download is.
      session.onfile = (stream, mimetype, filename) => {
        const key = `down:${filename}:${Date.now()}`
        setTransfers((held) => [
          { key, name: filename, size: 0, sent: 0, direction: "down", state: "moving" },
          ...held,
        ])
        const reader = new G.BlobReader(stream, mimetype)
        reader.onprogress = (length) => track(key, { sent: length })
        reader.onend = () => {
          const blob = reader.getBlob()
          const url = URL.createObjectURL(blob)
          const link = document.createElement("a")
          link.href = url
          link.download = filename
          link.click()
          setTimeout(() => URL.revokeObjectURL(url), 30_000)
          track(key, { state: "done", size: blob.size, sent: blob.size })
        }
        stream.sendAck("Ready", G.Status.Code.SUCCESS)
      }

      const observer = new ResizeObserver(() => {
        if (!connected) return
        window.clearTimeout(resizeTimer)
        resizeTimer = window.setTimeout(followFrame, 250)
      })
      let resizeTimer = 0
      observer.observe(frame)

      session.connect(
        new URLSearchParams({
          width: String(size.width),
          height: String(size.height),
          dpi: String(size.dpi),
          timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
        }).toString(),
      )

      teardown = () => {
        observer.disconnect()
        window.clearTimeout(resizeTimer)
        keyboard.onkeydown = null
        keyboard.onkeyup = null
        keyboard.reset()
        session.onstatechange = null
        session.onerror = null
        session.disconnect()
        node.remove()
        client.current = null
      }
    })()
    return () => {
      cancelled = true
      teardown()
    }
  }, [id, known, control, attempt, track])

  useEffect(() => {
    const onChange = () => {
      if (!document.fullscreenElement) setImmersive(false)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [])

  const toggleImmersive = async () => {
    const keyboard = (
      navigator as Navigator & { keyboard?: { lock?: () => Promise<void>; unlock?: () => void } }
    ).keyboard
    if (document.fullscreenElement) {
      keyboard?.unlock?.()
      await document.exitFullscreen()
      return
    }
    await workspace.current?.requestFullscreen?.()
    setImmersive(true)
    // In fullscreen a browser will hand over the keys it otherwise keeps,
    // so Alt+Tab and the Windows key switch windows on the computer.
    await keyboard?.lock?.().catch(() => {})
    if (control) {
      viewport.current?.focus()
      return
    }
    // The stream takes keys in its own page, on its input element.
    const frame = streamFrame.current
    frame?.focus()
    frame?.contentDocument?.getElementById("input")?.focus()
  }

  const switchMode = (next: SessionMode) => {
    if (next === mode) return
    // The Control session is closed in Stream and opened afresh on the way back.
    if (next === "control") setPhase({ kind: "connecting" })
    onMode(next)
  }

  const signIn = async (values: Record<string, string>, remember: boolean) => {
    const session = client.current
    const G = guac.current
    if (!session || !G || !computer) return
    for (const [name, value] of Object.entries(values)) {
      const writer = new G.StringWriter(session.createArgumentValueStream("text/plain", name))
      writer.sendText(value)
      writer.sendEnd()
    }
    setPhase({ kind: "connecting" })
    if (!remember) return
    try {
      await put(
        `/remote-desktop/computers/${computer.id}`,
        inputOf(
          {
            ...draftOf(computer),
            username: values.username ?? computer.username,
            domain: values.domain ?? computer.domain,
            password: values.password ?? "",
          },
          true,
        ),
      )
      refresh()
    } catch (err) {
      notify.error(`Could not save the sign-in for ${computer.name}`, err)
    }
  }

  const send = (combo: KeyCombo) => {
    const session = client.current
    if (!session) return
    for (const key of combo.keys) session.sendKeyEvent(1, key)
    for (const key of [...combo.keys].reverse()) session.sendKeyEvent(0, key)
    viewport.current?.focus()
  }

  const paste = () => {
    const session = client.current
    const G = guac.current
    if (!session || !G || localClip === "") return
    const writer = new G.StringWriter(session.createClipboardStream("text/plain"))
    writer.sendText(localClip)
    writer.sendEnd()
    notify.success(`Sent to ${computer?.name ?? "the computer"}'s clipboard`)
    viewport.current?.focus()
  }

  const upload = (files: FileList | File[]) => {
    const session = client.current
    const G = guac.current
    if (!session || !G) return
    for (const file of Array.from(files)) {
      const key = `up:${file.name}:${Date.now()}`
      setTransfers((held) => [
        { key, name: file.name, size: file.size, sent: 0, direction: "up", state: "moving" },
        ...held,
      ])
      const writer = new G.BlobWriter(
        session.createFileStream(file.type || "application/octet-stream", file.name),
      )
      writer.onprogress = (_blob, offset) => track(key, { sent: offset })
      writer.oncomplete = () => {
        writer.sendEnd()
        track(key, { state: "done", sent: file.size })
      }
      writer.onerror = () => track(key, { state: "failed" })
      writer.sendBlob(file)
    }
  }

  // A browser starts its audio only after a press, and the session's sound
  // arrives before one: the first press on the screen lets it play.
  const wake = () => {
    const factory = (
      guac.current as unknown as {
        AudioContextFactory?: { getAudioContext(): AudioContext | null }
      }
    )?.AudioContextFactory
    void factory?.getAudioContext()?.resume()
  }

  if (data && !computer) {
    return (
      <Page className="animate-rise">
        <EmptyState
          title="This computer is not in Remote Desktop"
          description="It may have been removed. The computers that are still there are on the Remote Desktop page."
          action={
            <Button size="sm" variant="outline" asChild>
              <Link href={REMOTE_DESKTOP_HREF}>Back to the computers</Link>
            </Button>
          }
        />
      </Page>
    )
  }

  const reach = computer ? reachability(computer) : undefined
  const problem =
    phase.kind === "failed" && computer
      ? sessionProblem(phase.code, phase.message, computer)
      : undefined

  return (
    <Page fill className="gap-2 px-2 py-2 md:px-3 md:py-3">
      <h1 className="sr-only">
        {computer ? `Remote Desktop: ${computer.name}` : "Remote Desktop"}
      </h1>
      <div
        ref={workspace}
        data-slot="remote-workspace"
        className={cn(
          "relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card",
          immersive && "fixed inset-0 z-50 rounded-none border-0 bg-background",
        )}
      >
        {!immersive && data && <Rail computers={data.computers} current={id} hueOf={hueOf} />}

        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <PaneHeader className="min-h-10 shrink-0 flex-wrap gap-1 py-0.5">
            <Link
              href={REMOTE_DESKTOP_HREF}
              aria-label="Back to the computers"
              className="flex size-8 items-center justify-center rounded-md text-muted-foreground focus-ring transition-colors hover:bg-accent hover:text-foreground lg:hidden"
            >
              <ArrowLeft className="size-4" />
            </Link>
            {computer && (
              <span className="flex min-w-0 items-center gap-2 px-1">
                <ComputerMark os={computer.os} size="sm" className="scale-75" />
                <span className="truncate text-body font-medium">{computer.name}</span>
              </span>
            )}
            <Segments
              label="How the screen is drawn"
              value={mode}
              onChange={switchMode}
              options={[
                {
                  value: "control",
                  label: computer ? protocolLabel(computer.protocol, computer.os) : "Control",
                },
                { value: "stream", label: "Stream" },
              ]}
            />
            <span className="flex-1" />
            {!control ? (
              <StreamStatus stream={stream} />
            ) : phase.kind === "connected" ? (
              <Status
                tone="running"
                live
                label={latency(computer?.reach.latencyMs) ?? "connected"}
                className="px-2"
              />
            ) : phase.kind === "connecting" ? (
              <TextShimmer className="px-2 text-xs font-medium">connecting…</TextShimmer>
            ) : phase.kind === "sign-in" ? (
              <Status tone="notice" label="waiting for a sign-in" className="px-2" />
            ) : (
              <Status
                tone={phase.kind === "failed" ? "warning" : "stopped"}
                label={phase.kind === "failed" ? "not connected" : "ended"}
                className="px-2"
              />
            )}
            {computer && control && phase.kind === "connected" && (
              <VerbMenu
                label="Send keys"
                trigger={
                  <Button size="sm" variant="ghost">
                    Keys
                  </Button>
                }
                verbs={combosFor(computer.os).map((combo) => ({
                  key: combo.key,
                  label: combo.label,
                  icon: Plus,
                  run: () => send(combo),
                }))}
              />
            )}
            {!control && stream && streamStage(stream) === "ready" && (
              <IconAction label="Reload the stream" onClick={() => setStreamAttempt((n) => n + 1)}>
                <RefreshClockwise />
              </IconAction>
            )}
            <IconAction
              label={tools ? "Hide the session tools" : "Show the session tools"}
              onClick={() => setTools((held) => !held)}
              className="max-xl:hidden"
            >
              {tools ? <SidebarRightClose /> : <SidebarRightOpen />}
            </IconAction>
            <IconAction
              label={
                immersive
                  ? "Leave fullscreen"
                  : `Fullscreen, with every key going to ${computer?.name ?? "the computer"}`
              }
              onClick={() => void toggleImmersive()}
            >
              {immersive ? <FullscreenClose /> : <Fullscreen />}
            </IconAction>
            <Button size="sm" variant="outline" asChild>
              <Link href={REMOTE_DESKTOP_HREF} onClick={onDisconnect}>
                Disconnect
              </Link>
            </Button>
          </PaneHeader>

          {!control && computer ? (
            <div className="relative min-h-0 flex-1 bg-surface-sunken">
              <StreamPane
                computer={computer}
                stream={stream}
                live={streamPoll.live}
                quality={quality}
                attempt={streamAttempt}
                pointer={pointer}
                onFrame={(node) => {
                  streamFrame.current = node
                }}
                onRefresh={streamPoll.refresh}
                onControl={() => switchMode("control")}
              />
            </div>
          ) : (
            <div
              className="relative min-h-0 flex-1 bg-surface-sunken"
              onDragOver={(event) => {
                if (drive) event.preventDefault()
              }}
              onDrop={(event) => {
                if (!drive) return
                event.preventDefault()
                upload(event.dataTransfer.files)
              }}
            >
              <div
                ref={viewport}
                tabIndex={0}
                role="application"
                aria-label={computer ? `The screen of ${computer.name}` : "The remote screen"}
                onPointerDown={() => {
                  wake()
                  viewport.current?.focus()
                }}
                className={cn(
                  "absolute inset-0 flex items-center justify-center overflow-hidden outline-none",
                  phase.kind !== "connected" && "invisible",
                )}
              >
                <div ref={stage} data-slot="remote-screen" className="relative" />
              </div>
              {computer && pin && (
                <PinCard computer={computer} pin={pin} onStream={() => switchMode("stream")} />
              )}
              {computer && phase.kind !== "connected" && (
                <div className="absolute inset-0 flex items-center justify-center p-6">
                  <div className="flex max-w-md min-w-0 flex-col items-center gap-4 text-center">
                    <ComputerMark os={computer.os} size="lg" reach={reach} />
                    {phase.kind === "sign-in" ? (
                      <SignIn computer={computer} fields={phase.fields} onSubmit={signIn} />
                    ) : phase.kind === "connecting" ? (
                      <>
                        <TextShimmer className="text-title font-semibold">{`Connecting to ${computer.name}…`}</TextShimmer>
                        <p className="text-hint text-muted-foreground">
                          Through guacd on this server, to {computer.host}:{computer.port}
                        </p>
                      </>
                    ) : (
                      <>
                        <div className="space-y-1.5">
                          <p className="text-title font-semibold">
                            {problem ? problem.title : "The session ended"}
                          </p>
                          <p className="text-body text-muted-foreground">
                            {problem
                              ? problem.detail
                              : `${computer.name} is still there; connect again whenever you need it.`}
                          </p>
                        </div>
                        <div className="flex flex-wrap items-center justify-center gap-2">
                          <Button
                            size="sm"
                            onClick={() => {
                              setPhase({ kind: "connecting" })
                              setAttempt((held) => held + 1)
                              refresh()
                            }}
                          >
                            <RefreshClockwise className="size-3.5" />
                            Connect again
                          </Button>
                          <Button size="sm" variant="outline" asChild>
                            <Link href={REMOTE_DESKTOP_HREF}>Back to the computers</Link>
                          </Button>
                        </div>
                      </>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        {tools && !immersive && computer && !control && (
          <StreamTools
            computer={computer}
            stream={stream}
            quality={quality}
            onQuality={setQuality}
            pointer={pointer}
            onPointer={setPointer}
            onUnpaired={streamPoll.refresh}
          />
        )}
        {tools && !immersive && computer && control && (
          <Tools
            computer={computer}
            connected={phase.kind === "connected"}
            startedAt={startedAt}
            remoteClip={remoteClip}
            localClip={localClip}
            onLocalClip={setLocalClip}
            onPaste={paste}
            drive={drive}
            transfers={transfers}
            onUpload={upload}
            onKeys={send}
          />
        )}
      </div>
    </Page>
  )
}

/** The computers, to move between without going back to the page. */
function Rail({
  computers,
  current,
  hueOf,
}: {
  computers: RdComputerView[]
  current: string
  hueOf: (computer: RdComputerView) => string
}) {
  return (
    <nav
      aria-label="Computers"
      className="hidden w-60 shrink-0 flex-col border-r border-hairline lg:flex"
    >
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-hairline pr-1.5 pl-3">
        <Link
          href={REMOTE_DESKTOP_HREF}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-sm text-body font-medium focus-ring"
        >
          <ArrowLeft aria-hidden className="size-3.5 text-muted-foreground" />
          Computers
        </Link>
        <Button size="icon-sm" variant="ghost" asChild>
          <Link href={`${REMOTE_DESKTOP_HREF}/new`} aria-label="Add a computer">
            <Plus />
          </Link>
        </Button>
      </div>
      <ul className="flex min-h-0 flex-col gap-0.5 overflow-y-auto p-2">
        {computers.map((computer) => {
          const reach = reachability(computer)
          const active = computer.id === current
          return (
            <li key={computer.id}>
              <Link
                href={sessionHref(computer.id)}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex min-w-0 items-center gap-2.5 rounded-md py-2 pr-2.5 pl-3 text-body focus-ring-inset transition-colors hover:bg-row-hover",
                  active && "bg-accent font-medium hover:bg-accent",
                )}
              >
                <HueKey color={hueOf(computer)} />
                <StatusDot
                  tone={reachTone(reach)}
                  live={reach.kind === "live"}
                  className="size-1.5"
                />
                <span className="min-w-0 flex-1 truncate">{computer.name}</span>
                <span className="numeric shrink-0 text-hint font-normal text-muted-foreground">
                  {reach.kind === "open" || reach.kind === "live"
                    ? latency(computer.reach.latencyMs)
                    : reach.kind === "offline"
                      ? "offline"
                      : "closed"}
                </span>
              </Link>
            </li>
          )
        })}
      </ul>
    </nav>
  )
}

function Tools({
  computer,
  connected,
  startedAt,
  remoteClip,
  localClip,
  onLocalClip,
  onPaste,
  drive,
  transfers,
  onUpload,
  onKeys,
}: {
  computer: RdComputerView
  connected: boolean
  startedAt?: number
  remoteClip: string
  localClip: string
  onLocalClip: (text: string) => void
  onPaste: () => void
  drive: boolean
  transfers: Transfer[]
  onUpload: (files: FileList) => void
  onKeys: (combo: KeyCombo) => void
}) {
  const picker = useRef<HTMLInputElement>(null)
  const path = computer.peer
    ? computer.peer.direct
      ? "Direct over Tailscale"
      : computer.peer.relay
        ? `Relayed via ${computer.peer.relay}`
        : "Over Tailscale"
    : "This server's network"
  return (
    <aside
      aria-label="Session tools"
      className="hidden w-80 shrink-0 flex-col overflow-y-auto border-l border-hairline xl:flex"
    >
      <div className="flex h-10 shrink-0 items-center border-b border-hairline px-4 text-body font-medium">
        Session
      </div>
      <div className="flex flex-col gap-6 p-4">
        <section aria-label="Connection" className="flex flex-col gap-2 text-hint">
          <p className="eyebrow">Connection</p>
          <Fact label="System">
            {osLabel(computer.os)} · {protocolLabel(computer.protocol, computer.os)}
          </Fact>
          <Fact label="Path">{path}</Fact>
          <Fact label="Round trip">{latency(computer.reach.latencyMs) ?? "—"}</Fact>
          <Fact label="Signed in as">
            {computer.username || (computer.hasPassword ? "password only" : "—")}
          </Fact>
          {startedAt && <Fact label="Connected">{relativeTime(isoOf(startedAt))}</Fact>}
        </section>

        <section aria-label="Clipboard" className="flex flex-col gap-2">
          <label htmlFor="remote-clip" className="eyebrow">
            Clipboard
          </label>
          <Textarea
            id="remote-clip"
            rows={3}
            value={localClip}
            onChange={(event) => onLocalClip(event.target.value)}
            placeholder={`Text to paste on ${computer.name}`}
            className="resize-none text-hint"
          />
          <Button
            size="sm"
            variant="outline"
            className="self-start"
            disabled={!connected || localClip === ""}
            onClick={onPaste}
          >
            <Clipboard className="size-3.5" />
            Send to {computer.name}
          </Button>
          {remoteClip && (
            <div className="flex min-w-0 items-start gap-2 rounded-lg border border-hairline bg-surface-sunken p-2.5">
              <p className="line-clamp-3 min-w-0 flex-1 font-mono text-hint break-words">
                {remoteClip}
              </p>
              <IconAction
                label="Copy what the computer copied"
                onClick={() => {
                  void navigator.clipboard?.writeText(remoteClip)
                  notify.success("Copied")
                }}
              >
                <Copy />
              </IconAction>
            </div>
          )}
        </section>

        {computer.protocol === "rdp" && (
          <section aria-label="Files" className="flex flex-col gap-2">
            <p className="eyebrow">Files</p>
            <button
              type="button"
              disabled={!drive}
              onClick={() => picker.current?.click()}
              className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border-strong px-3 py-4 text-center text-hint text-muted-foreground focus-ring transition-colors hover:text-foreground disabled:opacity-50"
            >
              <CloudUpload className="size-5" />
              {drive
                ? `Drop files on the screen, or choose them, to put them in the Just Dashboard drive on ${computer.name}`
                : "Files can be sent once the session is open"}
            </button>
            <input
              ref={picker}
              type="file"
              multiple
              className="hidden"
              onChange={(event) => {
                if (event.target.files) onUpload(event.target.files)
                event.target.value = ""
              }}
            />
            <p className="text-hint text-muted-foreground">
              To send a file here, put it in the drive&apos;s Download folder on the computer.
            </p>
            {transfers.length > 0 && (
              <ul className="flex flex-col gap-1.5">
                {transfers.slice(0, 6).map((transfer) => (
                  <li key={transfer.key} className="flex min-w-0 items-center gap-2 text-hint">
                    <StatusDot
                      tone={
                        transfer.state === "done"
                          ? "running"
                          : transfer.state === "failed"
                            ? "danger"
                            : "notice"
                      }
                      live={transfer.state === "moving"}
                      className="size-1.5"
                    />
                    <span className="min-w-0 flex-1 truncate">{transfer.name}</span>
                    <span className="numeric shrink-0 text-muted-foreground">
                      {transfer.state === "failed"
                        ? "failed"
                        : transfer.direction === "up"
                          ? `${bytes(transfer.sent)} of ${bytes(transfer.size)}`
                          : bytes(transfer.sent)}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}

        <KeysNote name={computer.name} />

        <section aria-label="Keys" className="flex flex-col gap-2">
          <p className="eyebrow">Send keys</p>
          <div className="grid grid-cols-2 gap-1.5">
            {combosFor(computer.os).map((combo) => (
              <Button
                key={combo.key}
                size="sm"
                variant="outline"
                disabled={!connected}
                onClick={() => onKeys(combo)}
              >
                {combo.label}
              </Button>
            ))}
          </div>
        </section>
      </div>
    </aside>
  )
}

// Keyboard Lock is Chrome's and Edge's; other browsers keep their own keys
// even in fullscreen.
const keyboardLock = Boolean(
  (navigator as Navigator & { keyboard?: { lock?: unknown } }).keyboard?.lock,
)

/**
 * Where the keys go. With the screen focused every key a page may have goes
 * to the computer; the system's own (Alt+Tab, the Windows key) and the
 * browser's (Ctrl+W, Ctrl+T) never reach a page at all, except in fullscreen
 * with the keyboard locked, which is what the fullscreen button asks for.
 */
function KeysNote({ name }: { name: string }) {
  return (
    <section aria-label="Keyboard" className="flex flex-col gap-1.5">
      <p className="eyebrow">Keyboard</p>
      <p className="text-hint text-muted-foreground">
        {keyboardLock
          ? `With the screen focused, keys go to ${name}. Alt+Tab, the Windows key and the browser's own shortcuts stay with this computer until fullscreen, which sends every key to ${name}; hold Esc to leave it.`
          : `With the screen focused, keys go to ${name}. This browser keeps Alt+Tab, the Windows key and its own shortcuts even in fullscreen; Chrome and Edge send them on in fullscreen.`}
      </p>
    </section>
  )
}

function StreamStatus({ stream }: { stream: RdStream | undefined }) {
  if (!stream) return <TextShimmer className="px-2 text-xs font-medium">reading…</TextShimmer>
  const stage = streamStage(stream)
  if (stage === "ready")
    return (
      <Status
        tone="running"
        live
        label={latency(stream.sunshine.latencyMs) ?? "streaming"}
        className="px-2"
      />
    )
  if (stage === "pin") return <Status tone="notice" label="waiting for the PIN" className="px-2" />
  return (
    <Status
      tone="warning"
      label={
        stage === "bridge"
          ? "bridge not running"
          : stage === "sunshine"
            ? stream.sunshine.error === "stuck"
              ? "Sunshine stuck"
              : "Sunshine not answering"
            : "not paired"
      }
      className="px-2"
    />
  )
}

/**
 * Stream mode's side of the tools: where the picture comes from, how sharp
 * it is asked to be, and pairing again. The keyboard, mouse modes and the
 * stream's own statistics are in the stream's menu, at its left edge.
 */
function StreamTools({
  computer,
  stream,
  quality,
  onQuality,
  pointer,
  onPointer,
  onUnpaired,
}: {
  computer: RdComputerView
  stream: RdStream | undefined
  quality: StreamQuality
  onQuality: (quality: StreamQuality) => void
  pointer: boolean
  onPointer: (pointer: boolean) => void
  onUnpaired: () => void
}) {
  const [busy, setBusy] = useState(false)
  const preset = STREAM_QUALITIES.find((q) => q.key === quality) ?? STREAM_QUALITIES[0]
  const unpair = async () => {
    setBusy(true)
    try {
      await del(`/remote-desktop/computers/${computer.id}/stream`)
      onUnpaired()
    } catch (err) {
      notify.error(`Could not unpair ${computer.name}`, err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <aside
      aria-label="Stream tools"
      className="hidden w-80 shrink-0 flex-col overflow-y-auto border-l border-hairline xl:flex"
    >
      <div className="flex h-10 shrink-0 items-center border-b border-hairline px-4 text-body font-medium">
        Stream
      </div>
      <div className="flex flex-col gap-6 p-4">
        <section aria-label="Connection" className="flex flex-col gap-2 text-hint">
          <p className="eyebrow">Connection</p>
          <Fact label="From">Sunshine{stream?.address ? ` at ${stream.address}` : ""}</Fact>
          <Fact label="Through">Moonlight Web on this server</Fact>
          <Fact label="Picture">H.264 over this dashboard</Fact>
          <Fact label="Round trip">{latency(stream?.sunshine.latencyMs) ?? "—"}</Fact>
          <Fact label="Paired">{stream?.paired ? "yes" : "not yet"}</Fact>
        </section>

        <section aria-label="Quality" className="flex flex-col gap-2">
          <p className="eyebrow">Quality</p>
          <Segments
            label="Stream quality"
            value={quality}
            onChange={onQuality}
            fill
            options={STREAM_QUALITIES.map((q) => ({ value: q.key, label: q.label }))}
          />
          <p className="text-hint text-muted-foreground">
            {preset.detail}, up to {preset.bitrate / 1000} Mb/s. Sunshine scales the screen to it; a
            change reloads the stream.
          </p>
        </section>

        <section aria-label="In the stream" className="flex flex-col gap-2">
          <p className="eyebrow">In the stream</p>
          <label className="flex items-start gap-2 text-hint">
            <Checkbox
              checked={pointer}
              onCheckedChange={(on) => onPointer(on === true)}
              className="mt-px"
            />
            <span>
              Show this computer&apos;s pointer too
              <span className="block text-muted-foreground">
                {computer.name} draws its own into the picture, a round trip behind.
              </span>
            </span>
          </label>
          <p className="text-hint text-muted-foreground">
            The arrow at the stream&apos;s left edge opens its menu: the on-screen keyboard, the
            mouse&apos;s modes, its statistics and leaving fullscreen.
          </p>
        </section>

        <KeysNote name={computer.name} />

        {stream?.hostId ? (
          <section aria-label="Pairing" className="flex flex-col gap-2">
            <p className="eyebrow">Pairing</p>
            <p className="text-hint text-muted-foreground">
              Unpairing forgets {computer.name} on the bridge; it is paired again with a new PIN.
              Remove the device from Sunshine&apos;s Clients list too to revoke it there.
            </p>
            <Button
              size="sm"
              variant="outline"
              className="self-start"
              pending={busy}
              onClick={() => void unpair()}
            >
              Unpair
            </Button>
          </section>
        ) : null}
      </div>
    </aside>
  )
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 justify-between gap-3">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 truncate text-right">{children}</span>
    </div>
  )
}

const FIELD_ORDER = ["username", "domain", "password"]

/**
 * What the computer asked for, asked of the reader: a Mac signs in with its
 * account name and login password, and a PC without a saved password asks
 * for one. The answer goes to guacd on this connection and, unless the box is
 * cleared, is saved for the next one.
 */
function SignIn({
  computer,
  fields,
  onSubmit,
}: {
  computer: RdComputerView
  fields: string[]
  onSubmit: (values: Record<string, string>, remember: boolean) => void
}) {
  const id = useId()
  const [values, setValues] = useState<Record<string, string>>(() =>
    Object.fromEntries(
      fields.map((field) => [field, field === "username" ? computer.username : ""]),
    ),
  )
  const [remember, setRemember] = useState(true)
  const mac = computer.os === "macos"
  const label = (field: string) =>
    field === "username"
      ? mac
        ? "Mac account name"
        : "Username"
      : field === "password"
        ? mac
          ? "Mac login password"
          : "Password"
        : "Domain"
  const asked = FIELD_ORDER.filter((field) => fields.includes(field))
  return (
    <form
      className="flex w-full max-w-sm flex-col gap-3 text-left"
      onSubmit={(event) => {
        event.preventDefault()
        onSubmit(values, remember)
      }}
    >
      <div className="space-y-1 text-center">
        <p className="text-title font-semibold">Sign in to {computer.name}</p>
        <p className="text-body text-muted-foreground">
          {mac
            ? "The Mac signs in with one of its accounts: the short account name and the password you log in with."
            : `${computer.name} asks for a sign-in the dashboard does not have saved.`}
        </p>
      </div>
      {asked.map((field, index) => (
        <div key={field} className="flex flex-col gap-1.5">
          <label htmlFor={`${id}-${field}`} className="text-hint font-medium">
            {label(field)}
          </label>
          <Input
            id={`${id}-${field}`}
            type={field === "password" ? "password" : "text"}
            autoFocus={index === 0}
            autoComplete={field === "password" ? "current-password" : "off"}
            spellCheck={false}
            value={values[field] ?? ""}
            onChange={(event) => setValues((held) => ({ ...held, [field]: event.target.value }))}
          />
        </div>
      ))}
      <label className="flex items-center gap-2 text-hint text-muted-foreground">
        <Checkbox checked={remember} onCheckedChange={(on) => setRemember(on === true)} />
        Save for {computer.name}
      </label>
      <Button type="submit" className="self-end">
        Sign in
      </Button>
    </form>
  )
}
