"use client"

import { useEffect, useRef, useState } from "react"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Download, Key, Play, TerminalWindow, Warning } from "@/components/icons"
import { ProductLogo } from "@/components/product-logo"
import { ErrorState, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import {
  BRIDGE_SETTINGS_KEY,
  bridgeSettings,
  desktopApp,
  streamPageUrl,
  streamStage,
  SUNSHINE_PORT,
  type RdComputerView,
  type RdOS,
  type RdStream,
  type StreamQuality,
  type StreamStats,
} from "@/lib/remote-desktop"
import { meterVideo } from "@/lib/remote-desktop-stats"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"

/**
 * A computer's stream, read while Stream is open or a PIN is waiting to be
 * typed: quickly while it waits, so the page turns the moment Sunshine
 * takes the PIN, and slowly otherwise.
 */
export function useStream(id: string, active: boolean) {
  const [waiting, setWaiting] = useState(false)
  const [pinOut, setPinOut] = useState(false)
  // Keep the last usable bridge connection through a transient Sunshine probe miss.
  // Readiness permits mounting the player; playback is observed separately.
  const [readyStream, setReadyStream] = useState<RdStream>()
  const poll = usePoll(
    async (signal) => {
      const stream = await get<RdStream>(
        `/remote-desktop/computers/${id}/stream`,
        undefined,
        signal,
      )
      const ready = streamStage(stream) === "ready"
      setWaiting(!ready)
      setPinOut(stream.pairing)
      if (ready) setReadyStream(stream)
      else if (!stream.paired || stream.bridge.state !== "running") setReadyStream(undefined)
      return stream
    },
    waiting ? 2000 : 10_000,
    [id],
    { enabled: active || pinOut },
  )
  return { ...poll, readyStream }
}

/**
 * Stream mode in the screen's place: whatever it waits on, said where the
 * screen would be, and then the screen itself. The bridge's own stream page
 * draws it, framed from the dashboard's origin, so the dashboard's sign-in is
 * the only one and the picture comes over the dashboard's connection.
 */
export function StreamPane({
  computer,
  stream,
  readyStream,
  error,
  onPlayback,
  onStats,
  quality,
  attempt,
  pointer,
  onFrame,
  onRefresh,
  onControl,
}: {
  computer: RdComputerView
  stream: RdStream | undefined
  readyStream: RdStream | undefined
  error: Error | undefined
  onPlayback: (playing: boolean) => void
  /** What the stream measures of itself each second; undefined once it stops. */
  onStats: (stats: StreamStats | undefined) => void
  quality: StreamQuality
  attempt: number
  pointer: boolean
  onFrame: (frame: HTMLIFrameElement | null) => void
  onRefresh: () => void
  onControl: () => void
}) {
  if (readyStream)
    return (
      <StreamFrame
        computer={computer}
        stream={readyStream}
        onPlayback={onPlayback}
        onStats={onStats}
        quality={quality}
        attempt={attempt}
        pointer={pointer}
        onFrame={onFrame}
      />
    )
  if (error && !stream)
    return (
      <Centered>
        <ErrorState error={error} onRetry={onRefresh} />
      </Centered>
    )
  if (!stream)
    return (
      <Centered>
        <TextShimmer className="text-title font-semibold">{`Reading ${computer.name}'s stream…`}</TextShimmer>
      </Centered>
    )
  const stage = streamStage(stream)
  return (
    <div className="absolute inset-0 overflow-y-auto">
      <div className="mx-auto flex min-h-full max-w-xl flex-col justify-center gap-5 p-6">
        {stage === "bridge" ? (
          <BridgeSetup stream={stream} onReady={onRefresh} />
        ) : stage === "sunshine" ? (
          <TurnOnSunshine computer={computer} stream={stream} onControl={onControl} />
        ) : stage === "pin" ? (
          <TypePin computer={computer} pin={stream.pin ?? ""} onControl={onControl} />
        ) : (
          <Pair computer={computer} onPaired={onRefresh} />
        )}
        {stream.error && (
          <Notice tone="warning" icon={Warning} title="The stream is not ready">
            <p className="text-body text-muted-foreground">{stream.error}</p>
          </Notice>
        )}
      </div>
    </div>
  )
}

function Centered({ children }: { children: React.ReactNode }) {
  return (
    <div className="absolute inset-0 flex items-center justify-center p-6 text-center">
      {children}
    </div>
  )
}

function Heading({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-3 text-center">
      <ProductLogo id="sunshine" />
      <div className="space-y-1.5">
        <p className="text-title font-semibold">{title}</p>
        <p className="text-body text-muted-foreground">{children}</p>
      </div>
    </div>
  )
}

/**
 * The bridge's install, the same one press as the engine's: pull Moonlight
 * Web, make its container on loopback beside guacd, and wait for it.
 */
function BridgeSetup({ stream, onReady }: { stream: RdStream; onReady: () => void }) {
  const console_ = useJobConsole({ onSuccess: onReady })
  const [starting, setStarting] = useState(false)
  const busy = starting || console_.running
  const install = async () => {
    setStarting(true)
    try {
      console_.attach(await post<Job>("/remote-desktop/stream"))
    } catch (err) {
      notify.error("Could not start the install", err)
    } finally {
      setStarting(false)
    }
  }
  const fixable = stream.bridge.state === "missing" || stream.bridge.state === "stopped"
  return (
    <>
      <Heading title="Stream the screen as video">
        Stream mode receives the computer&apos;s screen from Sunshine as hardware-encoded video, at
        its own frame rate, instead of drawing it a picture at a time. It needs a small bridge on
        this server, Moonlight Web, which listens on this server only.
      </Heading>
      <div className="flex justify-center">
        {fixable ? (
          <Button pending={busy} onClick={() => void install()}>
            <Download className="size-4" />
            {stream.bridge.state === "stopped" ? "Start Stream mode" : "Install Stream mode"}
          </Button>
        ) : (
          <p className="text-body text-muted-foreground">{stream.bridge.error}</p>
        )}
      </div>
      {console_.job && <JobConsole job={console_.job} lines={console_.lines} className="h-56" />}
    </>
  )
}

type Step = { title: React.ReactNode; detail?: React.ReactNode }

function Steps({ steps }: { steps: Step[] }) {
  return (
    <ol className="flex min-w-0 flex-col gap-4 text-body">
      {steps.map((item, index) => (
        <li key={index} className="flex min-w-0 gap-3">
          <span className="numeric flex size-6 shrink-0 items-center justify-center rounded-md border border-hairline bg-surface-sunken text-hint font-medium">
            {index + 1}
          </span>
          <div className="min-w-0 space-y-1 pt-0.5">
            <p className="font-medium">{item.title}</p>
            {item.detail && <p className="text-muted-foreground">{item.detail}</p>}
          </div>
        </li>
      ))}
    </ol>
  )
}

function Mono({ children }: { children: React.ReactNode }) {
  return <code className="font-mono text-foreground">{children}</code>
}

function path(...parts: string[]) {
  return <span className="font-medium text-foreground">{parts.join(" › ")}</span>
}

const SUNSHINE_PAGE = "https://localhost:47990"

/** Where Sunshine comes from and what it needs, on each system. */
function sunshineSteps(os: RdOS): Step[] {
  const page: Step = {
    title: (
      <>
        Open <Mono>{SUNSHINE_PAGE}</Mono> on the computer
      </>
    ),
    detail:
      "Sunshine's own page. The first time, it asks for a username and password of its own; they stay on the computer.",
  }
  if (os === "macos")
    return [
      {
        title: "Install Sunshine",
        detail: (
          <>
            In Terminal: <Mono>brew install lizardbyte/homebrew/sunshine</Mono>. Skip this if it is
            already installed.
          </>
        ),
      },
      {
        title: "Keep it running",
        detail: (
          <>
            <Mono>brew services start sunshine</Mono> starts it now and every time you sign in.
          </>
        ),
      },
      {
        title: <>Let it see and control the screen</>,
        detail: (
          <>
            In {path("System Settings", "Privacy & Security")}, turn Sunshine on under Screen &amp;
            System Audio Recording and under Accessibility. If the firewall asks, allow incoming
            connections.
          </>
        ),
      },
      page,
      {
        title: "Use the software encoder",
        detail: (
          <>
            On that page, {path("Configuration", "Audio/Video", "Encoder")}: choose Software, then
            Save and Apply. Sunshine&apos;s VideoToolbox path holds each frame about 120 ms before
            it leaves the Mac; the software encoder about 10, and an Apple silicon Mac encodes
            1080p60 with it easily.
          </>
        ),
      },
    ]
  if (os === "windows")
    return [
      {
        title: "Install Sunshine",
        detail: (
          <>
            The installer from <Mono>github.com/LizardByte/Sunshine/releases</Mono> sets it up as a
            service that starts with Windows, and opens its ports in the firewall.
          </>
        ),
      },
      page,
    ]
  return [
    {
      title: "Install Sunshine",
      detail: (
        <>
          Your distribution&apos;s package from <Mono>github.com/LizardByte/Sunshine/releases</Mono>
          .
        </>
      ),
    },
    {
      title: "Keep it running",
      detail: (
        <>
          <Mono>systemctl --user enable --now sunshine</Mono> starts it now and with every sign-in.
        </>
      ),
    },
    page,
  ]
}

/**
 * Sunshine's port not answering: what to install and allow, in the
 * computer's own system's words, while the port is watched. The Mac is
 * likely far away, so Control is one press: every step can be done there.
 */
/**
 * A Sunshine that is running but stuck: it takes the connection on its
 * pairing port and never answers, so the bridge can neither pair nor stream.
 * What usually holds it is a system prompt nobody at the computer has
 * answered, else a copy of it that hung.
 */
function unstickSteps(os: RdOS): Step[] {
  const restart: Step =
    os === "macos"
      ? {
          title: "Restart Sunshine",
          detail: (
            <>
              In Terminal: <Mono>brew services restart sunshine</Mono>. If it was started by hand
              too, quit that copy first: <Mono>lsof -iTCP:{SUNSHINE_PORT} -sTCP:LISTEN</Mono> lists
              what holds the port.
            </>
          ),
        }
      : os === "windows"
        ? {
            title: "Restart Sunshine",
            detail: (
              <>
                Its tray icon › Restart, or restart <Mono>SunshineService</Mono> in Services.
              </>
            ),
          }
        : {
            title: "Restart Sunshine",
            detail: (
              <>
                <Mono>systemctl --user restart sunshine</Mono>.
              </>
            ),
          }
  return [
    {
      title: "Look for a prompt on the computer's screen",
      detail:
        os === "macos"
          ? "Through Control: a macOS dialog asking to let Sunshine record the screen, control the computer, find devices on the local network or accept incoming connections holds it until it is answered. Allow it."
          : "Through Control: a dialog asking about Sunshine, or its firewall, holds it until it is answered.",
    },
    restart,
    {
      title: (
        <>
          Read its log at <Mono>{SUNSHINE_PAGE}</Mono> › Troubleshooting
        </>
      ),
      detail: "If it is still stuck, the last lines say what it is waiting on.",
    },
  ]
}

function TurnOnSunshine({
  computer,
  stream,
  onControl,
}: {
  computer: RdComputerView
  stream: RdStream
  onControl: () => void
}) {
  const stuck = stream.sunshine.error === "stuck"
  const where = `${stream.address ?? computer.host}:${SUNSHINE_PORT}`
  return (
    <>
      {stuck ? (
        <Heading title={`Sunshine on ${computer.name} is stuck`}>
          It is running and takes the connection on {where}, but answers nothing on it, so nothing
          can pair with it or stream from it.
        </Heading>
      ) : (
        <Heading title={`Turn on Sunshine on ${computer.name}`}>
          Sunshine is the server on the computer that encodes its screen. Nothing answered on{" "}
          {where}
          {stream.sunshine.error ? ` (${stream.sunshine.error})` : ""}.
        </Heading>
      )}
      <div className="rounded-xl border border-hairline bg-card p-4">
        <Steps steps={stuck ? unstickSteps(computer.os) : sunshineSteps(computer.os)} />
      </div>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <span className="flex items-center gap-2 text-hint text-muted-foreground">
          <StatusDot tone="notice" live className="size-1.5" />
          Watching port {SUNSHINE_PORT}
        </span>
        <Button size="sm" variant="outline" onClick={onControl}>
          <TerminalWindow className="size-3.5" />
          Do it through Control
        </Button>
      </div>
    </>
  )
}

function Pair({ computer, onPaired }: { computer: RdComputerView; onPaired: () => void }) {
  const [busy, setBusy] = useState(false)
  const pair = async () => {
    setBusy(true)
    try {
      await post<RdStream>(`/remote-desktop/computers/${computer.id}/stream/pair`)
      onPaired()
    } catch (err) {
      notify.error(`Could not pair with ${computer.name}`, err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <>
      <Heading title={`Pair with Sunshine on ${computer.name}`}>
        Sunshine answers. It streams only to what it has been paired with: pairing shows a PIN here,
        which is typed into Sunshine on {computer.name} once.
      </Heading>
      <div className="flex justify-center">
        <Button pending={busy} onClick={() => void pair()}>
          <Key className="size-4" />
          Pair
        </Button>
      </div>
    </>
  )
}

/**
 * The PIN, and where it goes. Sunshine takes it on its own page on the
 * computer, which Control reaches from here; the PIN stays up over the
 * Control screen while it is typed.
 */
function TypePin({
  computer,
  pin,
  onControl,
}: {
  computer: RdComputerView
  pin: string
  onControl: () => void
}) {
  return (
    <>
      <Heading title={`Type this PIN into Sunshine on ${computer.name}`}>
        The bridge is waiting for it. Pairing finishes here on its own.
      </Heading>
      <PinDigits pin={pin} />
      <div className="rounded-xl border border-hairline bg-card p-4">
        <Steps
          steps={[
            {
              title: (
                <>
                  Open <Mono>{SUNSHINE_PAGE}</Mono> on {computer.name}
                </>
              ),
              detail: "Sign in with Sunshine's own username and password.",
            },
            {
              title: <>Open {path("PIN")}</>,
              detail:
                "If Sunshine asks which request, choose the one named roth: that is the bridge, which pairs under Moonlight's usual name.",
            },
            { title: `Type ${pin}, name the device Just Dashboard, and send` },
          ]}
        />
      </div>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <TextShimmer className="text-hint font-medium">Waiting for Sunshine…</TextShimmer>
        <Button size="sm" variant="outline" onClick={onControl}>
          <TerminalWindow className="size-3.5" />
          Type it through Control
        </Button>
      </div>
    </>
  )
}

function PinDigits({ pin, small }: { pin: string; small?: boolean }) {
  return (
    <div
      data-slot="stream-pin"
      aria-label={`PIN ${pin.split("").join(" ")}`}
      className="flex justify-center gap-2"
    >
      {pin.split("").map((digit, index) => (
        <span
          key={index}
          aria-hidden
          className={
            small
              ? "numeric flex h-8 w-7 items-center justify-center rounded-md border border-border-strong bg-surface-sunken text-body font-semibold"
              : "numeric flex h-14 w-12 items-center justify-center rounded-lg border border-border-strong bg-surface-sunken text-2xl font-semibold"
          }
        >
          {digit}
        </span>
      ))}
    </div>
  )
}

/**
 * The PIN over the Control screen, so it can be read while it is typed into
 * Sunshine on the computer through that same screen.
 */
export function PinCard({
  computer,
  pin,
  onStream,
}: {
  computer: RdComputerView
  pin: string
  onStream: () => void
}) {
  return (
    <div
      role="status"
      className="absolute top-3 right-3 z-10 flex max-w-xs flex-col gap-2.5 rounded-xl border border-hairline bg-popover p-3 shadow-lg"
    >
      <div className="flex items-center gap-2">
        <ProductLogo id="sunshine" size="sm" />
        <p className="min-w-0 flex-1 text-body font-medium">Sunshine PIN</p>
        <TextShimmer className="text-hint">waiting…</TextShimmer>
      </div>
      <PinDigits pin={pin} small />
      <p className="text-hint text-muted-foreground">
        On {computer.name}, open <Mono>{SUNSHINE_PAGE}</Mono>, then PIN.
      </p>
      <Button size="sm" variant="outline" onClick={onStream}>
        <Play className="size-3.5" />
        Back to Stream
      </Button>
    </div>
  )
}

/**
 * The stream itself: the bridge's page, framed. Its settings live in this
 * origin's storage, where the page reads them, so they are written just
 * before it loads; a change of quality loads it again.
 */
function StreamFrame({
  computer,
  stream,
  quality,
  attempt,
  pointer,
  onFrame,
  onPlayback,
  onStats,
}: {
  computer: RdComputerView
  stream: RdStream
  quality: StreamQuality
  attempt: number
  pointer: boolean
  onFrame: (frame: HTMLIFrameElement | null) => void
  onPlayback: (playing: boolean) => void
  onStats: (stats: StreamStats | undefined) => void
}) {
  const app = desktopApp(stream.apps)
  const frame = useRef<HTMLIFrameElement>(null)
  const stopObserving = useRef<() => void>(() => {})
  const transport = stream.transport
  const key = `${stream.hostId}:${app?.id}:${quality}:${transport}:${attempt}`
  const src = app ? streamPageUrl(stream.hostId, app.id) : undefined
  useEffect(() => {
    onPlayback(false)
    onStats(undefined)
    stopObserving.current()
    return () => {
      stopObserving.current()
      onPlayback(false)
      onStats(undefined)
    }
  }, [key, onPlayback, onStats])
  useEffect(() => {
    if (!src || !frame.current) return
    let held: Record<string, unknown> = {}
    try {
      held = JSON.parse(localStorage.getItem(BRIDGE_SETTINGS_KEY) ?? "{}") ?? {}
    } catch {}
    localStorage.setItem(
      BRIDGE_SETTINGS_KEY,
      JSON.stringify(bridgeSettings(held, quality, transport)),
    )
    frame.current.src = src
  }, [key, src, quality, transport])
  // The computer draws its own pointer into the picture. This one, over it,
  // was a second pointer a round trip ahead of the first, so it is hidden
  // over the screen unless asked for; the stream's menu keeps its own.
  useEffect(() => {
    const doc = frame.current?.contentDocument
    if (doc?.readyState === "complete") pointerStyle(doc, pointer)
  }, [key, pointer])
  if (!app)
    return (
      <Centered>
        <p className="max-w-md text-body text-muted-foreground">
          Sunshine on {computer.name} offers nothing to stream. Add a Desktop app on its page under
          Applications.
        </p>
      </Centered>
    )
  return (
    <iframe
      key={key}
      ref={(node) => {
        frame.current = node
        onFrame(node)
      }}
      data-slot="remote-stream"
      title={`The screen of ${computer.name}, streamed by Sunshine`}
      allow="fullscreen; autoplay; gamepad; keyboard-map"
      // The bridge draws its page on a blue gradient of its own; framed here
      // it is a screen, so the margins round the picture go black.
      onLoad={(event) => {
        stopObserving.current()
        onPlayback(false)
        onStats(undefined)
        const doc = event.currentTarget.contentDocument
        if (!doc?.head) return
        let interrupted = false
        // The frames are counted on the first video the page draws; the
        // page is this origin, so its element can be read.
        let metered: HTMLVideoElement | null = null
        let stopMetering = () => {}
        const meter = () => {
          const video = doc.querySelector("video")
          if (video === metered) return
          stopMetering()
          metered = video
          stopMetering = video ? meterVideo(video, onStats) : () => {}
          if (!video) onStats(undefined)
        }
        const update = (event?: Event) => {
          meter()
          if (event && (event.target as HTMLElement | null)?.tagName !== "VIDEO") return
          if (
            event &&
            ["pause", "ended", "waiting", "stalled", "error", "emptied"].includes(event.type)
          )
            interrupted = true
          else if (event?.type === "playing") interrupted = false
          onPlayback(
            !interrupted &&
              [...doc.querySelectorAll("video")].some(
                (video) =>
                  !video.paused &&
                  !video.ended &&
                  !video.error &&
                  video.readyState >= 2 &&
                  video.videoWidth > 0,
              ),
          )
        }
        const events = [
          "playing",
          "timeupdate",
          "pause",
          "ended",
          "waiting",
          "stalled",
          "error",
          "emptied",
        ]
        for (const name of events) doc.addEventListener(name, update, true)
        const observer = new MutationObserver(() => update())
        observer.observe(doc.body, { childList: true, subtree: true })
        stopObserving.current = () => {
          observer.disconnect()
          stopMetering()
          for (const name of events) doc.removeEventListener(name, update, true)
        }
        update()
        const style = doc.createElement("style")
        style.textContent = "body::before { background: #000 !important; }"
        doc.head.append(style)
        pointerStyle(doc, pointer)
      }}
      className="absolute inset-0 size-full border-0 bg-black"
    />
  )
}

const POINTER_STYLE = "jd-hide-pointer"

function pointerStyle(doc: Document, pointer: boolean) {
  doc.getElementById(POINTER_STYLE)?.remove()
  if (pointer || !doc.head) return
  const style = doc.createElement("style")
  style.id = POINTER_STYLE
  style.textContent = "#root, #root *, #input { cursor: none !important; }"
  doc.head.append(style)
}
