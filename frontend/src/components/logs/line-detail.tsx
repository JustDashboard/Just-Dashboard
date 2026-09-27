"use client"

import { useEffect, useState } from "react"
import { ClockRewind, Copy, EyeOff, Filter, MoreHorizontal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { errorMessage, get } from "@/lib/api"
import { timestamp, truncateMiddle } from "@/lib/format"
import type { LogLine, LogSearchResult } from "@/lib/types"
import type { LogFields } from "@/components/logs/types"
import { LEVEL_LABEL, hideField, onlyField, type LogLevel } from "@/lib/log-filter"
import { fieldOf, isFilterable } from "@/lib/log-fields"
import { eventMeta, lensFor } from "@/lib/log-lenses"
import { useLogView } from "@/lib/log-view"
import { copyText } from "@/lib/clipboard"
import { EVENT_WORD, LEVEL_WORD, laneStyle } from "@/components/logs/log-text"
import { FieldValue } from "@/components/logs/field-value"
import { LogRow, eventColumnFor } from "@/components/logs/log-console"
import { FactDot } from "@/components/metrics/host-identity"
import { Detail, DetailList } from "@/components/page"
import { Well } from "@/components/panel"
import { IconAction } from "@/components/icon-action"
import { VerbMenu, type Verb } from "@/components/verbs"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"

/** How many lines each side "Lines around this" opens with; More doubles it. */
const AROUND = 25
const AROUND_MAX = 400

/**
 * One line, opened.
 *
 * The request row's shape (`RequestDetail`): what the line records as its
 * title, the values the lens read out of it as a list of facts drawn as what
 * they are, each one a press away from "only lines like this" or "hide lines
 * like this", and the line itself as it was written. Three questions a line
 * raises are named buttons — what was around it, what else of its kind, and
 * the page's own verb — and the rest is one menu (§13).
 *
 * "Lines around this" is two searches, before and after the line's own
 * instant, with nothing narrowing them: a search keeps the newest lines it
 * finds, so one window centred on the line would come back leaning after it,
 * and a filter would hide exactly the context the reader asked for.
 */
export function LineDetail({
  line,
  head,
  lens,
  forcedLens,
  sourceId,
  fields,
  onFieldsChange,
  lineVerbs,
}: {
  line: LogLine
  /** The record a continuation line belongs to: its time is the line's. */
  head?: LogLine
  /** The lens the line was read through, for its words. */
  lens?: string
  /** The lens the reader forced, sent with the searches around the line. */
  forcedLens?: string
  sourceId: string
  fields: LogFields
  onFieldsChange: (fields: LogFields) => void
  lineVerbs?: (line: LogLine) => Verb[]
}) {
  const { highlight } = useLogView()
  const plain = !highlight
  const eventId = line.event
  const meta = eventId ? eventMeta(lens, eventId, line.lens) : undefined
  const level = (line.level || "unknown") as LogLevel
  const at = line.timestamp ?? (line.cont ? head?.timestamp : undefined)
  const [around, setAround] = useState<number | null>(null)

  const title = meta?.label ?? (line.event ? line.event.replace(/_/g, " ") : undefined)
  const attrs = rankedEntries(line.attrs, lens)
  const own = Object.entries(line.fields ?? {}).filter(([, value]) => value !== "")
  const verbs = lineVerbs?.(line) ?? []
  const inline = verbs.find((v) => v.inline)
  const pretty = prettyJSON(line.text)

  const menu: Verb[] = [
    {
      key: "copy",
      label: "Copy line",
      icon: Copy,
      run: () => void copyText(line.text, "Line copied"),
    },
    {
      key: "json",
      label: "Copy as JSON",
      icon: Copy,
      // The match ranges are the search's, not the line's.
      run: () =>
        void copyText(
          JSON.stringify({ ...line, match: undefined }, null, 2),
          "Line copied as JSON",
        ),
    },
    ...verbs.filter((v) => v !== inline),
  ]

  const action = "w-full max-sm:h-9 sm:w-auto"

  return (
    <div className="animate-rise border-y border-hairline bg-background px-4 py-3 pl-[1.125rem] text-xs">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b border-hairline pb-2">
        <span
          className={cn(
            "text-body font-medium",
            title ? !plain && EVENT_WORD[meta?.tone ?? "default"] : !plain && LEVEL_WORD[level],
          )}
        >
          {title ?? `${LEVEL_LABEL[level] ?? "other"} line`}
        </span>
        {at && (
          <>
            <FactDot />
            <span className="numeric text-muted-foreground">{timestamp(at)}</span>
          </>
        )}
        {line.source && (
          <>
            <FactDot />
            <span
              className="truncate font-medium"
              style={plain ? undefined : laneStyle(line.source)}
            >
              {line.source}
            </span>
          </>
        )}
        {line.no !== undefined && (
          <span className="numeric ml-auto text-muted-foreground">
            line {line.no.toLocaleString()}
            {line.file && ` of ${line.file}`}
          </span>
        )}
      </div>

      {(attrs.length > 0 || own.length > 0) && (
        <DetailList className="mt-2.5 items-center">
          {[...attrs, ...own.filter(([key]) => !line.attrs?.[key])].map(([key, value]) => (
            <FactRow
              key={key}
              name={key}
              value={value}
              lens={lens}
              plain={plain}
              fp={key === "query" ? line.attrs?.fp : undefined}
              fields={fields}
              onFieldsChange={onFieldsChange}
            />
          ))}
        </DetailList>
      )}

      <Well className="mt-3 max-h-64 wrap-anywhere whitespace-pre-wrap">{pretty ?? line.text}</Well>

      <div className="mt-3 flex flex-col gap-1.5 sm:flex-row sm:flex-wrap sm:items-center">
        <Button
          size="xs"
          variant="outline"
          className={action}
          disabled={!at}
          title={at ? undefined : "This line has no time to look around"}
          aria-pressed={around !== null}
          onClick={() => setAround((n) => (n === null ? AROUND : null))}
        >
          <ClockRewind className="size-3" />
          Lines around this
        </Button>
        {eventId && (
          <Button
            size="xs"
            variant="outline"
            className={action}
            onClick={() => onFieldsChange(onlyField(fields, "event", eventId))}
          >
            <Filter className="size-3" />
            Only this event
          </Button>
        )}
        {inline && (
          <Button
            size="xs"
            variant="outline"
            disabled={inline.disabled}
            className={cn(action, inline.danger && "text-destructive")}
            onClick={inline.run}
          >
            <inline.icon className="size-3" />
            {inline.label}
          </Button>
        )}
        <VerbMenu
          verbs={menu}
          align="start"
          label="More actions for this line"
          trigger={
            <Button
              size="xs"
              variant="outline"
              className={action}
              aria-label="More actions for this line"
            >
              <MoreHorizontal className="size-3" />
              <span className="sm:hidden">More</span>
            </Button>
          }
        />
      </div>

      {around !== null && at && (
        <LinesAround
          key={around}
          line={line}
          at={at}
          n={around}
          sourceId={sourceId}
          lens={lens}
          forcedLens={forcedLens}
          onMore={around < AROUND_MAX ? () => setAround(around * 2) : undefined}
        />
      )}
    </div>
  )
}

/**
 * The order a line's values are listed in: what the lens puts in its columns
 * first, then what it ranks by, then the rest by name — the values a reader
 * came for before the ones the parser happened to find.
 */
function rankedEntries(attrs: Record<string, string> | undefined, lensId: string | undefined) {
  const lens = lensFor(lensId)
  const order = [...(lens?.columns ?? []), ...(lens?.facets ?? [])]
  const rank = (key: string) => {
    const at = order.indexOf(key)
    return at < 0 ? order.length : at
  }
  return Object.entries(attrs ?? {})
    .filter(([, value]) => value !== "")
    .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
}

function FactRow({
  name,
  value,
  lens,
  plain,
  fp,
  fields,
  onFieldsChange,
}: {
  name: string
  value: string
  lens?: string
  plain: boolean
  /** A query's shape, which is what "only lines like this" means for a statement. */
  fp?: string
  fields: LogFields
  onFieldsChange: (fields: LogFields) => void
}) {
  const field = fieldOf(name)
  const label = field.label.charAt(0).toUpperCase() + field.label.slice(1)
  // A statement's text is one line's; its fingerprint is every run of it.
  const key = isFilterable(name) ? name : fp ? "fp" : undefined
  const target = key === "fp" ? fp! : value
  const said =
    key === "fp" ? "the query has this shape" : `${field.label} is ${truncateMiddle(value, 40)}`
  return (
    <div className="group contents">
      <Detail label={label} className="flex items-center gap-1">
        <FieldValue name={name} value={value} lens={lens} plain={plain} className="min-w-0" />
        {key && (
          <span className="ml-auto flex shrink-0 items-center">
            <IconAction
              reveal
              label={`Only lines where ${said}`}
              className="-my-1 size-6"
              onClick={() => onFieldsChange(onlyField(fields, key, target))}
            >
              <Filter />
            </IconAction>
            <IconAction
              reveal
              label={`Hide lines where ${said}`}
              className="-my-1 size-6"
              onClick={() => onFieldsChange(hideField(fields, key, target))}
            >
              <EyeOff />
            </IconAction>
          </span>
        )}
      </Detail>
    </div>
  )
}

/** The text as indented JSON when it is one object, which is how a structured line is read. */
function prettyJSON(text: string): string | undefined {
  const trimmed = text.trim()
  if (!trimmed.startsWith("{")) return undefined
  try {
    const parsed: unknown = JSON.parse(trimmed)
    return parsed && typeof parsed === "object" ? JSON.stringify(parsed, null, 2) : undefined
  } catch {
    return undefined
  }
}

function sameLine(a: LogLine, b: LogLine) {
  return a.timestamp === b.timestamp && a.text === b.text
}

/**
 * What the source wrote either side of one line: the newest `n` before its
 * instant and the first `n` from it, drawn by the console's own row so the
 * context reads exactly as the pane does, with the line itself marked.
 */
function LinesAround({
  line,
  at,
  n,
  sourceId,
  lens,
  forcedLens,
  onMore,
}: {
  line: LogLine
  at: string
  n: number
  sourceId: string
  lens?: string
  forcedLens?: string
  onMore?: () => void
}) {
  const { wrap, highlight, time } = useLogView()
  const [state, setState] = useState<{ lines?: LogLine[]; error?: string }>({})
  const eventColumn = state.lines ? eventColumnFor(state.lines, lens) : false
  useEffect(() => {
    const controller = new AbortController()
    const common = { source: sourceId, lens: forcedLens || undefined, limit: n }
    Promise.all([
      get<LogSearchResult>("/logs/search", { ...common, until: at }, controller.signal),
      get<LogSearchResult>(
        "/logs/search",
        { ...common, since: at, order: "asc" },
        controller.signal,
      ),
    ]).then(
      ([before, after]) => {
        const earlier = before.lines ?? []
        const later = (after.lines ?? []).filter((l) => !earlier.some((e) => sameLine(e, l)))
        setState({ lines: [...earlier, ...later] })
      },
      (err) => {
        if (!controller.signal.aborted) setState({ error: errorMessage(err) })
      },
    )
    return () => controller.abort()
  }, [at, n, sourceId, forcedLens])

  return (
    <div className="mt-3 animate-rise">
      <div className="mb-1.5 flex items-center gap-2 text-hint text-muted-foreground">
        <span className="eyebrow">Lines around this</span>
        {state.lines && (
          <span className="numeric">
            {state.lines.length.toLocaleString()} {state.lines.length === 1 ? "line" : "lines"}
          </span>
        )}
        {onMore && state.lines && (
          <Button size="xs" variant="ghost" className="ml-auto h-6" onClick={onMore}>
            More
          </Button>
        )}
      </div>
      {state.error ? (
        <ErrorState error={new Error(state.error)} />
      ) : !state.lines ? (
        <p className="text-hint text-muted-foreground">Reading the lines around it…</p>
      ) : (
        <div className="max-h-80 overflow-auto rounded-lg border border-hairline bg-surface-sunken py-1 font-mono text-xs leading-relaxed">
          {state.lines.map((l, i) => (
            <LogRow
              key={i}
              line={l}
              open={sameLine(l, line)}
              time={time}
              prev={state.lines![i - 1]?.timestamp}
              wrap={wrap}
              highlight={highlight}
              lens={lens}
              eventColumn={eventColumn}
            />
          ))}
        </div>
      )}
    </div>
  )
}
