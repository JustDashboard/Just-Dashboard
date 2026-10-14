"use client"

import { useRef, useState } from "react"
import {
  Clipboard,
  CloudUpload,
  Copy,
  DesktopDevice,
  Fullscreen,
  Servers,
} from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { KEYCAP } from "@/components/command-search/keycaps"
import { ProductGlyph } from "@/components/product-logo"
import { ComputerMark } from "@/components/remote-desktop/marks"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { del } from "@/lib/api"
import { bytes } from "@/lib/format"
import {
  combosFor,
  latency,
  osLabel,
  osProduct,
  protocolLabel,
  STREAM_QUALITIES,
  type KeyCombo,
  type RdComputerView,
  type RdStream,
  type SessionMode,
  type StreamQuality,
} from "@/lib/remote-desktop"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"

export type Transfer = {
  key: string
  name: string
  size: number
  sent: number
  direction: "up" | "down"
  state: "moving" | "done" | "failed"
}

// Keyboard Lock is Chrome's and Edge's; other browsers keep their own keys
// even in fullscreen.
const keyboardLock = Boolean(
  (navigator as Navigator & { keyboard?: { lock?: unknown } }).keyboard?.lock,
)

/**
 * The session's own column: which computer, how its screen is drawn, and
 * what that way of drawing it can be asked for.
 *
 * It reads top down the way a session is used. The computer and how far it
 * is, as a mark and a coloured round trip rather than a list of facts; the
 * two ways to draw the screen as their own logos, the one in use lit; then
 * the route the picture takes and that mode's controls, each a titled block
 * with its controls first and one line of why, not paragraphs.
 */
export function SessionPanel({
  computer,
  mode,
  live,
  latencyMs,
  onMode,
  children,
}: {
  computer: RdComputerView
  mode: SessionMode
  live: boolean
  latencyMs?: number
  onMode: (mode: SessionMode) => void
  children: React.ReactNode
}) {
  return (
    <aside
      aria-label="Session"
      data-slot="session-panel"
      className="flex w-full shrink-0 flex-col overflow-y-auto border-hairline xl:w-80 xl:border-l"
    >
      <div className="flex items-center gap-3 border-b border-hairline px-4 py-3.5">
        <ComputerMark os={computer.os} size="sm" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-title leading-tight font-semibold">{computer.name}</p>
          <p className="truncate text-hint text-muted-foreground">
            {osLabel(computer.os)} ·{" "}
            <span className="font-mono">{computer.peer?.address || computer.host}</span>
          </p>
        </div>
        <RoundTrip ms={latencyMs} live={live} />
      </div>

      <div
        role="radiogroup"
        aria-label="How the screen is drawn"
        className="grid grid-cols-2 gap-2 border-b border-hairline p-4"
      >
        <ModeCard
          selected={mode === "control"}
          onSelect={() => onMode("control")}
          mark={<ProductGlyph id={osProduct(computer.os)} className="size-5" />}
          title={protocolLabel(computer.protocol, computer.os)}
          detail="Nothing to install"
        />
        <ModeCard
          selected={mode === "stream"}
          onSelect={() => onMode("stream")}
          mark={<ProductGlyph id="sunshine" className="size-5" />}
          title="Stream"
          detail="60 fps from Sunshine"
        />
      </div>

      {children}
    </aside>
  )
}

/** The round trip, coloured by how it will feel: green is not felt, amber is, red is in the way. */
function RoundTrip({ ms, live }: { ms?: number; live: boolean }) {
  const tone = !live || ms === undefined ? "idle" : ms < 60 ? "good" : ms < 120 ? "fair" : "slow"
  return (
    <span
      data-tone={tone}
      className={cn(
        "numeric flex shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-hint font-semibold",
        tone === "good" && "bg-wash-success text-success",
        tone === "fair" && "bg-wash-warning text-warning",
        tone === "slow" && "bg-wash-danger text-destructive",
        tone === "idle" && "bg-control text-muted-foreground",
      )}
    >
      <StatusDot
        tone={
          tone === "idle"
            ? "stopped"
            : tone === "good"
              ? "running"
              : tone === "fair"
                ? "warning"
                : "danger"
        }
        live={live}
        className="size-1.5"
      />
      {live ? (latency(ms) ?? "live") : "idle"}
    </span>
  )
}

