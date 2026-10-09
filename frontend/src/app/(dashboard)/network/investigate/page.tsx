"use client"

import { Suspense, useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"
import { PathReport } from "@/components/network/path-report"
import { SaveInvestigationRun } from "@/components/network/save-investigation-run"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Disclosure, Field, FieldRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useAuth } from "@/hooks/use-auth"
import { errorMessage, get, post } from "@/lib/api"
import { pathDraftFromQuery, pathRequest } from "@/lib/network-investigator"
import type { PathDraft } from "@/lib/network-investigator"
import type { PathResult, PathSources } from "@/lib/network-investigator-types"

// Another page names the tuple in the query string — a stream's port, say —
// which the App Router hands out only inside a Suspense boundary.
export default function NetworkInvestigatorPage() {
  return (
    <Suspense>
      <NetworkInvestigator />
    </Suspense>
  )
}

// A report register: the input selects the evidence to read; it does not stage
// a network change or draw unmeasured edges as successful packet traversal.
function NetworkInvestigator() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const params = useSearchParams()
  const [sources, setSources] = useState<PathSources>({ containers: [] })
  const [draft, setDraft] = useState(() => pathDraftFromQuery(params))
  const [result, setResult] = useState<PathResult>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  useEffect(() => {
    if (!admin) return
    const abort = new AbortController()
    get<PathSources>("/network/investigate/sources", undefined, abort.signal)
      .then(setSources)
      .catch((err) => {
        if (!abort.signal.aborted) {
          setSources({ containers: [], error: errorMessage(err) })
        }
      })
    return () => abort.abort()
  }, [admin])
  const change = <K extends keyof PathDraft>(key: K, value: PathDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }))
  const request = pathRequest(draft)
  const supportedProbe = draft.protocol === "tcp" && !draft.mark.trim()
  const submit = async () => {
    if (!request || busy || !admin) return
    setBusy(true)
    setError(undefined)
    try {
      setResult(await post<PathResult>("/network/investigate", request))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="Connection path" />
      <Panel plain>
        <PanelHeader title="Investigate a connection" />
        <PanelBody className="space-y-5">
          <p className="max-w-3xl text-body text-muted-foreground">
            Follow native DNS, policy rules, the kernel route and the owners that can explain this
            tuple. Each step states whether it is a snapshot, a supported model, a measured response
            or unknown.
          </p>
          {!admin && (
            <p role="status" className="text-body text-muted-foreground">
              Connection diagnostics and process/container attribution require system administrator
              access.
            </p>
          )}
          <form
            className="max-w-3xl space-y-4"
            onSubmit={(event) => {
              event.preventDefault()
              void submit()
            }}
          >
            <FieldRow>
              <Field label="Source" htmlFor="path-source" hint={sources.error}>
                <PathSelect
                  id="path-source"
                  value={draft.source}
                  onChange={(value) => {
                    change("source", value)
                    change("address", "")
                  }}
                  disabled={!admin || busy}
                  options={[
                    { value: "host", label: "Dashboard host" },
                    ...sources.containers.map((container) => ({
                      value: container.id,
                      label: container.name,
                    })),
                  ]}
                />
              </Field>
              <Field label="Destination" htmlFor="path-target" hint="A name or literal address.">
                <Input
                  id="path-target"
                  value={draft.target}
                  onChange={(event) => {
                    change("target", event.target.value)
                    change("address", "")
                  }}
                  disabled={!admin || busy}
                  placeholder="private.corp or 192.0.2.8"
                  spellCheck={false}
                  autoComplete="off"
                  className="font-mono"
                />
              </Field>
            </FieldRow>
            <FieldRow columns={3}>
              <Field label="Family" htmlFor="path-family">
                <PathSelect
                  id="path-family"
                  value={draft.family}
                  onChange={(value) => {
                    change("family", value as PathDraft["family"])
                    change("address", "")
                  }}
                  disabled={!admin || busy}
                  options={[
                    { value: "inet", label: "IPv4" },
                    { value: "inet6", label: "IPv6" },
                  ]}
                />
              </Field>
              <Field label="Protocol" htmlFor="path-protocol">
                <PathSelect
                  id="path-protocol"
                  value={draft.protocol}
                  onChange={(value) => change("protocol", value as PathDraft["protocol"])}
                  disabled={!admin || busy}
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
                  disabled={!admin || busy}
                />
              </Field>
            </FieldRow>
            <Disclosure summary="Route selectors" quiet facts="Source address and packet mark">
              <FieldRow>
                <Field
                  label="Source address"
                  htmlFor="path-source-address"
                  hint="Blank uses the kernel's selected source."
                >
                  <Input
                    id="path-source-address"
                    value={draft.sourceAddress}
                    onChange={(event) => change("sourceAddress", event.target.value)}
                    disabled={!admin || busy}
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
                <Field
                  label="Mark"
                  htmlFor="path-mark"
                  hint="One decimal or hexadecimal value; route model only."
                >
                  <Input
                    id="path-mark"
                    value={draft.mark}
                    onChange={(event) => change("mark", event.target.value)}
                    disabled={!admin || busy}
                    placeholder="0x1"
                    spellCheck={false}
                    className="font-mono"
                  />
                </Field>
              </FieldRow>
            </Disclosure>
            {result &&
              result.scope.target === draft.target.trim() &&
              result.scope.family === draft.family &&
              (result.request.containerId || "host") === draft.source &&
              result.addresses.length > 1 && (
                <Field
                  label="Chosen DNS address"
                  htmlFor="path-address"
                  hint="The next investigation checks that this address is still in the native answer."
                >
                  <PathSelect
                    id="path-address"
                    value={draft.address || result.scope.address || result.addresses[0]}
                    onChange={(value) => change("address", value)}
                    disabled={!admin || busy}
                    options={result.addresses.map((address) => ({
                      value: address,
                      label: address,
                    }))}
                  />
                </Field>
              )}
            <div className="space-y-1">
              <label className="flex min-h-11 items-center gap-3 text-body">
                <Checkbox
                  aria-label="Measure a TCP connection"
                  checked={draft.measure && supportedProbe}
                  onCheckedChange={(value) => change("measure", value === true)}
                  disabled={!admin || busy || !supportedProbe}
                />
                Measure one TCP connection from this source
              </label>
              <p className="text-hint text-muted-foreground">
                {supportedProbe
                  ? "Sends a bounded TCP handshake to the pinned address and port. It does not test TLS, login or inbound access."
                  : "UDP and explicit marks have model evidence only; the TCP adapter cannot reproduce these probes."}
              </p>
            </div>
            {error && (
              <p role="alert" className="text-body text-destructive">
                {error}
                {result && " The previous report remains below with its original scope."}
              </p>
            )}
            <div className="flex flex-wrap gap-3">
              <Button type="submit" pending={busy} disabled={!admin || !request || busy}>
                Investigate
              </Button>
              {request && <SaveInvestigationRun request={request} disabled={!admin || busy} />}
            </div>
          </form>
        </PanelBody>
      </Panel>
      {admin && result && <PathReport result={result} />}
    </Page>
  )
}

function PathSelect({
  id,
  value,
  onChange,
  disabled,
  options,
}: {
  id: string
  value: string
  onChange: (value: string) => void
  disabled: boolean
  options: { value: string; label: string }[]
}) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger id={id} className="w-full">
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
