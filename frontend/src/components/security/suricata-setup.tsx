"use client"

import { useState } from "react"
import { post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { Job, SuricataView } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { JobConsole, useJobConsole } from "@/components/job-console"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

/**
 * What installing Suricata leaves undone, in the order it has to be done: the
 * interface it captures on, whether that capture sees any packets, the rules
 * it matches them against, and the service running. Each step is a job whose
 * output streams beneath, and each reads again when it finishes.
 *
 * The inline queue is the one part only read. A queue rule without bypass
 * drops whatever it queues while Suricata is not reading — a restart, a crash
 * — and that includes the dashboard's own traffic if it is queued, so the
 * page says which rules there are and whether they fail open, and changes
 * none of them.
 */
export function SuricataSetup({ data, onChanged }: { data: SuricataView; onChanged: () => void }) {
  const console_ = useJobConsole({ onSuccess: onChanged })
  const { confirm, dialog } = useConfirm()
  const configured = data.interfaces.configured[0]
  const [iface, setIface] = useState<string>()
  const chosen = iface ?? configured
  const capture = data.capture
  const seeing = capture ? capture.kernelPackets > 0 || capture.decoderPackets > 0 : undefined

  const start = async (path: string, what: string, body?: unknown) => {
    try {
      console_.attach(await post<Job>(path, body ?? {}))
    } catch (err) {
      notify.error(`Could not start ${what}`, err)
    }
  }
  const moveCapture = () =>
    confirm({
      title: `Capture on ${chosen}`,
      confirmLabel: "Test and move",
      description: (
        <p>
          suricata.yaml&rsquo;s af-packet interface changes from{" "}
          <span className="font-mono">{configured}</span> to{" "}
          <span className="font-mono">{chosen}</span>, is tested with{" "}
          <span className="font-mono">suricata -T</span> and, where Suricata runs, restarted onto
          it. The previous file is put back if the test fails or Suricata does not come back.
        </p>
      ),
      action: async () => {
        await start("/security/suricata/interface", "moving the capture", { interface: chosen })
      },
    })

  return (
    <>
      <Panel plain>
        <PanelHeader title="Setup" />
        <PanelBody flush>
          <RowList>
            <Row
              title="Captures on"
              subtitle={
                data.interfaces.editable
                  ? `af-packet in suricata.yaml · ${data.interfaces.configured.join(", ")}`
                  : (data.interfaces.reason ?? "")
              }
              className="py-2.5"
              trailing={
                data.interfaces.editable ? (
                  <span className="flex items-center gap-2">
                    <Select value={chosen} onValueChange={setIface}>
                      <SelectTrigger className="w-36" aria-label="Capture interface">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {[...new Set([configured, ...data.interfaces.candidates])].map((name) => (
                          <SelectItem key={name} value={name}>
                            {name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Button
                      size="xs"
                      variant="outline"
                      disabled={chosen === configured || console_.running}
                      onClick={moveCapture}
                    >
                      Move capture
                    </Button>
                  </span>
                ) : (
                  <span className="font-mono text-hint text-muted-foreground">
                    {data.interfaces.configured.join(", ") || "—"}
                  </span>
                )
              }
            />
            <Row
              title="Seeing packets"
              subtitle={
                capture
                  ? `${plural(capture.kernelPackets, "packet")} captured, ${plural(capture.kernelDrops, "drop")} in the kernel, over ${Math.round(capture.uptimeSeconds / 60)} min · stats ${relativeTime(capture.at)}`
                  : "No stats event in eve.json yet, so whether the capture sees anything is unknown."
              }
              className="py-2.5"
              trailing={
                <Status
                  verdict={seeing === undefined ? "notice" : seeing ? "ok" : "warning"}
                  label={
                    seeing === undefined ? "unknown" : seeing ? "capturing" : "nothing captured"
                  }
                />
              }
            />
            <Row
              title="Rules"
              subtitle={
                data.rulesLoaded === undefined
                  ? `${data.rules.file} could not be read`
                  : `${plural(data.rulesLoaded, "rule")} enabled${data.rules.updatedAt ? ` · fetched ${relativeTime(data.rules.updatedAt)}` : ""}${data.rules.sources.length > 0 ? ` · ${data.rules.sources.join(", ")}` : ""}`
              }
              className="py-2.5"
              trailing={
                data.rules.updater ? (
                  <Button
                    size="xs"
                    variant="outline"
                    disabled={console_.running}
                    onClick={() => void start("/security/suricata/rules/update", "the rule update")}
                  >
                    Update rules
                  </Button>
                ) : (
                  <span className="text-hint text-muted-foreground">
                    install <span className="font-mono">suricata-update</span>
                  </span>
                )
              }
            />
            <Row
              title="Running"
              subtitle={
                data.active ? "The service is active." : "The service is installed and stopped."
              }
              className="py-2.5"
              trailing={
                data.active ? (
                  <Status tone="running" label="active" />
                ) : (
                  <Button
                    size="xs"
                    disabled={console_.running}
                    onClick={() => void start("/security/suricata/start", "Suricata")}
                  >
                    Start Suricata
                  </Button>
                )
              }
            />
          </RowList>
          <JobConsole
            job={console_.job}
            lines={console_.lines}
            onDismiss={console_.dismiss}
            onCancel={console_.cancel}
            className="mt-3"
          />
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader title="Inline queue" />
        <PanelBody flush>
          <Notice
            tone={data.inline.queues.length > 0 && !data.inline.failOpen ? "warning" : "default"}
            title={
              data.inline.queues.length === 0
                ? "Nothing is queued to Suricata"
                : data.inline.failOpen
                  ? "Every queue fails open"
                  : "A queue fails closed"
            }
          >
            <p>{data.inline.words}</p>
            <p className="mt-1">
              Queue rules are read here and never changed: switching to inline mode decides what
              reaches every port on this host, the dashboard&rsquo;s included, and belongs to
              Suricata&rsquo;s own configuration and the firewall that owns those rules.
            </p>
            {data.inline.error && <p className="mt-1">{data.inline.error}</p>}
          </Notice>
          {data.inline.queues.length > 0 && (
            <RowList className="mt-2">
              {data.inline.queues.map((queue) => (
                <Row
                  key={`${queue.source}-${queue.rule}`}
                  title={`${queue.chain} → queue ${queue.queue}`}
                  subtitle={queue.rule}
                  mono
                  className="py-2"
                  trailing={
                    <Status
                      verdict={queue.bypass ? "ok" : "warning"}
                      label={queue.bypass ? "fails open" : "fails closed"}
                    />
                  }
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}