function ModeCard({
  selected,
  onSelect,
  mark,
  title,
  detail,
}: {
  selected: boolean
  onSelect: () => void
  mark: React.ReactNode
  title: string
  detail: string
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex min-w-0 flex-col items-start gap-2.5 rounded-xl border p-3 text-left focus-ring transition-colors",
        selected
          ? "border-rule-brand bg-wash-brand"
          : "border-hairline hover:border-border-strong hover:bg-row-hover",
      )}
    >
      <span className="flex w-full items-center justify-between">
        <span className="flex size-8 items-center justify-center rounded-lg border border-hairline bg-background">
          {mark}
        </span>
        <span
          aria-hidden
          className={cn(
            "size-2 rounded-full",
            selected ? "bg-brand shadow-[0_0_0_3px_var(--wash-brand)]" : "bg-border",
          )}
        />
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="truncate text-body font-semibold">{title}</span>
        <span className="truncate text-hint text-muted-foreground">{detail}</span>
      </span>
    </button>
  )
}

/** One block of the column: a plain title, an optional aside, and its controls. */
export function PanelSection({
  title,
  aside,
  children,
}: {
  title: string
  aside?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <section aria-label={title} className="flex flex-col gap-3 border-b border-hairline px-4 py-4">
      <div className="flex min-h-5 items-center justify-between gap-2">
        <h2 className="text-body font-semibold">{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  )
}

/**
 * The way the picture comes, as the three places it passes through: this
 * browser, the server, and the computer, each as its own mark.
 */
export function Route({
  server,
  serverMark,
  computer,
  computerMark,
  note,
}: {
  server: string
  serverMark: React.ReactNode
  computer: string
  computerMark: React.ReactNode
  note: string
}) {
  const stops = [
    { key: "browser", mark: <DesktopDevice className="size-3.5" />, label: "This browser" },
    { key: "server", mark: serverMark, label: server },
    { key: "computer", mark: computerMark, label: computer },
  ]
  return (
    <div className="flex flex-col gap-2.5">
      <ol className="flex items-center gap-1.5">
        {stops.map((stop, index) => (
          <li key={stop.key} className="flex min-w-0 flex-1 items-center gap-1.5 last:flex-none">
            <span
              title={stop.label}
              className="flex size-7 shrink-0 items-center justify-center rounded-lg border border-hairline bg-background text-muted-foreground"
            >
              {stop.mark}
            </span>
            {index < stops.length - 1 && (
              <span aria-hidden className="h-px min-w-3 flex-1 bg-rule-brand" />
            )}
            <span className="sr-only">{stop.label}</span>
          </li>
        ))}
      </ol>
      <p className="text-hint text-muted-foreground">{note}</p>
    </div>
  )
}

export function routeNote(computer: RdComputerView) {
  if (!computer.peer) return "Over this server's network"
  if (computer.peer.direct) return "Direct over Tailscale"
  if (computer.peer.relay) return `Relayed by Tailscale via ${computer.peer.relay}`
  return "Over Tailscale"
}

export function ServerMark() {
  return <Servers className="size-3.5" />
}

/**
 * Where the keys go, drawn as the keys themselves: with the screen focused a
 * page gets every key it can, and the system's own reach the computer only
 * in fullscreen, which the button opens with the keyboard locked.
 */
export function KeyboardSection({
  name,
  onFullscreen,
}: {
  name: string
  onFullscreen: () => void
}) {
  return (
    <PanelSection
      title="Keyboard"
      aside={
        keyboardLock && (
          <Button size="xs" variant="outline" onClick={onFullscreen}>
            <Fullscreen className="size-3" />
            Fullscreen
          </Button>
        )
      }
    >
      <div className="flex flex-wrap items-center gap-1.5">
        {["Alt", "Tab", "⊞ Win", "Ctrl", "W"].map((key) => (
          <kbd key={key} className={cn(KEYCAP, "h-6 px-1.5 text-hint text-foreground")}>
            {key}
          </kbd>
        ))}
      </div>
      <p className="text-hint text-muted-foreground">
        {keyboardLock
          ? `Stay with this computer until fullscreen, which sends every key to ${name}. Hold Esc to leave.`
          : `This browser keeps these even in fullscreen; Chrome and Edge send them to ${name}.`}
      </p>
    </PanelSection>
  )
}

/** Stream's controls: its route, how sharp it is, the pointer, the keys and the pairing. */
export function StreamSections({
  computer,
  stream,
  quality,
  onQuality,
  pointer,
  onPointer,
  onFullscreen,
  onUnpaired,
}: {
  computer: RdComputerView
  stream: RdStream | undefined
  quality: StreamQuality
  onQuality: (quality: StreamQuality) => void
  pointer: boolean
  onPointer: (pointer: boolean) => void
  onFullscreen: () => void
  onUnpaired: () => void
}) {
  const [busy, setBusy] = useState(false)
  const webrtc = stream?.transport === "webrtc"
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
    <>
      <PanelSection
        title="Route"
        aside={
          <span className="flex items-center gap-1.5 font-mono text-hint text-muted-foreground">
            H.264
            <span
              className={cn(
                "rounded-sm px-1.5 py-0.5 font-sans text-micro font-semibold",
                webrtc ? "bg-wash-success text-success" : "bg-control text-muted-foreground",
              )}
            >
              {webrtc ? "WebRTC" : "WebSocket"}
            </span>
          </span>
        }
      >
        <Route
          server="Moonlight Web on this server"
          serverMark={<ServerMark />}
          computer={`Sunshine on ${computer.name}`}
          computerMark={<ProductGlyph id="sunshine" className="size-4" />}
          note={
            webrtc
              ? `${routeNote(computer)}; to this browser over UDP on the tailnet`
              : `${routeNote(computer)}; to this browser over the dashboard's own connection`
          }
        />
      </PanelSection>

      <PanelSection title="Quality">
        <div role="radiogroup" aria-label="Stream quality" className="grid grid-cols-2 gap-1.5">
          {STREAM_QUALITIES.map((preset) => {
            const selected = preset.key === quality
            return (
              <button
                key={preset.key}
                type="button"
                role="radio"
                aria-checked={selected}
                aria-label={preset.label}
                onClick={() => onQuality(preset.key)}
                className={cn(
                  "flex min-w-0 flex-col items-start gap-0.5 rounded-lg border px-2.5 py-2 text-left focus-ring transition-colors",
                  selected
                    ? "border-rule-brand bg-wash-brand"
                    : "border-hairline hover:border-border-strong hover:bg-row-hover",
                )}
              >
                <span className="text-body font-semibold">{preset.label}</span>
                <span className="numeric text-hint text-muted-foreground">
                  {preset.size} · {preset.fps}
                </span>
                <span className="numeric text-micro text-muted-foreground">
                  {preset.bitrate / 1000} Mb/s
                </span>
              </button>
            )
          })}
        </div>
      </PanelSection>

      <PanelSection title="Pointer">
        <label className="flex items-center justify-between gap-3">
          <span className="flex min-w-0 flex-col gap-0.5">
            <span className="text-body">Show my pointer too</span>
            <span className="text-hint text-muted-foreground">
              {computer.name} draws its own, a round trip behind.
            </span>
          </span>
          <Switch
            checked={pointer}
            onCheckedChange={onPointer}
            aria-label="Show this computer's pointer too"
          />
        </label>
      </PanelSection>

      <KeyboardSection name={computer.name} onFullscreen={onFullscreen} />

      {stream?.hostId ? (
        <div className="flex items-center gap-2 px-4 py-3.5 text-hint">
          <StatusDot tone={stream.paired ? "running" : "warning"} className="size-1.5" />
          <span className="min-w-0 flex-1 truncate text-muted-foreground">
            {stream.paired ? "Paired with Sunshine" : "Not paired yet"}
          </span>
          <Button size="xs" variant="ghost" pending={busy} onClick={() => void unpair()}>
            Unpair
          </Button>
        </div>
      ) : null}
    </>
  )
}

