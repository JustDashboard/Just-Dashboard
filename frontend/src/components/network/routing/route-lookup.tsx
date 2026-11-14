"use client"

import { useEffect, useState } from "react"
import { get } from "@/lib/api"
import type { NetworkPath, NetworkRouting } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { Cross, SettingsSliders } from "@/components/icons"
import { Field, InfoTip } from "@/components/form"
import { SearchInput } from "@/components/page"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { addressFamily, type RouteFamily } from "./decision-reading"
import { askable, parseTarget, replyHighlight } from "./route-reading"

export type RouteLookupResult = NetworkPath & { family: RouteFamily; table?: number }

/** What the reader asks about: a destination, and the packet's source and mark when they matter. */
export type RouteQuestion = { target: string; source: string; mark: string }

export type RouteLookup =
  | { state: "idle" }
  | { state: "asking"; target: string }
  | { state: "answer"; target: string; result: RouteLookupResult }
  | { state: "error"; target: string; error: Error; retry: () => void }

/** A value once it has stopped changing, so a question is asked when the typing pauses. */
function useSettled<T>(value: T, ms: number) {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value), ms)
    return () => window.clearTimeout(timer)
  }, [value, ms])
  return settled
}

/**
 * The kernel's own answer for the question in the picture's head: `ip route
 * get`, which sends nothing and proves no connection. It is asked once the
 * typing pauses on one literal address and again every fifteen seconds, since
 * a route added meanwhile changes it; a network is not a packet's
 * destination, so it only narrows the table.
 */
export function useRouteLookup({ target, source, mark }: RouteQuestion): RouteLookup {
  const key = [target.trim(), source.trim(), mark.trim()].join("\n")
  const settled = useSettled(key, 350)
  const [address, from, fwmark] = settled.split("\n")
  const read = usePoll<RouteLookupResult>(
    (signal) =>
      get(
        "/network/routing/lookup",
        { target: address, ...(from ? { source: from } : {}), ...(fwmark ? { mark: fwmark } : {}) },
        signal,
      ),
    15_000,
    [settled],
    { enabled: askable(address, from) },
  )
  if (!askable(target, source)) return { state: "idle" }
  const asked = target.trim()
  if (settled !== key || (!read.data && !read.error)) return { state: "asking", target: asked }
  if (read.data) return { state: "answer", target: asked, result: read.data }
  return { state: "error", target: asked, error: read.error!, retry: read.refresh }
}

/**
 * The picture's question: where does a packet to this address go. One field
 * where the page had two — "Ask the kernel" under the picture and a target
 * filter over the tables — because they asked the same thing and neither
 * moved the picture. What is typed narrows the routes to those covering it,
 * and one address is also put to the kernel, whose table lights in the
 * picture. The packet's source and mark sit behind the field: they are for
 * the question a policy rule raises, which is the rarer one.
 */
export function TargetField({
  question,
  onChange,
  className,
}: {
  question: RouteQuestion
  onChange: (next: RouteQuestion) => void
  className?: string
}) {
  const set = question.source.trim() || question.mark.trim()
  return (
    <div className={cn("flex min-w-0 items-center gap-1.5", className)}>
      <SearchInput
        aria-label="Target"
        value={question.target}
        onChange={(event) => onChange({ ...question, target: event.target.value })}
        placeholder="Where does a packet to… go"
        className="font-mono placeholder:font-sans"
        containerClassName="sm:w-80"
        spellCheck={false}
        autoCapitalize="none"
        autoCorrect="off"
        trailing={
          question.target && (
            <Button
              type="button"
              size="icon-xs"
              variant="ghost"
              aria-label="Clear the target"
              onClick={() => onChange({ ...question, target: "" })}
            >
              <Cross aria-hidden />
            </Button>
          )
        }
      />
      <Popover>
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="outline"
            size="icon-sm"
            aria-label="Source and mark"
            className={cn("max-sm:size-10", set && "border-border-strong bg-accent")}
          >
            <SettingsSliders aria-hidden />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" className="space-y-3">
          <Field label="Source address" htmlFor="routing-lookup-source">
            <Input
              id="routing-lookup-source"
              value={question.source}
              onChange={(event) => onChange({ ...question, source: event.target.value })}
              placeholder="Whichever the kernel picks"
              className="font-mono text-xs"
              spellCheck={false}
            />
          </Field>
          <Field label="Packet mark" htmlFor="routing-lookup-mark">
            <Input
              id="routing-lookup-mark"
              value={question.mark}
              onChange={(event) => onChange({ ...question, mark: event.target.value })}
              placeholder="0x80000"
              className="font-mono text-xs"
              spellCheck={false}
            />
          </Field>
        </PopoverContent>
      </Popover>
    </div>
  )
}

const mono = "font-mono text-foreground break-all"

// The kernel's own tables, for an answer from one the page does not list.
const KERNEL_TABLES: Record<number, string> = { 253: "default", 254: "main", 255: "local" }

