"use client"

import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useSocket, type Envelope } from "@/hooks/use-socket"
import { containerRates } from "@/lib/container-usage"
import type { Container, ContainerStats, DockerNetwork, NetworkLivePoint } from "@/lib/types"
import { useSessionState } from "@/lib/view-state"
import type { ContainerTraffic } from "@/components/docker/network-members"

/** A container's last minute of rates, at the socket's two-second frames. */
const KEEP = 30

/**
 * Every container and what each is sending and receiving now, from the
 * containers socket: the listing as it changes, and a rate for each from two
 * frames of its counters, with its last minute kept for a trend.
 *
 * `listed` is undefined until the socket's first answer, so a host with no
 * containers is told apart from one still being read. The Networks table and
 * a network's own page both read it; it was the table's alone.
 */
export function useContainersLive() {
  const [listed, setContainers] = useState<Container[]>()
  const containers = useMemo(() => listed ?? [], [listed])
  const [traffic, setTraffic] = useState<Map<string, ContainerTraffic>>(() => new Map())
  const previous = useRef(new Map<string, ContainerStats>())
  const onMessage = useCallback((envelope: Envelope) => {
    if (envelope.type === "containers") {
      setContainers(envelope.data as Container[])
      return
    }
    if (envelope.type !== "stats") return
    const frames = envelope.data as ContainerStats[]
    // Measured here rather than in the updater: React may run that later,
    // after the next frame has replaced the one each rate is taken against.
    const before = previous.current
    previous.current = new Map(frames.map((f) => [f.id, f]))
    const measured = frames.map((frame) => ({
      frame,
      rates: containerRates(frame, before.get(frame.id)),
    }))
    setTraffic((held) => {
      const next = new Map<string, ContainerTraffic>()
      for (const { frame, rates } of measured) {
        const prior = held.get(frame.id)
        if (rates.rx === null || rates.tx === null) {
          // One frame Docker could not measure keeps the last reading; a
          // container that has lost its interfaces stops reading at all.
          if (prior && !prior.held) next.set(frame.id, { ...prior, held: true })
          continue
        }
        const point: NetworkLivePoint = {
          t: Date.parse(frame.ts) / 1000,
          rx: rates.rx,
          tx: rates.tx,
        }
        next.set(frame.id, {
          rx: rates.rx,
          tx: rates.tx,
          points: [...(prior?.points ?? []), point].slice(-KEEP),
        })
      }
      return next
    })
  }, [])
  useSocket("/docker/containers/stream", { onMessage })

  const byId = useMemo(() => new Map(containers.map((c) => [c.id, c])), [containers])
  return { listed, containers, byId, traffic }
}

/**
 * How many containers each network had the last time the list was read in
 * this tab, by name. A network's page opened from the table draws its waiting
 * picture with that many members, so what stands under the picture is where
 * it will be when the network lands.
 */
export function useMemberCounts(networks: DockerNetwork[] | undefined) {
  const [counts, setCounts] = useSessionState<Record<string, number>>("docker.networks.members", {})
  const read = useMemo(
    () =>
      networks
        ? JSON.stringify(Object.fromEntries(networks.map((n) => [n.name, n.usedBy.length])))
        : undefined,
    [networks],
  )
  const held = JSON.stringify(counts)
  useEffect(() => {
    if (read !== undefined && read !== held) setCounts(JSON.parse(read))
  }, [read, held, setCounts])
  return counts
}