/** Control's controls: its route, the clipboard both ways, files, and keys a browser keeps. */
export function ControlSections({
  computer,
  connected,
  remoteClip,
  localClip,
  onLocalClip,
  onPaste,
  drive,
  transfers,
  onUpload,
  onKeys,
  onFullscreen,
}: {
  computer: RdComputerView
  connected: boolean
  remoteClip: string
  localClip: string
  onLocalClip: (text: string) => void
  onPaste: () => void
  drive: boolean
  transfers: Transfer[]
  onUpload: (files: FileList) => void
  onKeys: (combo: KeyCombo) => void
  onFullscreen: () => void
}) {
  const picker = useRef<HTMLInputElement>(null)
  return (
    <>
      <PanelSection
        title="Route"
        aside={
          <span className="font-mono text-hint text-muted-foreground">
            {computer.protocol.toUpperCase()}
          </span>
        }
      >
        <Route
          server="guacd on this server"
          serverMark={<ProductGlyph id="guacamole" className="size-4" />}
          computer={`${protocolLabel(computer.protocol, computer.os)} on ${computer.name}`}
          computerMark={<ProductGlyph id={osProduct(computer.os)} className="size-4" />}
          note={`${routeNote(computer)} · ${osLabel(computer.os)}${computer.username ? `, signed in as ${computer.username}` : ""}`}
        />
      </PanelSection>

      <PanelSection title="Clipboard">
        <Textarea
          id="remote-clip"
          aria-label={`Text to paste on ${computer.name}`}
          rows={3}
          value={localClip}
          onChange={(event) => onLocalClip(event.target.value)}
          placeholder={`Text to paste on ${computer.name}`}
          className="resize-none text-body"
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
          <div className="flex min-w-0 items-start gap-2 rounded-lg border border-rule-brand bg-wash-brand p-2.5">
            <div className="min-w-0 flex-1 space-y-0.5">
              <p className="text-micro font-semibold text-brand">Copied on {computer.name}</p>
              <p className="line-clamp-3 font-mono text-hint break-words">{remoteClip}</p>
            </div>
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
      </PanelSection>

      {computer.protocol === "rdp" && (
        <PanelSection title="Files">
          <button
            type="button"
            disabled={!drive}
            onClick={() => picker.current?.click()}
            className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-border-strong px-3 py-4 text-center text-hint text-muted-foreground focus-ring transition-colors hover:text-foreground disabled:opacity-50"
          >
            <CloudUpload className="size-5 text-brand" />
            {drive
              ? `Drop files on the screen, or choose them, for the Just Dashboard drive on ${computer.name}`
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
            A file put in the drive&apos;s Download folder arrives here.
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
        </PanelSection>
      )}

      <PanelSection title="Send keys">
        <div className="grid grid-cols-2 gap-1.5">
          {combosFor(computer.os).map((combo) => (
            <Button
              key={combo.key}
              size="sm"
              variant="outline"
              className="justify-start font-mono text-hint"
              disabled={!connected}
              onClick={() => onKeys(combo)}
            >
              {combo.label}
            </Button>
          ))}
        </div>
      </PanelSection>

      <KeyboardSection name={computer.name} onFullscreen={onFullscreen} />
    </>
  )
}
