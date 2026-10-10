"use client"

import { FieldRow, FormSection, OptionList, OptionRow } from "@/components/form"
import { ProfileChoice, ProfileNumber } from "./sqm-fields"
import type { UploadDraft } from "./upload-profile"

/**
 * CAKE's egress profile under an upload limit: which DSCP classes it keeps
 * apart, how flows share the link, and the access link's framing so the
 * shaper counts the bytes the modem sends. The latency it buys is measured by
 * CAKE itself per class once traffic flows; this form only states intent.
 */
export function UploadFields({
  draft,
  onChange,
  disabled,
}: {
  draft: UploadDraft
  onChange: (draft: UploadDraft) => void
  disabled: boolean
}) {
  const change = <K extends keyof UploadDraft>(key: K, value: UploadDraft[K]) =>
    onChange({ ...draft, [key]: value })
  return (
    <FormSection title="Upload CAKE profile">
      <p className="text-hint text-muted-foreground">
        Set the limit a little below the measured upload so the queue forms here, where CAKE
        controls delay, rather than in the modem. The delay each class sees appears under the device
        once traffic flows.
      </p>
      <FieldRow>
        <ProfileChoice
          id="upload-classes"
          label="Traffic classes"
          value={draft.diffserv}
          choices={[
            ["besteffort", "One class"],
            ["diffserv3", "Three DSCP classes"],
            ["diffserv4", "Four DSCP classes"],
          ]}
          hint="Classes are chosen by the DSCP marks this host's programs set."
          disabled={disabled}
          onChange={(value) => change("diffserv", value as UploadDraft["diffserv"])}
        />
        <ProfileChoice
          id="upload-fairness"
          label="Fairness"
          value={draft.flowMode}
          choices={[
            ["dual-srchost", "Source hosts and flows"],
            ["triple-isolate", "Source, destination and flows"],
            ["flows", "Flows only"],
          ]}
          hint="Source host fairness keeps one busy sender from taking the link."
          disabled={disabled}
          onChange={(value) => change("flowMode", value as UploadDraft["flowMode"])}
        />
      </FieldRow>
      <OptionList>
        <OptionRow
          title="Look up NAT host addresses"
          hint="Use local conntrack for host fairness behind this server's own NAT."
          checked={draft.nat}
          disabled={disabled}
          onCheckedChange={(value) => change("nat", value)}
        />
        <OptionRow
          title="Clear DSCP marks after classifying"
          hint="For a provider that mistreats marked packets. Off keeps them for the next hop."
          checked={draft.wash}
          disabled={disabled}
          onCheckedChange={(value) => change("wash", value)}
        />
        <OptionRow
          title="Filter redundant ACKs"
          hint="Helps a very asymmetric link, where acknowledgements fill the upload."
          checked={draft.ackFilter}
          disabled={disabled}
          onCheckedChange={(value) => change("ackFilter", value)}
        />
      </OptionList>
      <ProfileChoice
        id="upload-link-layer"
        label="Link accounting"
        value={draft.linkLayer}
        choices={[
          ["noatm", "No cell accounting"],
          ["atm", "ATM cells (ADSL)"],
          ["ptm", "PTM framing (VDSL)"],
        ]}
        hint="Match the access link; its overhead is the operator's estimate."
        disabled={disabled}
        onChange={(value) => change("linkLayer", value as UploadDraft["linkLayer"])}
      />
      <FieldRow>
        <ProfileNumber
          id="upload-overhead"
          label="Overhead bytes"
          hint="−64 to 256, added per packet."
          value={draft.overhead}
          disabled={disabled}
          onChange={(value) => change("overhead", value)}
        />
        <ProfileNumber
          id="upload-mpu"
          label="Minimum packet bytes"
          hint="0 to 256; the smallest accounted packet."
          value={draft.mpu}
          disabled={disabled}
          onChange={(value) => change("mpu", value)}
        />
      </FieldRow>
      <ProfileNumber
        id="upload-rtt"
        label="Expected RTT in milliseconds"
        hint="10 to 1000. Tunes queue control; it is not a measured latency."
        value={draft.rttMillis}
        disabled={disabled}
        onChange={(value) => change("rttMillis", value)}
      />
    </FormSection>
  )
}
