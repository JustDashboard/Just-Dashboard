"use client"

import { useAuth } from "@/hooks/use-auth"
import { useState } from "react"
import { Information } from "@/components/icons"
import { del, post } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BBRState, ShapeDevice, ShapingView } from "@/lib/types"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormSection,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useConfirm } from "@/components/confirm-dialog"
import { LinkGlyph } from "@/components/network/marks"
import {
  formatKbit,
  guarded,
  kbitToMbit,
  limitProblem,
  mbitToKbit,
} from "@/components/network/traffic/shaping-math"

const QDISCS = [
  {
    value: "fq_codel",
    title: "fq_codel",
    description: "The default fair queue: keeps delay low when the link is full",
  },
  {
    value: "cake",
    title: "cake",
    description: "A fair queue with a shaper built in; the best fit once you set a limit",
  },
  { value: "fq", title: "fq", description: "Paces each flow; the queue BBR is meant to run over" },
  { value: "", title: "Kernel default", description: "Leave the queue the system chose" },
]

const num = (value: number) => value.toLocaleString()

/**
 * How fast each device may send and receive, and which queue holds the
 * packets while it waits.
 *
 * The table is framed because it is a table (§2): per shapeable device, the
 * queue it runs, the limits the dashboard set — upload is the device's egress,
 * download is policed on its ingress — and what the queue has dropped, held
 * back and is holding now, which is how a limit that bites is told from one
 * that is set and never reached. A drop is counted where the packets queue, so
 * the leaf's counters are read where there is a shaper and the root's where
 * there is not. A device that cannot be shaped says why in the place its
 * limits would be; one the dashboard shaped can be cleared.
 *
 * BBR is above it, as the one switch the kernel has for how TCP paces itself —
 * it is the host's, not a device's — with the algorithm now in force.
 */
