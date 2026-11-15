"use client"

import { useRef } from "react"
import { FolderOpen, Logs } from "@/components/icons"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { EngineGlyph } from "@/components/database/kit"
import { useDatabase } from "@/components/database/shell/database-context"

/** Where an archive can be kept, as the setup offers them. */
const DESTINATIONS = [
  { key: "local", title: "A directory", hint: "on this server", fallback: FolderOpen },
  { key: "s3", title: "S3", hint: "a bucket", product: "aws" },
  { key: "b2", title: "Backblaze B2", hint: "a bucket", product: "backblaze" },
] as const

/**
 * The path an archive takes, drawn before there is one: the server, the
 * write-ahead log it writes, and the places that log could be kept.
 *
 * The first line is solid, because the server writes its log whether or not
 * anybody keeps it. The lines on from the log are dashed and its destinations
 * are rings, because that half is what is missing — which is the whole of
 * what this state has to say, and was a paragraph.
 */
export function ArchivePathPicture({ container: name }: { container: string }) {
  const { conn, engine } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const logMark = useRef<HTMLDivElement>(null)
  const local = useRef<HTMLDivElement>(null)
  const s3 = useRef<HTMLDivElement>(null)
  const b2 = useRef<HTMLDivElement>(null)
  const marks = { local, s3, b2 }

  return (
    <SettingPicture
      label={`Where the log of ${name || conn.name} would be archived`}
      containerRef={container}
      className="py-2"
      lines={
        <>
          <AnimatedBeam containerRef={container} fromRef={serverMark} toRef={logMark} still />
          {DESTINATIONS.map((place) => (
            <AnimatedBeam
              key={place.key}
              containerRef={container}
              fromRef={logMark}
              toRef={marks[place.key]}
              still
              dashed
            />
          ))}
        </>
      }
      start={[
        <WireNode
          key="server"
          nodeRef={serverMark}
          align="end"
          mark={
            <WireMark size="md" tone="logo">
              <EngineGlyph engine={engine} />
            </WireMark>
          }
          eyebrow={engine.label}
          title={<span className="font-mono text-xs wrap-anywhere">{name || conn.name}</span>}
          hint="writes its log"
        />,
      ]}
      middle={
        <WireNode
          nodeRef={logMark}
          align="center"
          mark={
            <WireMark size="md" tone="neutral">
              <Logs />
            </WireMark>
          }
          eyebrow="Write-ahead log"
          title="Not archived"
          hint="kept only until it is recycled"
        />
      }
      end={
        <ul className="flex flex-col gap-5 lg:gap-3">
          {DESTINATIONS.map((place) => (
            <li key={place.key} className="min-w-0">
              <WireNode
                nodeRef={marks[place.key]}
                align="start"
                mark={
                  // Narrow, the marks stand in one column: a smaller ring is
                  // centred in the width of the marks above it, so the line
                  // down them meets its middle.
                  <span className="flex w-11 justify-center lg:w-auto">
                    <WirePlaceholder
                      size="sm"
                      product={"product" in place ? place.product : undefined}
                      fallback={"fallback" in place ? place.fallback : undefined}
                    />
                  </span>
                }
                title={<span className="text-muted-foreground">{place.title}</span>}
                hint={place.hint}
              />
            </li>
          ))}
        </ul>
      }
    />
  )
}
