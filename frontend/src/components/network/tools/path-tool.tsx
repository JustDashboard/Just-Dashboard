"use client"

import { useEffect, useState } from "react"
import { PathReport } from "@/components/network/path-report"
import { SaveInvestigationRun } from "@/components/network/save-investigation-run"
import { Disclosure, Field, InfoTip } from "@/components/form"
import { Play } from "@/components/icons"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { errorMessage, get, post } from "@/lib/api"
import { pathDraftFromQuery, pathRequest, type PathDraft } from "@/lib/network-investigator"
import type { PathResult, PathSources } from "@/lib/network-investigator-types"
import { cn } from "@/lib/utils"
import { ToolEmpty, ToolHead } from "./tool-frame"
import { PATH_TOOL } from "./tool-links"

/** What the rail and the empty pane say the investigator does. */
export const PATH_HINT =
  "Follow DNS, the policy rules, the kernel route and the owners that explain one connection, each step marked as a snapshot, a model, a measurement or unknown"

type Query = { get(name: string): string | null; toString(): string }

/**
 * Connection path as one more diagnostic in the workbench. It was a page of
 * its own (`/network/investigate`, which now lands here with its query); the
 * form is the investigator's, laid out as every other tool's, and the report
 * is the chain of hops `PathReport` draws.
 *
 * Like every tool it stays mounted while another is chosen, so its draft,
 * its report and a request in flight survive a switch. The containers it can
 * start from are read the first time it is opened, not on every visit to the
 * workbench.
 */
