"use client"

import { useRef, useState } from "react"
import { bytes, percent, plural, relativeTime } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { useMemoryState } from "@/lib/view-state"
import { BarList, type BarListItem } from "@/components/bar-list"
import { Segments } from "@/components/deploy/settings/segments"
import { Metric, MetricStrip } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { EngineMark } from "@/components/database/kit"
import { redisAnalysis } from "@/components/database/redis/api"
import { bytesId, bytesLabel, globEscape } from "@/components/database/redis/bytes"
import { DbPicker } from "@/components/database/redis/db-picker"
import { KindMark, kindLabel, kindOf, sizeOf } from "@/components/database/redis/kinds"
import { ReadError } from "@/components/database/redis/read-error"
import { ttlWord } from "@/components/database/redis/ttl"
import type { RedisAnalysis, RedisAnalysisGroup } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

const SAMPLES = [
  { value: "1000", label: "1,000" },
  { value: "10000", label: "10,000" },
  { value: "50000", label: "50,000" },
]

const EXPIRY: Record<string, string> = {
  none: "Never expires",
  hour: "Within the hour",
  day: "Within the day",
  week: "Within the week",
  later: "Later than a week",
}

/**
 * What the memory is spent on: by type, by namespace, by key and by how soon
 * it will be given back.
 *
 * It is a measurement the server pays for — up to fifty thousand keys, four
 * commands each — so it is made when asked and never on a timer. The server
 * measures a sample and scales it to the database; every figure here says
 * which it is, and the head says how many keys were measured of how many.
 * The last analysis of each database is kept for the tab, so looking at the
 * slow log and coming back does not measure again.
 */
