"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { noteProblem, preferredSourceProblem } from "@/components/network/draft-input"
import type { NetworkLink, NetworkRouting } from "@/lib/types"
import { Modal } from "@/components/modal"
import { Field, FieldRow } from "@/components/form"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const NONE = "__none"

/**
 * A static route: where to, by which hop or device, in which table. The
 * server applies it, asks the kernel again how it would answer this browser,
 * and takes it back if the answer moved — so the worst a wrong route here
 * does is be refused with the sentence saying where the reply would have gone.
 */
export function AddRoute({
  open,
  onOpenChange,
  routing,
  links,
  onAdded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onAdded: () => void
}) {
  const [destination, setDestination] = useState("")
  const [type, setType] = useState("unicast")
  const [gateway, setGateway] = useState("")
  const [device, setDevice] = useState(NONE)
  const [table, setTable] = useState("254")
  const [metric, setMetric] = useState("")
  const [source, setSource] = useState("")
  const [comment, setComment] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const discards = type !== "unicast"
  const sourceError = discards ? undefined : preferredSourceProblem(source, destination, gateway)
  const commentError = noteProblem(comment)
  const ready =
    !sourceError &&
    !commentError &&
    destination.trim() &&
    (discards || gateway.trim() || device !== NONE)
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/routing/routes", {
        destination: destination.trim(),
        type,
        gateway: discards ? undefined : gateway.trim() || undefined,
        device: discards || device === NONE ? undefined : device,
        table: Number(table) || 254,
        metric: metric ? Number(metric) : undefined,
        source: discards ? undefined : source.trim() || undefined,
        comment: comment.trim() || undefined,
      })
      notify.success("Route added", { description: "It is made again at every boot." })
      onOpenChange(false)
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  const tables = routing.tables.filter((t) => t.id !== 52 && t.id !== 255 && t.id !== 253)
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add a route"
      description="Send traffic for a network by a chosen hop or device"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            Add route
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <FieldRow>
          <Field
            label="Destination"
            htmlFor="route-dest"
            hint="An IPv4 or IPv6 network in CIDR form, or default"
          >
            <Input
              id="route-dest"
              value={destination}
              placeholder="192.168.60.0/24"
              onChange={(event) => setDestination(event.target.value)}
              className="font-mono"
            />
          </Field>
          <Field label="Kind" htmlFor="route-type">
            <Select value={type} onValueChange={setType}>
              <SelectTrigger id="route-type" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="unicast">Send it on</SelectItem>
                <SelectItem value="blackhole">Drop it silently (blackhole)</SelectItem>
                <SelectItem value="unreachable">Refuse: unreachable</SelectItem>
                <SelectItem value="prohibit">Refuse: prohibited</SelectItem>
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        {!discards && (
          <FieldRow>
            <Field label="Via" htmlFor="route-via" hint="The next hop's address, if there is one">
              <Input
                id="route-via"
                value={gateway}
                placeholder="10.8.0.2"
                onChange={(event) => setGateway(event.target.value)}
                className="font-mono"
              />
            </Field>
            <Field label="Device" htmlFor="route-dev">
              <Select value={device} onValueChange={setDevice}>
                <SelectTrigger id="route-dev" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>Whichever reaches the hop</SelectItem>
                  {links
                    .filter((l) => l.role !== "container" && l.owner !== "kernel")
                    .map((l) => (
                      <SelectItem key={l.name} value={l.name}>
                        {l.name}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
        )}
        {!discards && (
          <Field
            label="Preferred source"
            htmlFor="route-source"
            hint="Optional: the local address used for traffic this server sends over the route. Leave empty for the kernel to choose."
            error={sourceError}
          >
            <Input
              id="route-source"
              value={source}
              onChange={(event) => setSource(event.target.value)}
              aria-invalid={Boolean(sourceError)}
              placeholder={gateway.includes(":") ? "2001:db8::1" : "192.168.60.1"}
              className="font-mono"
            />
          </Field>
        )}
        <FieldRow>
          <Field
            label="Table"
            htmlFor="route-table"
            hint="main, or a number for a policy rule to pick"
          >
            <Input
              id="route-table"
              list="route-tables"
              inputMode="numeric"
              value={table}
              onChange={(event) => setTable(event.target.value)}
            />
            <datalist id="route-tables">
              {tables.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.name}
                </option>
              ))}
            </datalist>
          </Field>
          <Field label="Metric" htmlFor="route-metric" hint="Lower wins between equal routes">
            <Input
              id="route-metric"
              inputMode="numeric"
              value={metric}
              placeholder="100"
              onChange={(event) => setMetric(event.target.value)}
            />
          </Field>
        </FieldRow>
        <Field
          label="Note"
          htmlFor="route-comment"
          hint="Optional: what it is for"
          error={commentError}
        >
          <Input
            id="route-comment"
            value={comment}
            aria-invalid={Boolean(commentError)}
            placeholder="office LAN through wg0"
            onChange={(event) => setComment(event.target.value)}
          />
        </Field>
        {routing.clientPath.device && (
          <Notice title="Checked against your connection">
            Your browser is answered through{" "}
            <span className="font-mono">{routing.clientPath.device}</span>
            {routing.clientPath.gateway && (
              <>
                {" "}
                via <span className="font-mono">{routing.clientPath.gateway}</span>
              </>
            )}
            . A route that would move that reply is taken back the moment it is applied.
          </Notice>
        )}
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}

/**
 * A policy rule: which traffic, and the table it is looked up in. Its
 * priority is picked from the dashboard's own range unless one is given, so
 * it never lands between the rules the system and Tailscale wrote.
 */
