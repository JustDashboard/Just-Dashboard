"use client"

import { useState } from "react"
import { Disclosure, Field, FieldRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { ErrorState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import {
  captureDraftProblem,
  captureRequest,
  newCaptureDraft,
  type CaptureDraft,
  type CaptureInterface,
  type CaptureRun,
} from "@/lib/network-captures"
import type { DiagnosticRun } from "@/lib/network-diagnostics"
import { NetworkReadWarning } from "./read-warning"

function Pick({
  id,
  value,
  options,
  onChange,
  disabled,
}: {
  id: string
  value: string
  options: { value: string; label: string; disabled?: boolean }[]
  onChange: (value: string) => void
  disabled?: boolean
}) {
  return (
    <Select
      value={value || "unset"}
      onValueChange={(value) => onChange(value === "unset" ? "" : value)}
      disabled={disabled}
    >
      <SelectTrigger id={id} className="w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {options.map((option) => (
          <SelectItem key={option.value} value={option.value} disabled={option.disabled}>
            {option.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function CaptureCreate({
  open,
  onOpenChange,
  onCreated,
  seed,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated: (id: string) => void
  /** Settings handed over from a quick packet snapshot; starting is still a press. */
  seed?: Partial<CaptureDraft>
}) {
  const [draft, setDraft] = useState(() => ({ ...newCaptureDraft(), ...seed }))
  const seedKey = seed ? JSON.stringify(seed) : ""
  const [previousSeed, setPreviousSeed] = useState(seedKey)
  if (seedKey !== previousSeed) {
    setPreviousSeed(seedKey)
    if (seed) setDraft((current) => ({ ...current, ...seed }))
  }
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const interfaces = usePoll(
    (signal) => get<CaptureInterface[]>("/network/captures/interfaces", undefined, signal),
    30000,
    [open],
    { enabled: open },
  )
  const incidents = usePoll(
    (signal) => get<DiagnosticRun[]>("/network/diagnostics/", undefined, signal),
    30000,
    [open],
    { enabled: open },
  )
  const change = <K extends keyof CaptureDraft>(key: K, value: CaptureDraft[K]) =>
    setDraft((current) => ({ ...current, [key]: value }))
  const request = captureRequest(draft)
  const native = interfaces.data?.find((row) => row.name === draft.interface && row.up)
  const submit = async () => {
    if (busy || !request || !native || interfaces.error) return
    setBusy(true)
    setError(undefined)
    try {
      const created = await post<CaptureRun>("/network/captures/", {
        name: draft.name.trim(),
        request,
      })
      onCreated(created.id)
      onOpenChange(false)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={(next) => {
        if (!busy) onOpenChange(next)
      }}
      title="Start a packet capture"
      size="lg"
      footer={
        <Button
          disabled={busy || !request || !native || Boolean(interfaces.error)}
          pending={busy}
          onClick={() => void submit()}
        >
          Start capture
        </Button>
      }
    >
      <div className="space-y-4">
        <Notice tone="warning" title="Original packet bytes will be retained">
          The selected snapshot can include credentials or application data. This private artifact
          expires after 24 hours; the support export omits packet bytes and the filter tuple.
        </Notice>
        {error && <ErrorState error={error} />}
        <NetworkReadWarning
          error={interfaces.error}
          refresh={interfaces.refresh}
          lastSuccess={interfaces.lastSuccess}
          reading="native capture interfaces"
        />
        <FieldRow>
          <Field label="Capture name" htmlFor="capture-name">
            <Input
              id="capture-name"
              value={draft.name}
              onChange={(event) => change("name", event.target.value)}
              disabled={busy}
            />
          </Field>
          <Field label="Native interface" htmlFor="capture-interface">
            <Pick
              id="capture-interface"
              value={draft.interface}
              onChange={(value) => change("interface", value)}
              disabled={busy || !interfaces.data || Boolean(interfaces.error)}
              options={[
                { value: "unset", label: "Select an interface" },
                ...(interfaces.data ?? []).map((row) => ({
                  value: row.name,
                  label: `${row.name}${row.up ? "" : " · down"}`,
                  disabled: !row.up,
                })),
              ]}
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Address family" htmlFor="capture-family">
            <Pick
              id="capture-family"
              value={draft.family}
              onChange={(value) => {
                change("family", value as CaptureDraft["family"])
                if (draft.protocol === "icmp" || draft.protocol === "icmp6")
                  change("protocol", value === "inet" ? "icmp" : "icmp6")
              }}
              disabled={busy}
              options={[
                { value: "inet", label: "IPv4" },
                { value: "inet6", label: "IPv6" },
              ]}
            />
          </Field>
          <Field label="Protocol" htmlFor="capture-protocol">
            <Pick
              id="capture-protocol"
              value={draft.protocol}
              onChange={(value) => {
                change("protocol", value as CaptureDraft["protocol"])
                if (value !== "tcp" && value !== "udp") change("port", "")
              }}
              disabled={busy}
              options={[
                { value: "all", label: "All in this family" },
                { value: "tcp", label: "TCP" },
                { value: "udp", label: "UDP" },
                {
                  value: draft.family === "inet" ? "icmp" : "icmp6",
                  label: draft.family === "inet" ? "ICMP" : "ICMPv6",
                },
              ]}
            />
          </Field>
        </FieldRow>
        <FieldRow columns={3}>
          <Field label="Source address" htmlFor="capture-source" hint="Optional literal.">
            <Input
              id="capture-source"
              value={draft.source ?? ""}
              onChange={(event) => change("source", event.target.value)}
              disabled={busy}
              className="font-mono"
            />
          </Field>
          <Field label="Destination address" htmlFor="capture-destination" hint="Optional literal.">
            <Input
              id="capture-destination"
              value={draft.destination ?? ""}
              onChange={(event) => change("destination", event.target.value)}
              disabled={busy}
              className="font-mono"
            />
          </Field>
          <Field label="Port" htmlFor="capture-port" hint="Optional TCP or UDP.">
            <Input
              id="capture-port"
              value={draft.port}
              onChange={(event) => change("port", event.target.value)}
              inputMode="numeric"
              disabled={busy || !["tcp", "udp"].includes(draft.protocol)}
            />
          </Field>
        </FieldRow>
        <FieldRow columns={3}>
          <Field label="Packet limit" htmlFor="capture-packets" hint="1 to 10000.">
            <Input
              id="capture-packets"
              value={draft.packets}
              onChange={(event) => change("packets", event.target.value)}
              inputMode="numeric"
              disabled={busy}
            />
          </Field>
          <Field label="Time limit in seconds" htmlFor="capture-seconds" hint="1 to 120.">
            <Input
              id="capture-seconds"
              value={draft.seconds}
              onChange={(event) => change("seconds", event.target.value)}
              inputMode="numeric"
              disabled={busy}
            />
          </Field>
          <Field label="Artifact byte limit" htmlFor="capture-bytes" hint="Up to 2097152 bytes.">
            <Input
              id="capture-bytes"
              value={draft.maxBytes}
              onChange={(event) => change("maxBytes", event.target.value)}
              inputMode="numeric"
              disabled={busy}
            />
          </Field>
        </FieldRow>
        <Disclosure summary="Snapshot and incident">
          <FieldRow>
            <Field label="Bytes per packet" htmlFor="capture-snapshot">
              <Pick
                id="capture-snapshot"
                value={draft.snapshotLength}
                onChange={(value) => change("snapshotLength", value)}
                disabled={busy}
                options={[96, 128, 256, 512].map((value) => ({
                  value: String(value),
                  label: `${value} bytes`,
                }))}
              />
            </Field>
            <Field
              label="Related saved diagnostic"
              htmlFor="capture-incident"
              hint="An explicit reference; no diagnostic is rerun."
            >
              <Pick
                id="capture-incident"
                value={draft.incidentRunId ?? ""}
                onChange={(value) => change("incidentRunId", value)}
                disabled={busy || !incidents.data || Boolean(incidents.error)}
                options={[
                  { value: "unset", label: "No related diagnostic" },
                  ...(incidents.data ?? []).map((run) => ({ value: run.id, label: run.name })),
                ]}
              />
            </Field>
          </FieldRow>
          <NetworkReadWarning
            error={incidents.error}
            refresh={incidents.refresh}
            lastSuccess={incidents.lastSuccess}
            reading="saved diagnostic choices"
          />
        </Disclosure>
        {captureDraftProblem(draft) && (
          <p role="status" className="text-hint text-muted-foreground">
            {captureDraftProblem(draft)}
          </p>
        )}
      </div>
    </Modal>
  )
}
