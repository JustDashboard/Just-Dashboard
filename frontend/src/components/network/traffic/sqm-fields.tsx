"use client"

import { Field, FieldRow, FormSection, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import type { SQMDraft } from "./sqm-profile"

export function SQMFields({
  draft,
  onChange,
  disabled,
}: {
  draft: SQMDraft
  onChange: (draft: SQMDraft) => void
  disabled: boolean
}) {
  const change = <K extends keyof SQMDraft>(key: K, value: SQMDraft[K]) =>
    onChange({ ...draft, [key]: value })
  return (
    <FormSection title="Download CAKE profile">
      <p className="text-hint text-muted-foreground">
        Queue incoming traffic on a managed IFB. Set the limit below the measured bottleneck so the
        queue can control delay. Link capacity, provider queues and actual latency remain unmeasured
        by this form.
      </p>
      <FieldRow>
        <ProfileChoice
          id="sqm-classes"
          label="Traffic classes"
          value={draft.diffserv}
          choices={[
            ["besteffort", "One class"],
            ["diffserv3", "Three DSCP classes"],
            ["diffserv4", "Four DSCP classes"],
          ]}
          hint="DSCP classification happens before wash; incoming markings may be set by another network."
          disabled={disabled}
          onChange={(value) => change("diffserv", value as SQMDraft["diffserv"])}
        />
        <ProfileChoice
          id="sqm-fairness"
          label="Fairness"
          value={draft.flowMode}
          choices={[
            ["dual-dsthost", "Destination hosts and flows"],
            ["triple-isolate", "Source, destination and flows"],
            ["flows", "Flows only"],
          ]}
          hint="Destination host fairness fits shared download traffic."
          disabled={disabled}
          onChange={(value) => change("flowMode", value as SQMDraft["flowMode"])}
        />
      </FieldRow>
      <OptionList>
        <OptionRow
          title="Look up NAT host addresses"
          hint="Use local conntrack for host fairness behind NAT. Provider-side NAT is unknown."
          checked={draft.nat}
          disabled={disabled}
          onCheckedChange={(value) => change("nat", value)}
        />
        <OptionRow
          title="Preserve incoming DSCP"
          hint="Off washes DSCP after classification. It still preserves ECN."
          checked={draft.preserveDscp}
          disabled={disabled}
          onCheckedChange={(value) => change("preserveDscp", value)}
        />
      </OptionList>
      <ProfileChoice
        id="sqm-link-layer"
        label="Link accounting"
        value={draft.linkLayer}
        choices={[
          ["noatm", "No cell accounting"],
          ["atm", "ATM cells"],
          ["ptm", "PTM framing"],
        ]}
        hint="Match the actual access link; encapsulation overhead is an operator estimate."
        disabled={disabled}
        onChange={(value) => change("linkLayer", value as SQMDraft["linkLayer"])}
      />
      <FieldRow>
        <ProfileNumber
          id="sqm-overhead"
          label="Overhead bytes"
          hint="−64 to 256, added per packet."
          value={draft.overhead}
          disabled={disabled}
          onChange={(value) => change("overhead", value)}
        />
        <ProfileNumber
          id="sqm-mpu"
          label="Minimum packet bytes"
          hint="0 to 256; the smallest accounted packet."
          value={draft.mpu}
          disabled={disabled}
          onChange={(value) => change("mpu", value)}
        />
      </FieldRow>
      <ProfileNumber
        id="sqm-rtt"
        label="Expected RTT in milliseconds"
        hint="10 to 1000. Tunes queue control; it is not a measured latency."
        value={draft.rttMillis}
        disabled={disabled}
        onChange={(value) => change("rttMillis", value)}
      />
    </FormSection>
  )
}

function ProfileChoice({
  id,
  label,
  hint,
  value,
  choices,
  disabled,
  onChange,
}: {
  id: string
  label: string
  hint: string
  value: string
  choices: string[][]
  disabled: boolean
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {choices.map(([key, title]) => (
            <SelectItem key={key} value={key}>
              {title}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  )
}

function ProfileNumber({
  id,
  label,
  hint,
  value,
  disabled,
  onChange,
}: {
  id: string
  label: string
  hint: string
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <Input
        id={id}
        inputMode="numeric"
        autoComplete="off"
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        className="font-mono"
      />
    </Field>
  )
}