export function AddRule({
  open,
  onOpenChange,
  routing,
  links,
  onAdded,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  routing: NetworkRouting
  links: NetworkLink[]
  onAdded: () => void
}) {
  const [family, setFamily] = useState("inet")
  const [from, setFrom] = useState("")
  const [to, setTo] = useState("")
  const [iif, setIif] = useState(NONE)
  const [oif, setOif] = useState(NONE)
  const [fwmark, setFwmark] = useState("")
  const [action, setAction] = useState("lookup")
  const [table, setTable] = useState("")
  const [priority, setPriority] = useState("")
  const [comment, setComment] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  const commentError = noteProblem(comment)
  const selects = from.trim() || to.trim() || iif !== NONE || oif !== NONE || fwmark.trim()
  const ready = !commentError && selects && (action !== "lookup" || table.trim())
  const submit = async () => {
    if (!ready || busy) return
    setBusy(true)
    setError(undefined)
    try {
      await post("/network/routing/rules", {
        family,
        from: from.trim() || undefined,
        to: to.trim() || undefined,
        iif: iif === NONE ? undefined : iif,
        oif: oif === NONE ? undefined : oif,
        fwmark: fwmark.trim() || undefined,
        action,
        table: action === "lookup" ? Number(table) : undefined,
        priority: priority ? Number(priority) : undefined,
        comment: comment.trim() || undefined,
      })
      notify.success("Rule added", { description: "It is made again at every boot." })
      onOpenChange(false)
      onAdded()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title="Add a policy rule"
      description="Send a kind of traffic to its own routing table"
      footer={
        <>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={!ready || busy} pending={busy}>
            Add rule
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-5">
        <Field label="Address family" htmlFor="rule-family">
          <Select value={family} onValueChange={setFamily}>
            <SelectTrigger id="rule-family">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="inet">IPv4</SelectItem>
              <SelectItem value="inet6">IPv6</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        <FieldRow>
          <Field label="From" htmlFor="rule-from" hint="Traffic from this network">
            <Input
              id="rule-from"
              value={from}
              placeholder={family === "inet6" ? "2001:db8::/64" : "10.8.0.0/24"}
              onChange={(event) => setFrom(event.target.value)}
              className="font-mono"
            />
          </Field>
          <Field label="To" htmlFor="rule-to" hint="Traffic to this network">
            <Input
              id="rule-to"
              value={to}
              placeholder="any"
              onChange={(event) => setTo(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <Field
          label="Leaving on"
          htmlFor="rule-oif"
          hint="Optional: the outgoing device for locally generated traffic"
        >
          <Select value={oif} onValueChange={setOif}>
            <SelectTrigger id="rule-oif" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>Any device</SelectItem>
              {links
                .filter((link) => link.role !== "container" && link.owner !== "kernel")
                .map((link) => (
                  <SelectItem key={link.name} value={link.name}>
                    {link.name}
                  </SelectItem>
                ))}
            </SelectContent>
          </Select>
        </Field>
        <FieldRow>
          <Field label="Arriving on" htmlFor="rule-iif">
            <Select value={iif} onValueChange={setIif}>
              <SelectTrigger id="rule-iif" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>Any device</SelectItem>
                {links
                  .filter((l) => l.role !== "container" && l.owner !== "kernel")
                  .map((l) => (
                    <SelectItem key={l.name} value={l.name}>
                      {l.name}
                    </SelectItem>
                  ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Firewall mark" htmlFor="rule-fwmark" hint="Optional, such as 0x10/0xff">
            <Input
              id="rule-fwmark"
              value={fwmark}
              placeholder="0x10"
              onChange={(event) => setFwmark(event.target.value)}
              className="font-mono"
            />
          </Field>
        </FieldRow>
        <FieldRow>
          <Field label="Then" htmlFor="rule-action">
            <Select value={action} onValueChange={setAction}>
              <SelectTrigger id="rule-action" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="lookup">Look it up in a table</SelectItem>
                <SelectItem value="blackhole">Drop it silently</SelectItem>
                <SelectItem value="unreachable">Refuse: unreachable</SelectItem>
                <SelectItem value="prohibit">Refuse: prohibited</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {action === "lookup" ? (
            <Field label="Table" htmlFor="rule-table" hint="The number of the table to use">
              <Input
                id="rule-table"
                inputMode="numeric"
                value={table}
                placeholder="100"
                onChange={(event) => setTable(event.target.value)}
              />
            </Field>
          ) : (
            <div />
          )}
        </FieldRow>
        <Field
          label="Priority"
          htmlFor="rule-priority"
          hint={`Optional: ${routing.rulePriorities.min}–${routing.rulePriorities.max}; the first free one otherwise`}
        >
          <Input
            id="rule-priority"
            inputMode="numeric"
            value={priority}
            onChange={(event) => setPriority(event.target.value)}
          />
        </Field>
        <Field
          label="Note"
          htmlFor="rule-comment"
          hint="Optional: what this rule is for"
          error={commentError}
        >
          <Input
            id="rule-comment"
            value={comment}
            aria-invalid={Boolean(commentError)}
            onChange={(event) => setComment(event.target.value)}
          />
        </Field>
        {!selects && (
          <Notice title="A rule needs something to match">
            A rule for all traffic would send this browser&rsquo;s replies through the table it
            names too; give it a source, a destination, a device or a mark.
          </Notice>
        )}
        {error && (
          <p role="alert" className="animate-rise text-body text-destructive">
            {error}
          </p>
        )}
      </div>
    </Modal>
  )
}
