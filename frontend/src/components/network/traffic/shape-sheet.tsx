"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { ApiError, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { ShapeDevice } from "@/lib/types"
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
import { SidePanel } from "@/components/side-panel"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { CakeTins, QueueEvidence, QueueParameters } from "./queue-evidence"
import { formatKbit, guarded, kbitToMbit, limitProblem, mbitToKbit } from "./shaping-math"
import { SQMFields } from "./sqm-fields"
import { sqmDraft, sqmProblem, sqmProfile } from "./sqm-profile"
import { UploadFields } from "./upload-fields"
import { uploadDraft, uploadProblem as uploadProfileProblem, uploadProfile } from "./upload-profile"
import { queueState } from "./words"

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

/**
 * One device's queue and limits, and the evidence for them.
 *
 * It opens on the device, as a sheet that edits a thing that exists does:
 * the queue it runs and its limits now, then what the kernel says of them —
 * what the queue dropped, held back and is holding, whether it still matches
 * what was saved, the CAKE profile and the delay CAKE measured, the download
 * queue's own counters and what the card does to packets before it. That
 * evidence was up to nine wrapped lines in the device's cell of a table; a
 * queue's card is one line now and this is where the rest is read, by an
 * account that may change nothing as well.
 *
 * Under it, for an administrator and a device that can be shaped, the queue
 * as four cards — a choice between kinds — and the two limits in megabits a
 * second, which is what a person knows their plan as; the route takes
 * kilobits and the conversion is here. Blank or zero is no limit.
 *
 * The uplink and the device this browser arrives through cannot be held under
 * a megabit: the form says so before it is asked, and the server's refusal,
 * which sees the live path, is drawn under the fields when it comes anyway.
 */