export function PathTool({
  arrival,
  active,
  hue,
}: {
  /** The address's query when it chose this tool; a new one replaces the draft. */
  arrival?: Query
  active: boolean
  hue?: string
}) {
  const seed = arrival?.toString() ?? ""
  const [draft, setDraft] = useState(() => pathDraftFromQuery(arrival ?? new URLSearchParams()))
  // A container handed over by name (`?container=postgres`) is chosen once
  // the inventory names it.
  const [handoff, setHandoff] = useState(() => arrival?.get("container") ?? undefined)
  const [previousSeed, setPreviousSeed] = useState(seed)
  if (seed !== previousSeed) {
    setPreviousSeed(seed)
    if (arrival) {
      setDraft(pathDraftFromQuery(arrival))
      setHandoff(arrival.get("container") ?? undefined)
    }
  }
  const [opened, setOpened] = useState(active)
  if (active && !opened) setOpened(true)
  const [sources, setSources] = useState<PathSources & { loaded?: boolean }>({ containers: [] })
  if (handoff && sources.loaded) {
    const chosen = sources.containers.find((c) => c.name === handoff)
    setHandoff(undefined)
    if (chosen) setDraft((current) => ({ ...current, source: chosen.id, address: "" }))
  }
  const [result, setResult] = useState<PathResult>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()

  useEffect(() => {
    if (!opened) return
    const abort = new AbortController()
    get<PathSources>("/network/investigate/sources", undefined, abort.signal)
      .then((found) => setSources({ ...found, loaded: true }))
      .catch((err) => {
        if (!abort.signal.aborted)
          setSources({ containers: [], error: errorMessage(err), loaded: true })
      })
    return () => abort.abort()
  }, [opened])

  const change = <K extends keyof PathDraft>(key: K, value: PathDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }))
  const request = pathRequest(draft)
  const supportedProbe = draft.protocol === "tcp" && !draft.mark.trim()
  const submit = async () => {
    if (!request || busy) return
    setBusy(true)
    setError(undefined)
    try {
      setResult(await post<PathResult>("/network/investigate", request))
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  const onEnter = (event: React.KeyboardEvent) => {
    if (event.key === "Enter") void submit()
  }
  const sameScope =
    result &&
    result.scope.target === draft.target.trim() &&
    result.scope.family === draft.family &&
    (result.request.containerId || "host") === draft.source

  return (
    <div className="min-w-0 space-y-5">
      <ToolHead tool={PATH_TOOL} hue={hue} label="Connection path" hint={PATH_HINT} />
      {/* As every tool: the fields are in the page only while it is in view.
          The draft is this component's, so a switch loses nothing. */}
      {active && (
        // Not a <form>: inside one, each Select adds a hidden native control
        // beside its trigger, which pushed the selects off the inputs' line.
        <div className="min-w-0 space-y-4">
          <div className="flex min-w-0 flex-wrap items-end gap-3">
            <Field
              label="Source"
              htmlFor="path-source"
              error={sources.error}
              className="w-full sm:w-48"
            >
              <PathSelect
                id="path-source"
                value={draft.source}
                onChange={(value) => setDraft((d) => ({ ...d, source: value, address: "" }))}
                disabled={busy}
                options={[
                  { value: "host", label: "Dashboard host" },
                  ...sources.containers.map((container) => ({
                    value: container.id,
                    label: container.name,
                  })),
                ]}
              />
            </Field>
            <Field label="Destination" htmlFor="path-target" className="min-w-48 flex-1">
              <Input
                id="path-target"
                value={draft.target}
                onChange={(event) =>
                  setDraft((d) => ({ ...d, target: event.target.value, address: "" }))
                }
                onKeyDown={onEnter}
                disabled={busy}
                placeholder="private.corp or 192.0.2.8"
                spellCheck={false}
                autoComplete="off"
                className="font-mono"
              />
            </Field>
          </div>
          <div className="flex min-w-0 flex-wrap items-end gap-3">
            <Field label="Family" htmlFor="path-family">
              <PathSelect
                id="path-family"
                className="w-24"
                value={draft.family}
                onChange={(value) =>
                  setDraft((d) => ({ ...d, family: value as PathDraft["family"], address: "" }))
                }
                disabled={busy}
                options={[
                  { value: "inet", label: "IPv4" },
                  { value: "inet6", label: "IPv6" },
                ]}
              />
            </Field>
            <Field label="Protocol" htmlFor="path-protocol">
              <PathSelect
                id="path-protocol"
                className="w-24"
                value={draft.protocol}
                onChange={(value) => change("protocol", value as PathDraft["protocol"])}
                disabled={busy}
                options={[
                  { value: "tcp", label: "TCP" },
                  { value: "udp", label: "UDP" },
                ]}
              />
            </Field>
            <Field label="Port" htmlFor="path-port">
              <Input
                id="path-port"
                inputMode="numeric"
                value={draft.port}
                onChange={(event) => change("port", event.target.value)}
                onKeyDown={onEnter}
                disabled={busy}
                className="w-24 font-mono"
              />
            </Field>
            {/* The verbs stand before everything optional, so a field that
                arrives with a report (the DNS answers) never moves them. */}
            <div className="flex flex-wrap items-center gap-3 sm:ml-auto">
              <Button onClick={() => void submit()} pending={busy} disabled={!request || busy}>
                <Play />
                Run
              </Button>
              {request ? (
                <SaveInvestigationRun request={request} disabled={busy} />
              ) : (
                <Button type="button" variant="outline" disabled>
                  Run and save
                </Button>
              )}
            </div>
          </div>
          <div className="flex min-w-0 items-center gap-2.5">
            <label className="flex min-h-9 min-w-0 items-center gap-2.5 text-body">
              <Checkbox
                aria-label="Measure a TCP connection"
                checked={draft.measure && supportedProbe}
                onCheckedChange={(value) => change("measure", value === true)}
                disabled={busy || !supportedProbe}
              />
              Measure one TCP connection
            </label>
            <InfoTip label="What measuring does">
              {supportedProbe
                ? "Sends one bounded TCP handshake from the chosen source to the pinned address and port. It does not test TLS, login or inbound access."
                : "UDP and explicit marks have model evidence only; the TCP adapter cannot reproduce these probes, and no substitute is sent."}
            </InfoTip>
          </div>
          <Disclosure summary="Route selectors" quiet facts="Source address and packet mark">
            <div className="flex min-w-0 flex-wrap items-end gap-3">
              <Field
                label="Source address"
                htmlFor="path-source-address"
                className="min-w-48 flex-1"
              >
                <Input
                  id="path-source-address"
                  value={draft.sourceAddress}
                  onChange={(event) => change("sourceAddress", event.target.value)}
                  onKeyDown={onEnter}
                  disabled={busy}
                  placeholder="the kernel's choice"
                  spellCheck={false}
                  className="font-mono"
                />
              </Field>
              <Field label="Mark" htmlFor="path-mark">
                <Input
                  id="path-mark"
                  value={draft.mark}
                  onChange={(event) => change("mark", event.target.value)}
                  onKeyDown={onEnter}
                  disabled={busy}
                  placeholder="0x1"
                  spellCheck={false}
                  className="w-32 font-mono"
                />
              </Field>
            </div>
          </Disclosure>
          {sameScope && result.addresses.length > 1 && (
            <Field label="Chosen DNS address" htmlFor="path-address" className="max-w-xs">
              <PathSelect
                id="path-address"
                value={draft.address || result.scope.address || result.addresses[0]}
                onChange={(value) => change("address", value)}
                disabled={busy}
                options={result.addresses.map((address) => ({ value: address, label: address }))}
              />
            </Field>
          )}
          {error && (
            <div className="space-y-1.5">
              <ErrorState error={error} onRetry={request && !busy ? submit : undefined} />
              {result && (
                <p className="text-hint text-muted-foreground">
                  The previous report stays below with its own scope.
                </p>
              )}
            </div>
          )}
        </div>
      )}
      {result ? (
        <div className={cn("border-t border-hairline pt-5", busy && "opacity-60")}>
          <PathReport result={result} />
        </div>
      ) : (
        <ToolEmpty
          tool={PATH_TOOL}
          hue={hue}
          sentence={`${PATH_HINT}.`}
          busy={busy}
          example="example.com"
          onExample={() => setDraft((d) => ({ ...d, target: "example.com", address: "" }))}
        />
      )}
    </div>
  )
}

function PathSelect({
  id,
  value,
  onChange,
  disabled,
  options,
  className,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  disabled: boolean
  options: { value: string; label: string }[]
  className?: string
}) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger id={id} className={cn("w-full", className)}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}
