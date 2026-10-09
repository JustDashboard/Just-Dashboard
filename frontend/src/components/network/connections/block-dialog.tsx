"use client"

import { useState } from "react"
import { errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DiagnosticRun } from "@/lib/network-diagnostics"
import { BLOCK_DURATIONS, type AddressBlock } from "@/lib/network-traffic"
import { usePoll } from "@/hooks/use-poll"
import { Modal } from "@/components/modal"
import { Field, FormNote } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/** The incident choice: none, a new saved run, or an existing one by id. */
const NONE = "none"
const NEW = "new"

/**
 * Block a remote address from the connection it made, with a reason and an
 * end. The block is a source deny added through the firewall's guarded path —
 * the server refuses the address this browser arrives from — and the record
 * beside it keeps why, until when and which incident it belongs to; an ended
 * block is lifted by the server whether or not this page is open.
 *
 * The incident is a saved diagnostic run: an existing one, or a new one that
 * starts by recording who owns the address, so the block has evidence to
 * point at rather than a sentence.
 */
export function BlockDialog({
  address,
  context,
  onClose,
  onBlocked,
}: {
  address: string
  /** What the reader was looking at, offered as the reason. */
  context: string
  onClose: () => void
  onBlocked: (block: AddressBlock) => void
}) {
  const [reason, setReason] = useState(context)
  const [duration, setDuration] = useState(String(86400))
  const [incident, setIncident] = useState(NEW)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const runs = usePoll<DiagnosticRun[]>(
    (signal) => get("/network/diagnostics/", undefined, signal),
    0,
  )
  const problem =
    reason.trim().length === 0
      ? "Say why; the reason is what the record shows later."
      : reason.length > 200
        ? "Keep the reason under 200 characters."
        : undefined
  const submit = async () => {
    if (problem || busy) return
    setBusy(true)
    setError(undefined)
    try {
      let incidentRunId: string | undefined
      if (incident === NEW) {
        const run = await post<DiagnosticRun>("/network/diagnostics/", {
          name: `Blocked ${address}: ${reason.trim()}`.slice(0, 100),
          request: { tool: "asn", target: address },
        })
        incidentRunId = run.id
      } else if (incident !== NONE) {
        incidentRunId = incident
      }
      const block = await post<AddressBlock>("/firewall/blocks", {
        address,
        reason: reason.trim(),
        durationSeconds: Number(duration),
        ...(incidentRunId ? { incidentRunId } : {}),
      })
      notify.success(`${address} blocked`, {
        description:
          Number(duration) === 0
            ? "The deny sits in front of every allow until it is lifted."
            : "The deny sits in front of every allow and is lifted when it ends.",
      })
      onBlocked(block)
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={`Block ${address}`}
      description={`Deny ${address} at the firewall with a reason and an end`}
      footer={
        <>
          <FormNote className="mr-auto">
            Refused for the address this browser arrives from.
          </FormNote>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={() => void submit()}
            disabled={Boolean(problem) || busy}
            pending={busy}
          >
            Block {address}
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-5"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <Field label="Reason" htmlFor="block-reason" error={reason ? problem : undefined}>
          <Input
            id="block-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            maxLength={200}
          />
        </Field>
        <Field label="For">
          <ToggleGroup
            type="single"
            value={duration}
            onValueChange={(next) => next && setDuration(next)}
            variant="outline"
            size="sm"
            aria-label="How long the block lasts"
            className="flex-wrap"
          >
            {BLOCK_DURATIONS.map((d) => (
              <ToggleGroupItem key={d.value} value={String(d.value)} className="px-2.5 text-hint">
                {d.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
        <Field
          label="Incident"
          htmlFor="block-incident"
          hint="A saved diagnostic run the block belongs to; a new one records who owns the address."
        >
          <Select value={incident} onValueChange={setIncident}>
            <SelectTrigger id="block-incident" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NEW}>Open an incident: save who owns {address}</SelectItem>
              <SelectItem value={NONE}>No incident</SelectItem>
              {(runs.data ?? []).map((run) => (
                <SelectItem key={run.id} value={run.id}>
                  {run.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {error && (
          <p role="alert" className="text-body text-destructive">
            {error}
          </p>
        )}
      </form>
    </Modal>
  )
}
