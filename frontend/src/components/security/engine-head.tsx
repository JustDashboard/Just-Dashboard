import { Bug } from "@/components/icons"
import { HostIdentity } from "@/components/metrics/host-identity"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { WireMark } from "@/components/deploy/wire"
import type { EngineId } from "@/components/security/intrusion-model"

/**
 * The line an engine's view opens on: the engine as its own mark, its name
 * with its state after it, a line of facts, and its one command at the far
 * end. The same line the page opens on, a step in, so the three views read as
 * three of one kind. Suricata has no mark in the logo collection and keeps a
 * glyph on the tile, never a guessed logo.
 */
export function EngineHead({
  id,
  name,
  title,
  state,
  facts,
  command,
}: {
  id: EngineId
  name: string
  /** The name with something after it (a version), where it is more than the name. */
  title?: React.ReactNode
  state: React.ReactNode
  facts: React.ReactNode
  command?: React.ReactNode
}) {
  return (
    <HostIdentity
      label={`About ${name}`}
      slot="engine-head"
      mark={id === "suricata" ? undefined : id}
      fallback={Bug}
      title={title ?? name}
      state={state}
      facts={facts}
      command={command}
    />
  )
}

/**
 * An engine's view while its read is out: its line, the lines its readings
 * will stand on, and grey rows, so the view has its shape before it has its
 * figures.
 */
export function EngineWaiting({
  id,
  name,
  lines,
}: {
  id: EngineId
  name: string
  /** How many reading lines the view opens with. */
  lines: number
}) {
  return (
    <>
      <EngineHead
        id={id}
        name={name}
        state={<Status tone="unknown" label="reading" />}
        facts={<span>&nbsp;</span>}
      />
      {lines > 0 && (
        <ReadingLines aria-hidden>
          {Array.from({ length: lines }, (_, index) => (
            <ReadingLine
              key={index}
              mark={
                <WireMark size="md">
                  <span />
                </WireMark>
              }
              name={" "}
            >
              <NoReading>No reading yet</NoReading>
            </ReadingLine>
          ))}
        </ReadingLines>
      )}
      <LoadingRows rows={4} hold />
    </>
  )
}
