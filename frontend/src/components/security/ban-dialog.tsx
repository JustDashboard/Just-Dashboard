"use client"

import { useState } from "react"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { BlocksView } from "@/lib/types"
import { Field } from "@/components/form"
import { Modal } from "@/components/modal"
import { heldSentence } from "@/components/security/blocks"
import { boundaryVerdict } from "@/components/security/boundary"
import { BoundaryImpacts, useBoundaryCheck } from "@/components/security/boundary-view"
import { useSecurity } from "@/components/security/security-context"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/** Who makes the ban: CrowdSec, or one of fail2ban's jails by name. */
export type BanTarget = { engine: "crowdsec" } | { engine: "fail2ban"; jail: string }

export const banKey = (target: BanTarget) =>
  target.engine === "crowdsec" ? "crowdsec" : `jail:${target.jail}`

const banName = (target: BanTarget) =>
  target.engine === "crowdsec" ? "CrowdSec" : `fail2ban · ${target.jail}`

/** Hours, because that is the one unit CrowdSec's route reads for every length. */
const DURATIONS = [
  { value: "1h", label: "1 hour" },
  { value: "4h", label: "4 hours" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
]

/**
 * A ban by hand, through whichever engine the reader picks.
 *
 * It was two forms: CrowdSec's dialog on the page and a field inside each
 * jail's sheet, three presses in. They ask the same question (which address)
 * and answer it the same way (what already holds it, what it does to the way
 * the dashboard is reached), so it is one form with the engine as its first
 * field, opened by the page's one command. CrowdSec takes a length and a
 * reason, which becomes the scenario its decisions show a month later; a jail
 * holds the address for its own ban time, as it does an earned ban.
 *
 * The server refuses a ban that covers this browser's own address, and says
 * so beside the field it is about rather than in a toast.
 */
export function BanDialog({
  open,
  onOpenChange,
  targets,
  initial,
  blocks,
  onBanned,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The engines that can take a ban right now, in the order they are offered. */
  targets: BanTarget[]
  /** The target the form opens on: the view the reader is in. */
  initial?: string
  blocks?: BlocksView
  onBanned: () => void
}) {
  if (!open || targets.length === 0) return null
  return (
    <BanForm
      targets={targets}
      initial={initial}
      blocks={blocks}
      onOpenChange={onOpenChange}
      onBanned={onBanned}
    />
  )
}

function BanForm({
  targets,
  initial,
  blocks,
  onOpenChange,
  onBanned,
}: {
  targets: BanTarget[]
  initial?: string
  blocks?: BlocksView
  onOpenChange: (open: boolean) => void
  onBanned: () => void
}) {
  const { exposure } = useSecurity()
  const [chosen, setChosen] = useState(
    targets.some((target) => banKey(target) === initial) ? initial! : banKey(targets[0]),
  )
  const target = targets.find((candidate) => banKey(candidate) === chosen) ?? targets[0]
  const [value, setValue] = useState("")
  const [duration, setDuration] = useState("4h")
  const [reason, setReason] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<{ field: boolean; message: string }>()
  const typed = value.trim()
  const boundary = useBoundaryCheck(typed ? { kind: "ban", target: typed } : undefined)
  const crossing = boundaryVerdict(boundary.impacts ?? [])
  const held = heldSentence(value, blocks)

  const submit = async () => {
    setBusy(true)
    setError(undefined)
    const acknowledged = crossing === "acknowledge" && { acknowledgeBoundary: true }
    try {
      if (target.engine === "crowdsec") {
        await post("/security/crowdsec/decisions", {
          value: typed,
          duration,
          reason: reason.trim() || undefined,
          ...acknowledged,
        })
        notify.success(`${typed} banned for ${DURATIONS.find((d) => d.value === duration)?.label}`)
      } else {
        await post(`/fail2ban/${encodeURIComponent(target.jail)}/ban`, {
          ip: typed,
          ...acknowledged,
        })
        notify.success(`${typed} banned in ${target.jail}`)
      }
      onBanned()
      onOpenChange(false)
    } catch (err) {
      const refusal = err instanceof ApiError ? err : undefined
      setError({
        field:
          refusal?.code === "would_lock_you_out" ||
          refusal?.field === "value" ||
          refusal?.field === "ip",
        message: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="Ban an address"
      description="Refuses an address through CrowdSec or one of fail2ban's jails."
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="intrusion-ban"
            pending={busy}
            disabled={busy || !typed || boundary.checking || crossing === "refused"}
          >
            Ban
          </Button>
        </>
      }
    >
      <form
        id="intrusion-ban"
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault()
          if (typed && !boundary.checking && crossing !== "refused") void submit()
        }}
      >
        {targets.length > 1 && (
          <Field label="Banned by" htmlFor="ban-engine">
            <Select value={chosen} onValueChange={setChosen}>
              <SelectTrigger id="ban-engine" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {targets.map((candidate) => (
                  <SelectItem key={banKey(candidate)} value={banKey(candidate)}>
                    {banName(candidate)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        <Field
          label={target.engine === "crowdsec" ? "Address or range" : "Address"}
          htmlFor="ban-value"
          hint={
            target.engine === "crowdsec"
              ? exposure?.client
                ? `One address or a range such as 203.0.113.0/24. It cannot cover yours, ${exposure.client}.`
                : "One address or a range such as 203.0.113.0/24."
              : `Held for ${target.jail}'s ban time, as an earned ban is. Your own address is refused.`
          }
          error={error?.field ? error.message : undefined}
        >
          <Input
            id="ban-value"
            aria-describedby={held ? "ban-value-held" : undefined}
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="203.0.113.77"
            className="font-mono"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={error?.field || undefined}
          />
          {held && (
            <p id="ban-value-held" className="mt-1.5 text-hint text-warning">
              {held}
            </p>
          )}
        </Field>
        <BoundaryImpacts impacts={boundary.impacts} />
        {target.engine === "crowdsec" && (
          <>
            <Field label="For how long" htmlFor="ban-duration">
              <Select value={duration} onValueChange={setDuration}>
                <SelectTrigger id="ban-duration" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {DURATIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Reason"
              htmlFor="ban-reason"
              hint="Optional. It becomes the scenario the decisions show."
            >
              <Input
                id="ban-reason"
                value={reason}
                onChange={(event) => setReason(event.target.value)}
                placeholder="scanning the admin panel"
                autoComplete="off"
              />
            </Field>
          </>
        )}
        {error && !error.field && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error.message}
          </p>
        )}
      </form>
    </Modal>
  )
}
