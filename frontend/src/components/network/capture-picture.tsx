"use client"

import { File, Filter } from "@/components/icons"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark } from "@/components/deploy/wire"
import { WireFlow, type WireLine } from "@/components/wire-flow"
import { bytes, plural } from "@/lib/format"
import { captureFinished, type CaptureRun } from "@/lib/network-captures"
import { captureFilter, familyWord } from "@/lib/network-runs-view"
import type { NetworkLink } from "@/lib/types"
import { cn } from "@/lib/utils"
import { interfaceMark } from "./diagnostic-marks"

/**
 * A capture as the thing it is: one interface, the filter its packets pass,
 * and the file they land in. The lines carry while it captures, rest once it
 * has ended with a file, and are dashed where nothing was kept; amber where
 * the record says the evidence is incomplete.
 */
export function CapturePicture({
  run,
  links,
  className,
}: {
  run: CaptureRun
  /** The server's devices, for the interface's own mark. */
  links?: NetworkLink[]
  className?: string
}) {
  const device = interfaceMark(run.request.interface, links)
  const DeviceGlyph = device.glyph
  const { request, result } = run
  const filter = captureFilter(request)
  const running = run.status === "running"
  const finished = captureFinished(run)
  const kept = finished && Boolean(result?.artifactAvailable)
  const doubtful = run.status === "interrupted" || run.status === "failed"
  const into: WireLine =
    run.status === "queued" || run.status === "cancelled"
      ? { state: "idle" }
      : doubtful
        ? { state: "warning" }
        : { state: "up", moving: running }
  const out: WireLine = running
    ? { state: "up", moving: true }
    : kept
      ? { state: doubtful ? "warning" : "up" }
      : { state: "down" }
  const drops = result?.kernelDropped
  const narrowed = Boolean(request.source || request.destination)
  // Wide, the filter's words hang under its mark, outside the picture's own
  // box: the room they take is kept under it, more where an address is long
  // enough to take two lines, so the block below never moves under them.
  const long = [request.source, request.destination].some((address) => (address ?? "").length > 20)
  return (
    <WireFlow
      className={cn("animate-rise", long ? "lg:pb-32" : "lg:pb-24", className)}
      start={{
        title: "Interface",
        nodes: [
          {
            id: "interface",
            name: request.interface,
            mark: (
              <WireMark tone="logo" size="md">
                {device.product ? (
                  <ProductGlyph id={device.product} />
                ) : (
                  <DeviceGlyph aria-hidden />
                )}
              </WireMark>
            ),
            // Where the columns stack, the column's own heading says it.
            eyebrow: <span className="max-lg:hidden">Interface</span>,
            title: <span className="font-mono">{request.interface}</span>,
            hint: (
              <>
                <span className="block">{familyWord(request.family)}</span>
                {/* Said only when it is news: the device was replaced or
                    changed while the capture ran. */}
                {finished && result && !result.identityVerified && (
                  <span className="block text-warning">changed while capturing</span>
                )}
              </>
            ),
            line: into,
          },
        ],
      }}
      hub={{
        mark: (
          <WireMark tone="logo" size="lg">
            <Filter aria-hidden />
          </WireMark>
        ),
        eyebrow: "Filter",
        title: filter.title,
        hint: narrowed ? (
          <>
            <span className="block font-mono break-all">from {filter.from}</span>
            <span className="block font-mono break-all">to {filter.to}</span>
          </>
        ) : (
          "any address"
        ),
      }}
      end={{
        title: "File",
        nodes: [
          {
            id: "file",
            name: "PCAP file",
            mark: (
              <WireMark tone={kept || running ? "logo" : "neutral"} size="md">
                <File aria-hidden />
              </WireMark>
            ),
            eyebrow: kept ? "PCAP" : running ? "Writing PCAP" : "No PCAP kept",
            title: (
              <span className="numeric">
                {result
                  ? `${plural(result.packets, "packet")} · ${bytes(result.bytes)}`
                  : finished
                    ? "Nothing kept"
                    : "Nothing yet"}
              </span>
            ),
            // The drops are a reading of what was captured; with nothing
            // captured the line says the limit it was held to.
            hint: result ? (
              <span
                className={cn(
                  "numeric block",
                  (drops === undefined || drops > 0) && "text-warning",
                )}
              >
                {drops === undefined ? "kernel drops unknown" : plural(drops, "kernel drop")}
              </span>
            ) : (
              <span className="numeric block">up to {bytes(request.maxBytes)}</span>
            ),
            line: out,
          },
        ],
      }}
    />
  )
}
