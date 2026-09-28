"use client"

import { useState } from "react"
import { ArrowUpRight, RefreshClockwise, Warning } from "@/components/icons"
import { del, get, put } from "@/lib/api"
import { calendarDate, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { CTCertificate, CTDomain, CTReport } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { FormSection, FormSections, OptionList, OptionRow } from "@/components/form"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"

/**
 * What the public Certificate Transparency logs hold for this host's
 * domains. Any authority can sign for a domain; the logs are where a
 * certificate this host never asked for shows up. Off until an administrator
 * turns it on, because every check sends the domain names to crt.sh.
 */
export function CertTransparency() {
  const { confirm, dialog } = useConfirm()
  const [busy, setBusy] = useState(false)
  // crt.sh's answers are cached for six hours on the server, so an hourly
  // read costs nothing and picks up a renewal made meanwhile.
  const report = usePoll(
    (signal) => get<CTReport>("/certificates/transparency", undefined, signal),
    3_600_000,
  )

  const enable = async () => {
    setBusy(true)
    try {
      await put("/certificates/transparency")
      report.refresh()
    } catch (err) {
      notify.error("The monitor was not switched on", err)
    } finally {
      setBusy(false)
    }
  }
  const disable = () =>
    confirm({
      title: "Stop checking the transparency logs",
      confirmLabel: "Switch off",
      description: (
        <p>
          This host stops asking crt.sh about its domains. A certificate another party gets for them
          goes unnoticed here until the monitor is switched back on.
        </p>
      ),
      action: async () => {
        await del("/certificates/transparency")
        report.refresh()
      },
    })

  const data = report.data
  const unexpected =
    data?.domains?.flatMap((d) => d.certificates.filter((c) => c.unexpectedIssuer)) ?? []

  return (
    <FormSections>
      <FormSection
        aside
        title="Transparency logs"
        hint={
          data?.enabled
            ? `${data.domains?.length ?? 0} registered ${data.domains?.length === 1 ? "domain" : "domains"} checked on crt.sh`
            : "Off"
        }
        actions={
          data?.enabled && (
            <Button variant="outline" size="sm" onClick={() => report.refresh()}>
              <RefreshClockwise className="size-3.5" />
              Read again
            </Button>
          )
        }
      >
        <OptionList>
          <OptionRow
            title="Watch the public logs for this host's domains"
            hint="Lists every live certificate any authority has logged for the domains these sites and certificates name, from crt.sh, at most every six hours. The domain names are sent to crt.sh."
            checked={data?.enabled ?? false}
            disabled={busy || !data}
            onCheckedChange={(on) => (on ? enable() : disable())}
          />
        </OptionList>
        <div>
          {report.loading && !data ? (
            <LoadingRows rows={2} />
          ) : report.error ? (
            <ErrorState error={report.error} onRetry={report.refresh} />
          ) : data?.enabled ? (
            <TransparencyReport report={data} unexpected={unexpected} />
          ) : null}
        </div>
      </FormSection>
      {dialog}
    </FormSections>
  )
}

function TransparencyReport({
  report,
  unexpected,
}: {
  report: CTReport
  unexpected: CTCertificate[]
}) {
  const domains = report.domains ?? []
  const skipped = report.skipped ?? []
  return (
    <div className="flex flex-col gap-3">
      {unexpected.length > 0 && (
        <Notice
          tone="warning"
          icon={Warning}
          title={`${unexpected.length} ${unexpected.length === 1 ? "certificate is" : "certificates are"} from an authority that signed nothing on this host`}
        >
          This host&apos;s own certificates come from {report.expectedIssuers?.join(", ")}. A CDN or
          another server of yours explains most of these; a certificate nobody here asked for is
          what the logs are for.
        </Notice>
      )}
      {!report.expectedIssuers?.length && domains.length > 0 && (
        <p className="text-body text-muted-foreground">
          No certificate on this host comes from a public authority, so none listed here can be
          called unexpected.
        </p>
      )}
      {domains.length === 0 && (
        <p className="py-2 text-body text-muted-foreground">
          No site or certificate here names a publicly registered domain.
        </p>
      )}
      {domains.map((d) => (
        <DomainLog key={d.domain} domain={d} />
      ))}
      {skipped.length > 0 && (
        <p className="text-hint text-muted-foreground">
          Not checked, to stay a polite caller: {skipped.join(", ")}.
        </p>
      )}
    </div>
  )
}

function DomainLog({ domain }: { domain: CTDomain }) {
  const ours = domain.certificates.filter((c) => c.ours).length
  return (
    <section aria-label={domain.domain} className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="text-title font-semibold break-all">{domain.domain}</h3>
        <span className="text-hint text-muted-foreground">
          {domain.error
            ? "not read"
            : `${domain.certificates.length} live · ${ours} on this host · read ${relativeTime(domain.checked)}`}
        </span>
      </div>
      {domain.error ? (
        <p className="text-hint break-all text-destructive">{domain.error}</p>
      ) : domain.certificates.length === 0 ? (
        <p className="text-body text-muted-foreground">The logs hold no live certificate for it.</p>
      ) : (
        <ul className="divide-y divide-hairline border-y border-hairline">
          {domain.certificates.map((c) => (
            <LoggedCertificate key={c.id} cert={c} />
          ))}
        </ul>
      )}
    </section>
  )
}

function LoggedCertificate({ cert }: { cert: CTCertificate }) {
  return (
    <li className="flex flex-wrap items-start justify-between gap-x-4 gap-y-1 py-2">
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="text-body break-all">{cert.names.join(", ")}</span>
        <span className="text-hint text-muted-foreground">
          {cert.issuerOrg === cert.issuer ? cert.issuer : `${cert.issuerOrg} · ${cert.issuer}`} ·{" "}
          {calendarDate(cert.notBefore)} to {calendarDate(cert.notAfter)} ·{" "}
          <Tag mono>{cert.serial}</Tag>
        </span>
      </div>
      <div className="flex shrink-0 items-center gap-3">
        {cert.ours ? (
          <Status verdict="ok" label="On this host" />
        ) : cert.unexpectedIssuer ? (
          <Status verdict="warning" label="Unexpected issuer" />
        ) : (
          <Status tone="unknown" label="Not on this host" />
        )}
        <a
          href={cert.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 text-hint text-muted-foreground hover:text-foreground"
        >
          crt.sh
          <ArrowUpRight className="size-3" />
        </a>
      </div>
    </li>
  )
}
