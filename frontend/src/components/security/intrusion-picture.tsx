"use client"

import { Bug, Globe } from "@/components/icons"
import { plural } from "@/lib/format"
import type { BlocksView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"
import type { EngineId, EngineStanding } from "@/components/security/intrusion-model"
import { useMediaQuery } from "@/hooks/use-mobile"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"

/** What each engine reads, which is what tells the three apart. */
const READS: Record<EngineId, string> = {
  fail2ban: "Reads the logs",
  crowdsec: "Logs and other servers",
  suricata: "Reads the packets",
}

/** Who holds an address, in the merged list's own names. */
const HOLDER = { fail2ban: "fail2ban", crowdsec: "CrowdSec", firewall: "firewall" } as const

const WORD_TONE = {
  running: "",
  notice: "",
  unknown: "",
  stopped: "",
  warning: "text-warning",
  danger: "text-destructive",
} as const

/**
 * What stands between the internet and this server, as one picture: who
 * knocks, the server with how many addresses it refuses right now, and each
 * engine watching it with what it holds and whether it is doing so.
 *
 * `WireFlow` has one hub, and the hub is the machine: the engines are not in
 * the packet's path (fail2ban and CrowdSec read what its services wrote down,
 * Suricata the packets beside them), so they stand after it, each on the line
 * it reads along. The line is the state, as on every wiring picture: moving
 * while the engine has something in flight, dashed for one that is stopped
 * or not installed, amber for one that works and should not be relied on, red
 * for one that decides and drops nothing.
 *
 * Under the server, what each engine and the firewall holds of the addresses
 * refused, which is why the engines' own counts do not add up to the total.
 *
 * The picture always holds three engines, so it is one height while the reads
 * are out, with all three installed and with none: an engine that is not
 * installed is its mark in a dashed ring, and opens the view that installs it.
 */
export function IntrusionPicture({
  engines,
  blocks,
  failing,
  loading,
  onOpen,
}: {
  engines: EngineStanding[]
  blocks?: BlocksView
  /** Attempts inside a jail's window right now, where fail2ban answered. */
  failing?: number
  loading: boolean
  onOpen: (engine: EngineId) => void
}) {
  const moving = engines.some((engine) => engine.line.state === "up" && engine.line.moving)
  const refused = blocks?.distinct
  const unread = blocks?.engines.filter((engine) => !engine.read).length ?? 0
  // On a phone the picture is its marks and the hub's one line: the engines
  // are named again by the views under it, and a second line under the hub
  // would arrive with the read and move them.
  const wide = useMediaQuery("(min-width: 1024px)")
  const held = blocks?.engines
    .filter((engine) => engine.read)
    .map((engine) => `${HOLDER[engine.engine]} ${engine.count}`)
    .join(" · ")

  const nodes: WireFlowNode[] = engines.map((engine) => {
    const here = engine.kind !== "absent" && engine.kind !== "closed"
    return {
      id: engine.id,
      name: `${engine.name}, ${[engine.count, engine.word].filter(Boolean).join(", ")}`,
      label: `Open ${engine.name}`,
      onPress: () => onOpen(engine.id),
      placeholder: !here,
      mark: (size) =>
        here ? (
          <WireMark tone="logo" size={size}>
            {engine.id === "suricata" ? <Bug aria-hidden /> : <ProductGlyph id={engine.id} />}
          </WireMark>
        ) : (
          <WirePlaceholder
            size={size}
            product={engine.id === "suricata" ? undefined : engine.id}
            fallback={engine.id === "suricata" ? Bug : undefined}
          />
        ),
      eyebrow: READS[engine.id],
      title: engine.name,
      hint: (
        <span className="numeric">
          {engine.count && <>{engine.count} · </>}
          <span className={cn(WORD_TONE[engine.tone])}>{engine.word}</span>
        </span>
      ),
      line: engine.line,
    }
  })

  return (
    <WireFlow
      loading={loading}
      hubShare={1}
      phone="marks"
      start={{
        title: "From outside",
        loadingRows: 1,
        nodes: [
          {
            id: "internet",
            name: "The internet",
            mark: (size) => (
              <WireMark tone="logo" size={size}>
                <Globe aria-hidden />
              </WireMark>
            ),
            eyebrow: "Who knocks",
            title: "The internet",
            hint:
              failing !== undefined && failing > 0 ? (
                <span className="numeric text-warning">
                  {plural(failing, "attempt")} inside a jail&rsquo;s window
                </span>
              ) : (
                "every address that reaches a port"
              ),
            line: { state: "up", moving },
          },
        ],
      }}
      hub={{
        mark: (size) => <WireHost size={size} />,
        eyebrow: "This server",
        title:
          refused === undefined ? (
            "Refused addresses not read"
          ) : refused === 0 ? (
            "No address refused"
          ) : (
            <span className="numeric">{plural(refused, "address", "addresses")} refused</span>
          ),
        hint: !wide ? undefined : unread > 0 ? (
          <span className="text-warning">{plural(unread, "engine")} not read</span>
        ) : (
          <span className="numeric">{held || "by any engine or the firewall"}</span>
        ),
      }}
      end={{ title: "Watched by", loadingRows: 3, nodes }}
    />
  )
}
