"use client"

import { useState } from "react"
import { useRouter } from "next/navigation"
import {
  Code,
  Copy,
  Lightning,
  Pause,
  Pencil,
  Play,
  RefreshClockwise,
  Route,
  Trash,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { copyText } from "@/lib/clipboard"
import { del, post } from "@/lib/api"
import type {
  StreamDeleteResult,
  StreamEntry,
  StreamResult,
  StreamStatus,
  StreamTestResult,
} from "@/lib/types"
import {
  includedPlace,
  listenFamily,
  listenLabel,
  streamTests,
  streamsLive,
  testPlace,
  testStatus,
  testSummary,
} from "@/lib/streams"
import { investigateHref } from "@/lib/network-investigator"
import type { useConfirm } from "@/components/confirm-dialog"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import type { Verb } from "@/components/verbs"
import { streamHref } from "@/components/proxy/stream-facts"

/**
 * Where a client on this host reaches a stream: the address it binds, or
 * loopback in its family for a wildcard.
 */
function streamTarget(stream: StreamEntry): string {
  if (!stream.address || stream.address === "0.0.0.0") return "127.0.0.1"
  if (stream.address === "::") return "::1"
  return stream.address
}

/**
 * What can be done to a stream, from its row on the list and from its own
 * page: one set of verbs, so the two never disagree about who may do what.
 *
 * Reading — re-checking, copying the route — is no change, so every account
 * has it. A test dials an address, which is the scanner boundary, so only an
 * administrator tests; pause, resume and delete are destructive routes. A
 * test's results stay until the next test or a reload: they are a reading of
 * that moment, not of the stream.
 */
export function useStreamVerbs({
  status,
  admin,
  confirm,
  refresh,
  checking,
  recheck,
  onDuplicate,
  onRaw,
  onDeleted,
}: {
  status: StreamStatus | undefined
  admin: boolean
  confirm: ReturnType<typeof useConfirm>["confirm"]
  refresh: () => void
  /** A re-check is out. */
  checking: boolean
  recheck: () => void
  /** Opens the New stream sheet with this stream's fields under a new name. */
  onDuplicate?: (stream: StreamEntry) => void
  onRaw: (stream: StreamEntry) => void
  /** After a delete went through: the stream's page goes back to the list. */
  onDeleted?: () => void
}) {
  const router = useRouter()
  const live = status ? streamsLive(status) : false
  const [resuming, setResuming] = useState("")
  const [tests, setTests] = useState<Record<string, StreamTestResult[] | "running">>({})

  const remove = (stream: StreamEntry) => {
    let result: StreamDeleteResult | undefined
    // A file the listing could not read has no port to name and no content
    // to keep; a link goes as a link, and what it points to stays.
    const port = stream.error ? null : stream.listen
    // Included inside http, nginx does read these — as http, refusing them.
    const misplaced = status?.includedIn
      ? `nginx reads these ${includedPlace(status.includedIn)}, where its test refuses them`
      : ""
    confirm({
      title: `Delete ${stream.name}`,
      confirmLabel: live && !stream.paused ? "Delete and reload" : "Delete",
      description: (
        <p>
          {stream.paused
            ? "It is paused, so nginx is not reading it and nothing stops forwarding."
            : port === null
              ? live
                ? "nginx reloads without it."
                : misplaced
                  ? `${misplaced}, so this file never forwarded anything.`
                  : "nginx is not reading these, so this only removes the file."
              : live
                ? `Port ${port} stops being forwarded as soon as nginx reloads.`
                : misplaced
                  ? `${misplaced}, so port ${port} was never forwarded.`
                  : `nginx is not reading these, so port ${port} was never forwarded — this removes the file before it ever took effect.`}{" "}
          {stream.link ? (
            <>
              This removes the link only, not <code className="font-mono">{stream.link}</code>.
            </>
          ) : stream.error ? (
            "It could not be read, so no copy of it is kept."
          ) : (
            <>
              The file is kept as <code className="font-mono">{stream.name}.conf.bak</code>.
            </>
          )}
        </p>
      ),
      action: async () => {
        result = await del<StreamDeleteResult>(`/proxy/streams/${encodeURIComponent(stream.name)}`)
        refresh()
      },
      // The delete itself completed; a reload that did not is its own news,
      // because the port stays forwarded until one succeeds.
      onDone: () => {
        if (result?.reloadError) {
          notify.warning("nginx did not reload", {
            description:
              live && port !== null
                ? `Port ${port} is still forwarded until nginx reloads. ${result.reloadError}`
                : result.reloadError,
          })
        }
        // The dialog promised a .bak for a file the listing could read.
        if (result?.unread && !stream.error) {
          notify.warning("No copy was kept", {
            description: `${stream.name}.conf could not be read when it was deleted: ${result.unread}`,
          })
        }
        if (result) onDeleted?.()
      },
    })
  }

  // Pausing moves the file into paused/, out of the include's reach; the
  // form cannot edit it there, and Resume puts it back under a save's checks.
  const pause = (stream: StreamEntry) => {
    let result: StreamResult | undefined
    const reads = live && stream.state !== "not-read"
    confirm({
      title: `Pause ${stream.name}`,
      confirmLabel: reads ? "Pause and reload" : "Pause",
      description: (
        <p>
          {reads
            ? `Port ${stream.listen} stops being forwarded as soon as nginx reloads.`
            : "nginx is not reading this stream, so nothing stops forwarding."}{" "}
          The file moves to <code className="font-mono">paused/</code> unchanged, and Resume puts it
          back.
        </p>
      ),
      action: async () => {
        result = await post<StreamResult>(
          `/proxy/streams/${encodeURIComponent(stream.name)}/enabled`,
          { enabled: false },
        )
        refresh()
      },
      onDone: () => {
        if (result?.reloadError) {
          notify.warning("nginx did not reload", {
            description: `Port ${stream.listen} is still forwarded until nginx reloads. ${result.reloadError}`,
          })
        }
      },
    })
  }

  const resume = async (stream: StreamEntry) => {
    setResuming(stream.name)
    try {
      const res = await post<StreamResult>(
        `/proxy/streams/${encodeURIComponent(stream.name)}/enabled`,
        { enabled: true },
      )
      if (res.reloadError) {
        notify.warning(`${stream.name} resumed, reload failed`, {
          description: `The file passed nginx's test and is back, but nginx did not reload, so it is not forwarding yet: ${res.reloadError}`,
        })
      } else if (res.listening === false) {
        notify.warning(`${stream.name} resumed, not listening yet`, {
          description: res.listenNote,
          action: { label: "Re-check", onClick: refresh },
        })
      } else if (!res.validation) {
        notify.warning(`${stream.name} resumed, not yet live`, { description: res.listenNote })
      } else {
        notify.success(
          res.listening ? `${stream.name} resumed and listening` : `${stream.name} resumed`,
          { description: res.listenNote },
        )
      }
    } catch (err) {
      notify.error(`${stream.name} not resumed`, err)
    } finally {
      setResuming("")
      refresh()
    }
  }

  const test = async (stream: StreamEntry) => {
    setTests((t) => ({ ...t, [stream.name]: "running" }))
    try {
      const results = await Promise.all(
        streamTests(stream).map((req) => post<StreamTestResult>("/proxy/streams/test", req)),
      )
      setTests((t) => ({ ...t, [stream.name]: results }))
    } catch (err) {
      setTests((t) => {
        const rest = { ...t }
        delete rest[stream.name]
        return rest
      })
      notify.error(`${stream.name} not tested`, err)
    }
  }

  const testable = (stream: StreamEntry) => !stream.error && streamTests(stream).length > 0
  const testVerb = (stream: StreamEntry): Verb[] =>
    admin && testable(stream)
      ? [
          {
            key: "test",
            label: tests[stream.name] === "running" ? "Testing…" : "Test",
            icon: Lightning,
            disabled: tests[stream.name] === "running",
            run: () => void test(stream),
          },
        ]
      : []
  // A stream that is not live can be asked about again at once, rather than
  // at the next minute's poll — after freeing its port, say.
  const recheckVerb = (stream: StreamEntry): Verb[] =>
    stream.state === "live" || stream.state === "not-read" || stream.paused
      ? []
      : [
          {
            key: "recheck",
            label: checking ? "Checking…" : "Re-check",
            icon: RefreshClockwise,
            disabled: checking,
            run: recheck,
          },
        ]
  // The connection path a client takes through the stream, on the Network
  // page: the forward joined with its sockets, backend legs and log. TCP
  // only — the investigator's probe sends nothing over UDP.
  const pathVerb = (stream: StreamEntry): Verb[] =>
    !admin || stream.paused || stream.error || stream.protocol === "udp"
      ? []
      : [
          {
            key: "path",
            label: "Trace in Network",
            icon: Route,
            run: () =>
              router.push(
                investigateHref({
                  target: streamTarget(stream),
                  port: stream.listen,
                  measure: stream.state === "live",
                }),
              ),
          },
        ]
  const rawVerb = (stream: StreamEntry): Verb[] =>
    admin
      ? [
          {
            key: "raw",
            label: "Raw file",
            icon: Code,
            disabled: Boolean(stream.error),
            run: () => onRaw(stream),
          },
        ]
      : []
  const copyVerbs = (stream: StreamEntry): Verb[] =>
    stream.error
      ? []
      : [
          {
            key: "copy-listen",
            label: stream.address && !listenFamily(stream.address) ? "Copy address" : "Copy port",
            icon: Copy,
            run: () => void copyText(listenLabel(stream), `${listenLabel(stream)} copied`),
          },
          {
            key: "copy-upstream",
            label: "Copy upstream",
            icon: Copy,
            run: () => void copyText(stream.upstream, `${stream.upstream} copied`),
          },
        ]
  // Only a file the form can say everything about is duplicated: from one
  // with a deny rule or a second server, the copy would silently lack them.
  const duplicateVerb = (stream: StreamEntry): Verb[] =>
    !admin || !onDuplicate || stream.error || stream.unsupported.length > 0
      ? []
      : [{ key: "duplicate", label: "Duplicate", icon: Copy, run: () => onDuplicate(stream) }]
  // A link is stopped by deleting it: moved into paused/, a relative one
  // would point somewhere else.
  const pauseVerb = (stream: StreamEntry): Verb[] =>
    !admin || stream.paused || stream.link || stream.error
      ? []
      : [{ key: "pause", label: "Pause", icon: Pause, run: () => pause(stream) }]
  const resumeVerb = (stream: StreamEntry): Verb[] =>
    admin && stream.paused
      ? [
          {
            key: "resume",
            label: resuming === stream.name ? "Resuming…" : "Resume",
            icon: Play,
            disabled: resuming !== "" || Boolean(stream.error),
            run: () => void resume(stream),
          },
        ]
      : []
  const deleteVerb = (stream: StreamEntry): Verb[] =>
    admin
      ? [{ key: "delete", label: "Delete", icon: Trash, danger: true, run: () => remove(stream) }]
      : []

  /** The row's menu: everything, the way to Settings first for a stream the form edits. */
  const rowVerbs = (stream: StreamEntry): Verb[] => [
    ...resumeVerb(stream),
    ...(admin && !stream.paused && !stream.error
      ? [
          {
            key: "edit",
            label: "Edit",
            icon: Pencil,
            run: () => router.push(streamHref(stream.name, "settings")),
          },
        ]
      : []),
    ...recheckVerb(stream),
    ...testVerb(stream),
    ...pathVerb(stream),
    ...rawVerb(stream),
    ...duplicateVerb(stream),
    ...copyVerbs(stream),
    ...pauseVerb(stream),
    ...deleteVerb(stream),
  ]

  /**
   * The page's verbs: the one command at the far right — Resume for a paused
   * stream, Test for one that can be dialled, Re-check for one that is not
   * live — and the rest behind the menu.
   */
  const pageVerbs = (stream: StreamEntry): { primary?: Verb; rest: Verb[] } => {
    const primary =
      resumeVerb(stream)[0] ?? testVerb(stream)[0] ?? (admin ? undefined : recheckVerb(stream)[0])
    const rest = [
      ...recheckVerb(stream),
      ...pathVerb(stream),
      ...rawVerb(stream),
      ...duplicateVerb(stream),
      ...copyVerbs(stream),
      ...pauseVerb(stream),
      ...deleteVerb(stream),
    ].filter((verb) => verb.key !== primary?.key)
    return { primary, rest }
  }

  return { rowVerbs, pageVerbs, tests, resume, resuming }
}

/** A test's outcome, and every dial's answer in a popover under it. */
export function TestResults({ name, results }: { name: string; results: StreamTestResult[] }) {
  const summary = testSummary(results)
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          className="h-7 min-w-0 px-1.5"
          aria-label={`Test of ${name}: ${summary.label}`}
        >
          <Status tone={summary.tone} label={summary.label} className="min-w-0 truncate" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 space-y-3">
        {results.map((result, i) => (
          <div key={i} className="min-w-0 space-y-1">
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-x-3">
              <span className="text-hint font-medium">{testPlace(result)}</span>
              <Status {...testStatus(result)} />
            </div>
            {result.address && (
              <span className="block font-mono text-hint break-all text-muted-foreground">
                {result.address}
              </span>
            )}
            {result.banner && (
              <pre className="max-h-24 overflow-auto font-mono text-hint break-all whitespace-pre-wrap">
                {result.banner}
              </pre>
            )}
            {result.answer && <p className="text-hint">{result.answer}</p>}
            <p className="text-hint text-muted-foreground">{result.detail}</p>
            {result.warnings.map((warning) => (
              <p key={warning} className="text-hint text-warning">
                {warning}
              </p>
            ))}
          </div>
        ))}
      </PopoverContent>
    </Popover>
  )
}
