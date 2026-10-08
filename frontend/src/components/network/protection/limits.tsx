"use client"

import { useState } from "react"
import { del, post, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ProtectionLimit } from "@/lib/types"
import { ShieldCheck } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { ProductLogo } from "@/components/product-logo"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import { Segments } from "@/components/deploy/settings/segments"
import { useAuth } from "@/hooks/use-auth"
import { compact } from "@/components/network/gateway/reading"
import {
  limitProduct,
  limitRequest,
  limitSentence,
  portsWord,
  type LimitRequest,
} from "@/components/network/protection/reading"

/**
 * Every rate limit as a lit card that opens its editor: the port it guards,
 * how many new connections it allows and to whom, what it does to the rest,
 * and how many packets it has refused since the table was loaded.
 */
export function LimitList({
  limits,
  onOpen,
  onChanged,
}: {
  limits: ProtectionLimit[]
  onOpen: (limit: ProtectionLimit) => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const { can } = useAuth()
  const [busy, setBusy] = useState<number>()
  const toggle = (limit: ProtectionLimit, enabled: boolean) => {
    if (!enabled) {
      confirm({
        title: `Switch off ${limit.name}`,
        confirmLabel: "Switch off",
        description: (
          <p>
            {portsWord(limit)} is no longer limited until it is switched on again:{" "}
            {limitSentence(limit)}. The limit is kept.
          </p>
        ),
        action: async () => {
          await put(`/network/protection/limits/${limit.id}`, limitRequest(limit, false))
          notify.success(`${limit.name} is off`)
          onChanged()
        },
      })
      return
    }
    setBusy(limit.id)
    put(`/network/protection/limits/${limit.id}`, limitRequest(limit, true))
      .then(() => {
        notify.success(`${limit.name} is on`)
        onChanged()
      })
      .catch((err) => notify.error(`${limit.name} was not changed`, err))
      .finally(() => setBusy(undefined))
  }
  return (
    <>
      <ChoiceList>
        {limits.map((limit, index) => (
          <ChoiceRow
            key={limit.id}
            index={index}
            leading={<ProductLogo id={limitProduct(limit)} size="sm" fallback={ShieldCheck} />}
            title={
              <span className={limit.enabled ? undefined : "text-muted-foreground"}>
                {limit.name}
              </span>
            }
            verb={`Edit ${limit.name}`}
            description={limitSentence(limit)}
            trailing={
              <span className="flex items-center gap-4">
                <span className="numeric hidden min-w-[4.5rem] text-right leading-tight sm:grid">
                  <span className="text-body font-medium">{compact(limit.packets)}</span>
                  <span className="font-mono text-micro text-muted-foreground">refused</span>
                </span>
                <Switch
                  checked={limit.enabled}
                  disabled={!can("system.admin") || busy === limit.id}
                  onCheckedChange={(next) => toggle(limit, next)}
                  aria-label={`${limit.name} in force`}
                />
              </span>
            }
            onSelect={() => onOpen(limit)}
          />
        ))}
      </ChoiceList>
      {dialog}
    </>
  )
}

const PROTOCOLS = [
  { value: "tcp", label: "TCP" },
  { value: "udp", label: "UDP" },
  { value: "both", label: "TCP and UDP" },
] as const

const PERS = [
  { value: "second", label: "a second" },
  { value: "minute", label: "a minute" },
  { value: "hour", label: "an hour" },
] as const

const ACTIONS = [
  { value: "drop", label: "Drop" },
  { value: "reject", label: "Reject" },
] as const

/** A figure typed into a number field, empty as zero: "no limit". */
const whole = (text: string) => (text.trim() === "" ? 0 : Number(text))

/**
 * The editor for one rate limit. Two things can be limited and either may be
 * left out: how fast new connections arrive (a rate, per second, minute or
 * hour, with a burst) and how many may be open at once; one of the two is
 * needed. "Count each address on its own" makes both per visitor, which is
 * what stops one scanner using everybody's allowance. Drop is silent; reject
 * tells the sender, which is kinder to a legitimate client and louder to a
 * scanner.
 */
export function LimitModal({
  limit,
  onOpenChange,
  onSaved,
}: {
  limit?: ProtectionLimit
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [name, setName] = useState(limit?.name ?? "")
  const [protocol, setProtocol] = useState<ProtectionLimit["protocol"]>(limit?.protocol ?? "tcp")
  const [ports, setPorts] = useState(limit?.ports ?? "")
  const [rate, setRate] = useState(limit?.rate ? String(limit.rate) : "")
  const [per, setPer] = useState<ProtectionLimit["per"]>(limit?.per || "minute")
  const [burst, setBurst] = useState(limit?.burst ? String(limit.burst) : "")
  const [perSource, setPerSource] = useState(limit?.perSource ?? true)
  const [max, setMax] = useState(limit?.maxConnections ? String(limit.maxConnections) : "")
  const [action, setAction] = useState<ProtectionLimit["action"]>(limit?.action ?? "drop")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const figures = [rate, burst, max].every((t) => t.trim() === "" || /^\d+$/.test(t.trim()))
  const limited = whole(rate) > 0 || whole(max) > 0
  const ready = name.trim() && ports.trim() && figures && limited
  const submit = async () => {
    setBusy(true)
    setError(undefined)
    const body: LimitRequest = {
      name: name.trim(),
      protocol,
      ports: ports.trim(),
      rate: whole(rate),
      per,
      burst: whole(rate) > 0 ? whole(burst) : 0,
      perSource,
      maxConnections: whole(max),
      action,
    }
    try {
      if (limit) await put(`/network/protection/limits/${limit.id}`, body)
      else await post("/network/protection/limits", body)
      notify.success(limit ? "Limit saved" : "Limit made", {
        description: "It is made again at every boot.",
      })
      onOpenChange(false)
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const remove = () => {
    if (!limit) return
    confirm({
      title: `Remove ${limit.name}`,
      confirmLabel: "Remove",
      description: <p>{portsWord(limit)} is no longer limited and the limit is forgotten.</p>,
      action: async () => {
        await del(`/network/protection/limits/${limit.id}`)
        notify.success("Limit removed")
        onOpenChange(false)
        onSaved()
      },
    })
  }
  return (
    <>
      <Modal
        open
        onOpenChange={(next) => !busy && onOpenChange(next)}
        title={limit ? limit.name : "New rate limit"}
        description="Slow down or cap the connections to a port"
        actions={
          limit &&
          can("destructive") && (
            <Button size="sm" variant="outline" onClick={remove}>
              Remove
            </Button>
          )
        }
        footer={
          <>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              type="submit"
              form="limit-form"
              disabled={!can("system.admin") || !ready || busy}
              pending={busy}
            >
              {limit ? "Save limit" : "Make limit"}
            </Button>
          </>
        }
      >
        <form
          id="limit-form"
          className="flex min-w-0 flex-col gap-5"
          onSubmit={(event) => {
            event.preventDefault()
            if (ready && !busy) void submit()
          }}
        >
          <Field label="Name" htmlFor="limit-name" hint="What it protects: SSH, the website">
            <Input
              id="limit-name"
              value={name}
              placeholder="SSH"
              onChange={(event) => setName(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <FieldRow>
            <Field label="Protocol">
              <Segments
                label="Protocol"
                value={protocol}
                options={[...PROTOCOLS]}
                onChange={setProtocol}
                fill
              />
            </Field>
            <Field label="Ports" htmlFor="limit-ports" hint="One port, or a range like 8000-8010">
              <Input
                id="limit-ports"
                value={ports}
                inputMode="numeric"
                placeholder="22"
                onChange={(event) => setPorts(event.target.value)}
                className="font-mono"
                autoComplete="off"
              />
            </Field>
          </FieldRow>
          <FieldRow columns={3}>
            <Field label="New connections" htmlFor="limit-rate" hint="Empty for no rate">
              <Input
                id="limit-rate"
                value={rate}
                inputMode="numeric"
                placeholder="10"
                onChange={(event) => setRate(event.target.value)}
                autoComplete="off"
              />
            </Field>
            <Field label="Per">
              <Segments label="Per" value={per} options={[...PERS]} onChange={setPer} fill />
            </Field>
            <Field label="Burst" htmlFor="limit-burst" hint="Extra allowed at once">
              <Input
                id="limit-burst"
                value={burst}
                inputMode="numeric"
                placeholder="5"
                disabled={whole(rate) === 0}
                onChange={(event) => setBurst(event.target.value)}
                autoComplete="off"
              />
            </Field>
          </FieldRow>
          <Field
            label="Open at once"
            htmlFor="limit-max"
            hint="The most connections that may be open together. Empty for no ceiling."
          >
            <Input
              id="limit-max"
              value={max}
              inputMode="numeric"
              placeholder="50"
              onChange={(event) => setMax(event.target.value)}
              autoComplete="off"
            />
          </Field>
          <OptionRow
            title="Count each address on its own"
            hint="One scanner cannot use up everybody's allowance."
            checked={perSource}
            onCheckedChange={setPerSource}
          />
          <Field label="Over the limit">
            <Segments
              label="Action"
              value={action}
              options={[...ACTIONS]}
              onChange={setAction}
              fill
            />
          </Field>
          {!limited && (name || ports) && (
            <p className="text-hint text-muted-foreground">
              A limit needs a rate, a number open at once, or both.
            </p>
          )}
          {error && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {error}
            </p>
          )}
        </form>
      </Modal>
      {dialog}
    </>
  )
}