export function ShapeSheet({
  device,
  qdiscs,
  onClose,
  onSaved,
  stale,
}: {
  device: ShapeDevice
  qdiscs: string[]
  onClose: () => void
  onSaved: () => void
  stale: boolean
}) {
  const [qdisc, setQdisc] = useState(device.qdisc || device.root?.kind || "")
  const [upload, setUpload] = useState(kbitToMbit(device.egressKbit))
  const [download, setDownload] = useState(kbitToMbit(device.ingressKbit))
  const [downloadSQM, setDownloadSQM] = useState(Boolean(device.sqm))
  const [profile, setProfile] = useState(() => sqmDraft(device.sqm))
  const [uploadCake, setUploadCake] = useState(Boolean(device.upload))
  const [upProfile, setUpProfile] = useState(() => uploadDraft(device.upload))
  const { can } = useAuth()
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const editable = can("system.admin") && device.shapeable
  const uploadProblem = limitProblem(upload, device)
  const downloadProblem = limitProblem(download, device)
  const profileProblem = downloadSQM ? sqmProblem(profile, mbitToKbit(download)) : undefined
  // A profile shapes CAKE's own upload limit; with another queue or no limit
  // there is nothing for it to describe.
  const profileOffered = qdisc === "cake" && mbitToKbit(upload) > 0
  const upProblem = profileOffered && uploadCake ? uploadProfileProblem(upProfile) : undefined
  const refused = device.ownership?.verdict === "refused" && !device.managed
  const state = queueState(device)

  const submit = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await post(
        `/network/shaping/${encodeURIComponent(device.name)}`,
        {
          qdisc: qdiscs.includes(qdisc) ? qdisc : "",
          egressKbit: mbitToKbit(upload),
          ingressKbit: mbitToKbit(download),
          ...(downloadSQM ? { sqm: sqmProfile(profile) } : {}),
          ...(profileOffered && uploadCake ? { upload: uploadProfile(upProfile) } : {}),
        },
        { ...(downloadSQM || device.sqm ? { networkApply: "pending" as const } : {}) },
      )
      notify.success(
        downloadSQM || device.sqm
          ? `${device.name} applied; reconnect and confirm the network change`
          : `${device.name} is shaped`,
      )
      onSaved()
      onClose()
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      const uncertain =
        (downloadSQM || device.sqm) &&
        ((err instanceof ApiError && err.status >= 500) || err instanceof TypeError)
      setRefusal(
        uncertain
          ? `${message} The outcome may be uncertain. Inspect network change status before retrying.`
          : message,
      )
    } finally {
      setBusy(false)
    }
  }

  const blocked =
    stale ||
    busy ||
    refused ||
    Boolean(uploadProblem || downloadProblem || profileProblem || upProblem)

  return (
    <SidePanel
      open
      onOpenChange={(open) => !open && onClose()}
      width="sm"
      // The first field holds the queue in force; landing in it would read
      // as a choice already being made.
      initialFocus="body"
      title={
        <>
          <span className="font-mono">{device.name}</span>
          <Status tone={state.tone} label={state.label} />
        </>
      }
      description={
        editable
          ? `Queue and speed limits for ${device.name}`
          : `The queue on ${device.name} and its evidence`
      }
      footer={
        editable ? (
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
            <Button onClick={() => void submit()} pending={busy} disabled={blocked}>
              Apply
            </Button>
          </>
        ) : (
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        )
      }
    >
      <form
        className="flex flex-col gap-6"
        onSubmit={(event) => {
          event.preventDefault()
          if (editable && !blocked) void submit()
        }}
      >
        <FormFacts>
          <FormFact label="Queue now" mono>
            {device.root?.kind ?? "none"}
          </FormFact>
          <FormFact label="Upload" mono>
            {device.shapeable ? formatKbit(device.egressKbit) : "—"}
          </FormFact>
          <FormFact label="Download" mono>
            {device.shapeable ? formatKbit(device.ingressKbit) : "—"}
          </FormFact>
        </FormFacts>

        <QueueEvidence device={device} />
        <QueueParameters device={device} />

        {editable && (
          <>
            <FormSection title="Queue">
              <ChoiceGrid columns={2}>
                {QDISCS.filter((q) => q.value === "" || qdiscs.includes(q.value)).map(
                  (q, index) => (
                    <ChoiceCard
                      key={q.value || "default"}
                      verb={`Use ${q.title}`}
                      title={q.title}
                      description={q.description}
                      selected={qdisc === q.value}
                      onClick={() => setQdisc(q.value)}
                      index={index}
                    />
                  ),
                )}
              </ChoiceGrid>
            </FormSection>

            <FormSection title="Limits">
              <FieldRow>
                <Field
                  label="Upload limit"
                  htmlFor="shape-upload"
                  info="Megabits a second. Blank is no limit."
                  error={uploadProblem}
                >
                  <MbitField id="shape-upload" value={upload} onChange={setUpload} />
                </Field>
                <Field
                  label="Download limit"
                  htmlFor="shape-download"
                  info={
                    downloadSQM
                      ? "Queued on the managed IFB. Blank is no limit."
                      : "Policed: excess packets are dropped as they arrive. Blank is no limit."
                  }
                  error={downloadProblem}
                >
                  <MbitField id="shape-download" value={download} onChange={setDownload} />
                </Field>
              </FieldRow>
              <OptionList>
                <OptionRow
                  title="Use download SQM"
                  hint="CAKE queues downloads on a managed IFB for host and flow fairness. Requires independent recovery and a successful reconnect confirmation."
                  checked={downloadSQM}
                  disabled={busy}
                  onCheckedChange={setDownloadSQM}
                />
                {profileOffered && (
                  <OptionRow
                    title="Use an upload profile"
                    hint="CAKE classes by DSCP, host fairness and the link's framing, verified after apply."
                    checked={uploadCake}
                    disabled={busy}
                    onCheckedChange={setUploadCake}
                  />
                )}
              </OptionList>
              {(refusal || profileProblem || upProblem) && (
                <p role="alert" className="animate-rise text-body text-destructive">
                  {refusal || profileProblem || upProblem}
                </p>
              )}
            </FormSection>
            {profileOffered && uploadCake && (
              <UploadFields draft={upProfile} onChange={setUpProfile} disabled={busy} />
            )}
            {downloadSQM && <SQMFields draft={profile} onChange={setProfile} disabled={busy} />}
          </>
        )}
        <CakeTins stat={device.root} label="Upload queue delay" />
        <CakeTins stat={device.sqm?.queue} label="Download queue delay" />
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