export function Shaping({ view, onChanged }: { view: ShapingView; onChanged: () => void }) {
  const [editing, setEditing] = useState<string>()
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const device = view.devices.find((d) => d.name === editing)

  const clear = (d: ShapeDevice) =>
    confirm({
      title: `Clear the limits on ${d.name}`,
      confirmLabel: "Clear",
      description: (
        <p>
          <span className="font-mono">{d.name}</span> goes back to the kernel&rsquo;s own queue with
          no upload or download limit, and is not shaped again at boot.
        </p>
      ),
      action: async () => {
        await del(`/network/shaping/${encodeURIComponent(d.name)}`)
        onChanged()
      },
    })

  return (
    <Section title="Shaping">
      <BBRSwitch bbr={view.bbr} onChanged={onChanged} />
      <Panel>
        <PanelHeader
          title="Queues"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {plural(view.devices.length, "device")}
            </span>
          }
        />
        <PanelBody flush>
          <Table containerClassName="max-h-[28rem]">
            <TableHeader>
              <TableRow>
                <TableHead>Device</TableHead>
                <TableHead className="max-md:hidden">Queue</TableHead>
                <TableHead className="max-md:hidden">Upload</TableHead>
                <TableHead className="max-md:hidden">Download</TableHead>
                <TableHead className="text-right max-lg:hidden">Dropped</TableHead>
                <TableHead className="text-right max-lg:hidden">Over limit</TableHead>
                <TableHead className="text-right max-lg:hidden">Held</TableHead>
                <TableHead className="w-px">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {view.devices.map((d) => {
                const stat = d.leaf ?? d.root
                return (
                  <TableRow key={d.name}>
                    <TableCell className="min-w-44">
                      <span className="inline-flex items-center gap-2 font-mono font-medium">
                        <LinkGlyph kind={d.kind} className="size-3.5 text-muted-foreground" />
                        {d.name}
                      </span>
                      <DeviceNote device={d} stat={stat} />
                    </TableCell>
                    <TableCell className="font-mono max-md:hidden">{d.root?.kind ?? "—"}</TableCell>
                    <TableCell className="numeric whitespace-nowrap max-md:hidden">
                      {d.shapeable ? <Limit kbit={d.egressKbit} /> : "—"}
                    </TableCell>
                    <TableCell className="numeric whitespace-nowrap max-md:hidden">
                      {d.shapeable ? <Limit kbit={d.ingressKbit} /> : "—"}
                    </TableCell>
                    <TableCell className="numeric text-right max-lg:hidden">
                      {stat ? num(stat.drops) : "—"}
                    </TableCell>
                    <TableCell className="numeric text-right max-lg:hidden">
                      {stat ? num(stat.overlimits) : "—"}
                    </TableCell>
                    <TableCell className="numeric text-right whitespace-nowrap max-lg:hidden">
                      {stat ? bytes(stat.backlog) : "—"}
                    </TableCell>
                    <TableCell>
                      {can("system.admin") && d.shapeable && (
                        <span className="flex items-center justify-end gap-1.5">
                          <Button
                            size="xs"
                            variant="outline"
                            aria-label={`Edit the limits on ${d.name}`}
                            onClick={() => setEditing(d.name)}
                          >
                            Edit
                          </Button>
                          {d.managed && (
                            <Button
                              size="xs"
                              variant="ghost"
                              aria-label={`Clear the limits on ${d.name}`}
                              onClick={() => clear(d)}
                            >
                              Clear
                            </Button>
                          )}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                )
              })}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>
      {device && (
        <ShapeSheet
          key={device.name}
          device={device}
          qdiscs={view.qdiscs}
          onClose={() => setEditing(undefined)}
          onSaved={onChanged}
        />
      )}
      {dialog}
    </Section>
  )
}

function Limit({ kbit }: { kbit: number }) {
  return kbit > 0 ? (
    <span>{formatKbit(kbit)}</span>
  ) : (
    <span className="text-muted-foreground">none</span>
  )
}

/** The second line of a device's cell: what it is to this connection, why it cannot be shaped, and — narrow — its counters. */
function DeviceNote({ device, stat }: { device: ShapeDevice; stat: ShapeDevice["root"] }) {
  const facts = [device.uplink && "uplink", device.clientPath && "carries your connection"].filter(
    Boolean,
  )
  return (
    <>
      {facts.length > 0 && (
        <span className="mt-0.5 block text-hint text-muted-foreground">{facts.join(" · ")}</span>
      )}
      {device.verification && (
        <span className="mt-0.5 block max-w-96 text-hint font-normal whitespace-normal text-muted-foreground">
          {device.verification.status === "verified"
            ? "Kernel shaping matches saved limits"
            : device.verification.status === "drift"
              ? "Kernel shaping differs from saved limits"
              : "Kernel shaping could not be verified"}
          {device.verification.reason && ` · ${device.verification.reason}`}
        </span>
      )}
      {device.guard && (
        <span className="mt-0.5 flex max-w-96 items-start gap-1 text-hint font-normal whitespace-normal text-muted-foreground">
          <Information aria-hidden className="mt-0.5 size-3 shrink-0" />
          {device.guard}
        </span>
      )}
      {device.shapeable && (
        <span className="numeric mt-0.5 block text-hint text-muted-foreground md:hidden">
          {device.root?.kind ?? "no queue"} · up {formatKbit(device.egressKbit).toLowerCase()} ·
          down {formatKbit(device.ingressKbit).toLowerCase()}
        </span>
      )}
      {stat && (
        <span className="numeric mt-0.5 block text-hint text-muted-foreground lg:hidden">
          {num(stat.drops)} dropped · {bytes(stat.backlog)} held
        </span>
      )}
    </>
  )
}

/** The kernel's congestion control as the switch it is, with what is in force and what the host offers. */
function BBRSwitch({ bbr, onChanged }: { bbr: BBRState; onChanged: () => void }) {
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const toggle = async (on: boolean) => {
    setBusy(true)
    try {
      await post("/network/shaping/bbr", { on })
      notify.success(on ? "BBR is on" : "Back to the kernel's usual congestion control")
      onChanged()
    } catch (err) {
      notify.error("BBR was not changed", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <OptionList>
      <OptionRow
        title="Use BBR congestion control"
        hint={
          bbr.available ? (
            <>
              TCP paces itself by measured bandwidth instead of waiting for loss, over the fq queue.
              Now <span className="font-mono">{bbr.congestion}</span> over{" "}
              <span className="font-mono">{bbr.defaultQdisc}</span>.
            </>
          ) : (
            <>
              This kernel offers <span className="font-mono">{bbr.algorithms.join(" ")}</span>. Load
              it on the host with <span className="font-mono">modprobe tcp_bbr</span> to turn it on
              here.
            </>
          )
        }
        checked={bbr.active}
        disabled={!can("system.admin") || !bbr.available || busy}
        onCheckedChange={(on) => void toggle(on)}
      />
    </OptionList>
  )
}

/**
 * One device's queue and limits. It opens on the device, as a sheet that edits
 * a thing that exists does, then the queue as four cards — a choice between
 * kinds — and the two limits in megabits a second, which is what a person
 * knows their plan as; the route takes kilobits and the conversion is here.
 * Blank or zero is no limit.
 *
 * The uplink and the device this browser arrives through cannot be held under
 * a megabit: the form says so before it is asked, and the server's refusal,
 * which sees the live path, is drawn under the fields when it comes anyway.
 */
function ShapeSheet({
  device,
  qdiscs,
  onClose,
  onSaved,
}: {
  device: ShapeDevice
  qdiscs: string[]
  onClose: () => void
  onSaved: () => void
}) {
  const [qdisc, setQdisc] = useState(device.qdisc || device.root?.kind || "")
  const [upload, setUpload] = useState(kbitToMbit(device.egressKbit))
  const [download, setDownload] = useState(kbitToMbit(device.ingressKbit))
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const uploadProblem = limitProblem(upload, device)
  const downloadProblem = limitProblem(download, device)

  const submit = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await post(`/network/shaping/${encodeURIComponent(device.name)}`, {
        qdisc: qdiscs.includes(qdisc) ? qdisc : "",
        egressKbit: mbitToKbit(upload),
        ingressKbit: mbitToKbit(download),
      })
      notify.success(`${device.name} is shaped`)
      onSaved()
      onClose()
    } catch (err) {
      setRefusal(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="sm"
      title={<span className="font-mono">{device.name}</span>}
      description={`Queue and speed limits for ${device.name}`}
      footer={
        <>
          {guarded(device) && (
            <span className="mr-auto text-hint text-muted-foreground">
              At least 1 Mbit/s here:{" "}
              {device.uplink ? "this is the uplink" : "your connection arrives through it"}.
            </span>
          )}
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            onClick={() => void submit()}
            pending={busy}
            disabled={!can("system.admin") || busy || Boolean(uploadProblem || downloadProblem)}
          >
            Apply
          </Button>
        </>
      }
    >
      <form
        className="flex flex-col gap-6"
        onSubmit={(event) => {
          event.preventDefault()
          if (!uploadProblem && !downloadProblem && !busy) void submit()
        }}
      >
        <FormFacts>
          <FormFact label="Queue now" mono>
            {device.root?.kind ?? "none"}
          </FormFact>
          <FormFact label="Upload" mono>
            {formatKbit(device.egressKbit)}
          </FormFact>
          <FormFact label="Download" mono>
            {formatKbit(device.ingressKbit)}
          </FormFact>
        </FormFacts>

        <FormSection title="Queue">
          <ChoiceGrid columns={2}>
            {QDISCS.filter((q) => q.value === "" || qdiscs.includes(q.value)).map((q, index) => (
              <ChoiceCard
                key={q.value || "default"}
                verb={`Use ${q.title}`}
                title={q.title}
                description={q.description}
                selected={qdisc === q.value}
                onClick={() => setQdisc(q.value)}
                index={index}
              />
            ))}
          </ChoiceGrid>
        </FormSection>

        <FormSection title="Limits">
          <FieldRow>
            <Field
              label="Upload limit"
              htmlFor="shape-upload"
              hint="Blank is no limit."
              error={uploadProblem}
            >
              <MbitField id="shape-upload" value={upload} onChange={setUpload} />
            </Field>
            <Field
              label="Download limit"
              htmlFor="shape-download"
              hint="Dropped as it arrives, which slows TCP."
              error={downloadProblem}
            >
              <MbitField id="shape-download" value={download} onChange={setDownload} />
            </Field>
          </FieldRow>
          {refusal && (
            <p role="alert" className="animate-rise text-body text-destructive">
              {refusal}
            </p>
          )}
        </FormSection>
      </form>
    </SidePanel>
  )
}

function MbitField({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (next: string) => void
}) {
  return (
    <InputGroup>
      <InputGroupInput
        id={id}
        inputMode="decimal"
        autoComplete="off"
        placeholder="no limit"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="font-mono"
      />
      <InputGroupAddon align="inline-end">
        <InputGroupText>Mbit/s</InputGroupText>
      </InputGroupAddon>
    </InputGroup>
  )
}
