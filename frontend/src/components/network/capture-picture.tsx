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
  return (
    <WireFlow
      className={cn("animate-rise", className)}
      hubShare={1.4}
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
            eyebrow: "Interface",
            title: <span className="font-mono">{request.interface}</span>,
            hint: (
              <span className="block truncate">
                {familyWord(request.family)}
                {result
                  ? !finished
                    ? " · readback pending"
                    : result.identityVerified
                      ? " · readback matched"
                      : " · readback changed"
                  : ""}
              </span>
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
        // Three lines of words under the mark: room for them, so they never
        // run into the block under the picture.
        className: "py-12",
        eyebrow: "Filter",
        title: filter.title,
        hint: (
          <>
            <span className="block max-w-64 truncate font-mono" title={filter.from}>
              from {filter.from}
            </span>
            <span className="block max-w-64 truncate font-mono" title={filter.to}>
              to {filter.to}
            </span>
            <span className="block">first {request.snapshotLength} bytes of each packet</span>
          </>
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
                  : "Nothing yet"}
              </span>
            ),
            hint: (
              <span className="block">
                <span className="numeric">of {bytes(request.maxBytes)}</span>
                {" · "}
                <span
                  className={cn("numeric", (drops === undefined || drops > 0) && "text-warning")}
                >
                  {drops === undefined ? "kernel drops unknown" : plural(drops, "kernel drop")}
                </span>
              </span>
            ),
            line: out,
          },
        ],
      }}
    />
  )
}