export function MemoryView({ redis }: { redis: Redis }) {
  const { id, target, db, server, engine } = redis
  const [sample, setSample] = useState("10000")
  const [results, setResults] = useMemoryState<Record<string, RedisAnalysis & { at: string }>>(
    `databases.${id}.redis.analysis`,
    {},
  )
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<Error>()
  const inFlight = useRef<AbortController | null>(null)
  const slot = String(db ?? "default")
  const result = results[slot]

  const analyse = async () => {
    inFlight.current?.abort()
    const controller = new AbortController()
    inFlight.current = controller
    setRunning(true)
    setError(undefined)
    try {
      const answer = await redisAnalysis(target, Number(sample), controller.signal)
      setResults((held) => ({ ...held, [slot]: { ...answer, at: new Date().toISOString() } }))
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      if (inFlight.current === controller) setRunning(false)
    }
  }

  const controls = (
    <div className="flex flex-wrap items-center gap-2">
      <DbPicker
        server={server.data}
        db={db}
        onChange={(next) => {
          inFlight.current?.abort()
          redis.setDb(next)
        }}
      />
      <Segments label="Keys to measure" value={sample} onChange={setSample} options={SAMPLES} />
      <Button size="sm" pending={running} onClick={() => void analyse()}>
        {result ? "Analyse again" : "Analyse memory"}
      </Button>
    </div>
  )

  if (!result) {
    return (
      <div className="space-y-4">
        {error && <ReadError error={error} onRetry={() => void analyse()} />}
        <EmptyState
          mark={<EngineMark engine={engine} />}
          title={running ? "Measuring the keys" : `db${db ?? ""} has not been analysed`}
          description={
            running ? (
              <TextShimmer>
                Reading a sample of the keys, their sizes and their expiries…
              </TextShimmer>
            ) : (
              "An analysis measures a sample of the keys — type, namespace, size, expiry — and scales it to the database. It takes up to 25 seconds and is made only when you ask."
            )
          }
          action={controls}
        />
      </div>
    )
  }

  const noMemory = result.unavailable?.memory
  // Where the server cannot say what a key costs, a group's weight is its keys.
  const weight = (group: RedisAnalysisGroup) =>
    noMemory ? group.estimatedKeys : group.estimatedMemory
  const figure = (group: RedisAnalysisGroup) =>
    noMemory ? group.estimatedKeys.toLocaleString() : bytes(group.estimatedMemory)
  const whole = result.types.reduce((sum, group) => sum + weight(group), 0)
  const about = result.complete ? "" : "about "

  const ranked = (groups: RedisAnalysisGroup[]) => Math.max(...groups.map(weight), 1)
  const namespaces: BarListItem[] = result.namespaces.slice(0, 20).map((group) => {
    const name = bytesLabel(group.name)
    return {
      key: bytesId(group.name),
      label: name || "(no namespace)",
      mark: (
        <span
          className="size-1.5 rounded-full"
          style={{ backgroundColor: hueFor(name.toLowerCase(), LANES) }}
        />
      ),
      value: figure(group),
      share: weight(group) / ranked(result.namespaces),
      hint: `${about}${plural(group.estimatedKeys, "key")}`,
      ...(name && typeof group.name === "string"
        ? {
            title: `Open the keys of ${name}`,
            onClick: () =>
              redis.goto("data", {
                db: String(result.db),
                pattern: `${globEscape(name)}${result.delimiter}*`,
              }),
          }
        : {}),
    }
  })
  const top: BarListItem[] = result.topKeys.slice(0, 20).map((entry) => {
    const name = bytesLabel(entry.key)
    return {
      key: bytesId(entry.key),
      label: name,
      mark: <KindMark type={entry.type} className="size-3" />,
      value: noMemory ? entry.size.toLocaleString() : bytes(entry.memory),
      share:
        (noMemory ? entry.size : entry.memory) /
        Math.max(noMemory ? result.topKeys[0].size : result.topKeys[0].memory, 1),
      hint: [
        kindLabel(entry.type).toLowerCase(),
        sizeOf(entry.type, entry.size),
        entry.encoding,
        entry.ttl >= 0 ? `expires in ${ttlWord(entry.ttl)}` : "",
      ]
        .filter(Boolean)
        .join(" · "),
      ...(typeof entry.key === "string"
        ? {
            title: `Open ${name}`,
            onClick: () => redis.goto("data", { db: String(result.db), key: name }),
          }
        : {}),
    }
  })
  const expiry: BarListItem[] = result.expiry.map((group) => {
    const name = bytesLabel(group.name)
    return {
      key: name,
      label: EXPIRY[name] ?? name,
      mono: false,
      value: figure(group),
      share: weight(group) / ranked(result.expiry),
      hint: `${about}${plural(group.estimatedKeys, "key")}`,
    }
  })
  const encodings: BarListItem[] = result.encodings.slice(0, 12).map((group) => {
    const [type, encoding] = bytesLabel(group.name).split(":")
    return {
      key: bytesLabel(group.name),
      label: encoding ?? type,
      mark: <KindMark type={type} className="size-3" />,
      value: figure(group),
      share: weight(group) / ranked(result.encodings),
      hint: `${kindLabel(type).toLowerCase()} · ${about}${plural(group.estimatedKeys, "key")}`,
    }
  })
  const kept = result.expiry.find((group) => bytesLabel(group.name) === "none")

  return (
    <div data-slot="redis-analysis" className="animate-rise space-y-8">
      <div className="flex flex-wrap items-end justify-between gap-x-6 gap-y-3">
        <MetricStrip>
          <Metric
            label="Measured"
            value={`${result.sampled.toLocaleString()} of ${result.total.toLocaleString()} keys`}
            hint={
              result.complete
                ? "every key: nothing is an estimate"
                : `scaled ×${result.scale.toFixed(2)} to the database${result.timedOut ? " · stopped at 25 s" : ""}`
            }
          />
          <Metric
            label={`In db${result.db}`}
            value={noMemory ? "—" : `${about}${bytes(result.estimatedMemory)}`}
            hint={noMemory ?? `the server uses ${bytes(result.usedMemory)} in all`}
          />
          {kept && !noMemory && (
            <Metric
              label="Never given back"
              value={`${about}${bytes(kept.estimatedMemory)}`}
              hint={`${plural(kept.estimatedKeys, "key")} with no expiry`}
            />
          )}
          <Metric
            label="Taken"
            value={relativeTime(result.at)}
            hint={`in ${result.elapsedMs} ms`}
          />
        </MetricStrip>
        {controls}
      </div>
      {error && <ReadError error={error} onRetry={() => void analyse()} />}

      <Panel plain>
        <PanelHeader title="By type" />
        <PanelBody className="space-y-2.5">
          {/* One track, a width per type, each in its type's hue: the same
              legend the key browser draws a key's mark in. */}
          <div
            role="img"
            aria-label={result.types
              .map((group) => `${kindLabel(bytesLabel(group.name))} ${figure(group)}`)
              .join(", ")}
            className="flex h-2 w-full overflow-hidden rounded-full bg-meter-track"
          >
            {result.types.map((group) => (
              <span
                key={bytesId(group.name)}
                className="h-full transition-[width] first:rounded-l-full last:rounded-r-full"
                style={{
                  width: `${whole > 0 ? (weight(group) / whole) * 100 : 0}%`,
                  backgroundColor: kindOf(bytesLabel(group.name)).color,
                }}
              />
            ))}
          </div>
          <ul className="flex flex-wrap gap-x-5 gap-y-1.5">
            {result.types.map((group) => {
              const type = bytesLabel(group.name)
              return (
                <li key={type} className="flex min-w-0 items-center gap-1.5 text-hint">
                  <KindMark type={type} className="size-3" />
                  <span className="text-muted-foreground">{kindLabel(type)}</span>
                  <span className="numeric font-medium">{figure(group)}</span>
                  <span className="numeric text-muted-foreground">
                    {whole > 0 ? percent((weight(group) / whole) * 100) : "—"} · {about}
                    {plural(group.estimatedKeys, "key")}
                  </span>
                </li>
              )
            })}
          </ul>
        </PanelBody>
      </Panel>

      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        <Panel plain>
          <PanelHeader
            title="By namespace"
            actions={
              result.namespacesOmitted ? (
                <span className="text-hint text-muted-foreground">
                  {result.namespacesOmitted.toLocaleString()} smaller ones not listed
                </span>
              ) : undefined
            }
          />
          <PanelBody className="group-data-[plain]/panel:-ml-2">
            <BarList items={namespaces} emptyLabel="No key was measured." />
          </PanelBody>
        </Panel>
        <Panel plain>
          <PanelHeader title="Largest keys" />
          <PanelBody className="group-data-[plain]/panel:-ml-2">
            <BarList items={top} emptyLabel="No key was measured." />
          </PanelBody>
        </Panel>
        <Panel plain>
          <PanelHeader title="When it is given back" />
          <PanelBody className="group-data-[plain]/panel:-ml-2">
            <BarList items={expiry} />
          </PanelBody>
        </Panel>
        {encodings.length > 0 && (
          <Panel plain>
            <PanelHeader title="How it is stored" />
            <PanelBody className="group-data-[plain]/panel:-ml-2">
              <BarList items={encodings} />
            </PanelBody>
          </Panel>
        )}
      </div>
    </div>
  )
}