/**
 * The answer, on the one line under the picture's head: the kernel's for the
 * address asked about, and with nothing asked the path this browser's
 * replies take, which is what the picture lights then. It holds its line in
 * every state, so asking moves nothing under it.
 */
export function AnswerLine({
  routing,
  question,
  lookup,
  family,
}: {
  routing: NetworkRouting
  question: RouteQuestion
  lookup: RouteLookup
  family: RouteFamily
}) {
  const typed = question.target.trim()
  const tableName = (id: number) =>
    routing.tables.find((t) => t.id === id)?.name ?? KERNEL_TABLES[id] ?? `${id}`
  return (
    <p
      aria-live="polite"
      data-slot="route-answer"
      className="flex min-h-10 min-w-0 flex-wrap content-start items-center gap-x-1.5 gap-y-1 text-xs text-muted-foreground sm:min-h-5"
    >
      {!typed ? (
        <Replies routing={routing} family={family} />
      ) : parseTarget(typed) === null ? (
        <span className="text-destructive">Use an IPv4 or IPv6 address or network.</span>
      ) : lookup.state === "idle" ? (
        !typed.includes("/") ? (
          <span className="text-destructive">The source is not an address.</span>
        ) : (
          <>
            <span>Routes covering</span> <span className={mono}>{typed}</span>{" "}
            <span>are marked below · the kernel answers for one address</span>
          </>
        )
      ) : lookup.state === "asking" ? (
        <TextShimmer>{`Asking the kernel about ${lookup.target}…`}</TextShimmer>
      ) : lookup.state === "error" ? (
        <span role="alert" className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span className="text-destructive">{lookup.error.message}</span>
          <Button size="xs" variant="outline" onClick={lookup.retry}>
            Retry
          </Button>
        </span>
      ) : (
        <>
          <span className={mono}>{lookup.result.address}</span>{" "}
          {lookup.result.local ? (
            <span>is this host&rsquo;s own address </span>
          ) : (
            <>
              <span>leaves on</span>{" "}
              <span className={mono}>
                {lookup.result.device ?? "a device the kernel did not name"}
              </span>{" "}
              {lookup.result.source && (
                <>
                  <span>from</span> <span className={mono}>{lookup.result.source}</span>{" "}
                </>
              )}
              {lookup.result.gateway ? (
                <>
                  <span>via</span> <span className={mono}>{lookup.result.gateway}</span>{" "}
                </>
              ) : (
                <span>directly </span>
              )}
            </>
          )}
          <span aria-hidden>·</span>{" "}
          <span>
            {lookup.result.table ? (
              <>
                table <span className="text-foreground">{tableName(lookup.result.table)}</span>
              </>
            ) : (
              "table not reported"
            )}
          </span>{" "}
          <span aria-hidden>·</span> <span>the kernel&rsquo;s answer</span>{" "}
          <InfoTip label="What the answer proves">
            The route the kernel would pick now. Nothing was sent, so it does not prove a connection
            succeeds.
          </InfoTip>
        </>
      )}
    </p>
  )
}

/** The path this browser's replies take, and how much of the highlight the kernel vouches for. */
function Replies({ routing, family }: { routing: NetworkRouting; family: RouteFamily }) {
  const path = routing.clientPath
  const own = addressFamily(path.address)
  const highlight = replyHighlight(routing, own)
  return (
    <>
      <span>Your replies to</span> <span className={mono}>{path.address}</span>{" "}
      {path.device ? (
        <>
          <span>leave on</span> <span className={mono}>{path.device}</span>{" "}
        </>
      ) : (
        <span>have no path the kernel reported </span>
      )}
      {path.source && (
        <>
          <span>from</span> <span className={mono}>{path.source}</span>{" "}
        </>
      )}
      {path.gateway && (
        <>
          <span>via</span> <span className={mono}>{path.gateway}</span>{" "}
        </>
      )}
      {own !== family ? (
        <>
          <span aria-hidden>·</span>{" "}
          <span>over {own === "inet6" ? "IPv6" : "IPv4"}, not drawn here</span>{" "}
        </>
      ) : (
        highlight && (
          <>
            <span aria-hidden>·</span>{" "}
            <span>
              {highlight.basis === "kernel"
                ? "table from the kernel, rule evaluated"
                : highlight.basis === "table"
                  ? "table from the kernel, rule not named"
                  : "inferred from the routes"}
            </span>{" "}
            <InfoTip label="How the highlight was decided">
              {highlight.basis === "kernel"
                ? "The lit table is the one the kernel reports answering your replies; the lit rule is the one the evaluated rules reach it by."
                : highlight.basis === "table"
                  ? `The lit table is the kernel's answer for your replies; the rule is not named: ${highlight.reason ?? "the model could not decide it"}${highlight.candidates.length ? ` Rules looking up that table: ${highlight.candidates.join(", ")}.` : ""}`
                  : "Highlights infer which rule may answer your replies; they do not evaluate every policy selector."}
            </InfoTip>
          </>
        )
      )}
    </>
  )
}
