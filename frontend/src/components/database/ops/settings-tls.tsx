"use client"

import { useState } from "react"
import { Check, Copy, Download, RefreshClockwise, ShieldCheck } from "@/components/icons"
import { downloadUrl, post, put } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbTLS } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { FormFact, FormNote } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { EngineMark } from "@/components/database/kit"
import {
  applicationsWords,
  certificateLife,
  changeEffects,
  changedWords,
  namesLine,
  shortFingerprint,
  tlsState,
  type TLSChange,
} from "@/components/database/ops/settings-tls-model"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Whether the server speaks TLS, with a certificate from this dashboard's own
 * authority, and the few things that change it: turning it on or off, refusing
 * plain text, and replacing the certificate before it ends.
 *
 * Turning it on, off or toward required recreates the container — the same
 * act as changing where it can be reached from, with the same dropped sessions
 * — so each says what it does before it is taken. A renewal is asked for the same
 * way, because the engine that cannot read the new certificate while running
 * is restarted; it happens by itself a month before the old one ends.
 *
 * Any role reads the word the summary has. The certificate, the connection
 * string and the changes are an administrator's.
 */
export function TransportSecuritySection({
  confirm,
}: {
  confirm: (request: ConfirmRequest) => void
}) {
  const { id, conn, engine, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [working, setWorking] = useState<string>()
  const [now] = useState(() => Date.now())
  const tlsRead = usePoll(
    (signal) =>
      read<DbTLS>(
        `/databases/${id}/tls`,
        (answer) => typeof answer.supported === "boolean" && Array.isArray(answer.names),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: admin },
  )
  const data = tlsRead.data
  const words = data ? tlsState(data) : undefined
  const mayChange = admin && can("destructive") && data?.supported === true && !working

  const finish = () => {
    setWorking(undefined)
    tlsRead.refresh()
    status.refresh()
  }

  const change = (to: TLSChange) => {
    if (!data) return
    const effects = changeEffects(to, data, engine.label)
    const body =
      to === "off"
        ? { enabled: false }
        : { enabled: true, require: to === "require" || (to === "on" && !data.canOptional) }
    const send = () =>
      to === "renew"
        ? post<DbTLS>(`/databases/${id}/tls/renew`)
        : put<DbTLS>(`/databases/${id}/tls`, body)
    confirm({
      title: effects.title,
      confirmLabel: effects.confirm,
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Container" mono>
              {data.container ?? "—"}
            </FormFact>
            <FormFact label="Port" mono>
              {data.port ?? "—"}
            </FormFact>
          </>
        ),
      },
      description: (
        <>
          <p>{effects.lead}</p>
          {effects.cautions.map((caution) => (
            <p key={caution}>{caution}</p>
          ))}
        </>
      ),
      action: async () => {
        // The recreate can take a minute: the dialog begins it and the
        // section says it is happening.
        void (async () => {
          setWorking(to)
          try {
            await send()
            notify.success(changedWords(to, conn.name))
          } catch (err) {
            notify.error(`Could not change TLS for ${conn.name}`, err)
          } finally {
            finish()
          }
        })()
        return "reported"
      },
    })
  }

  const download = () => {
    const link = document.createElement("a")
    link.href = downloadUrl("/databases/tls/authority")
    link.download = "just-dashboard-database-ca.pem"
    link.click()
  }

  return (
    <SettingSection
      id="tls"
      title="Encryption in transit"
      state={
        words ? (
          words.on ? (
            <Status verdict="ok" label={words.word} />
          ) : (
            <Status tone="unknown" label={words.word} />
          )
        ) : undefined
      }
    >
      {!admin && (
        <FormNote>
          The certificate and the connection string are an administrator&rsquo;s to read and to
          change.
        </FormNote>
      )}

      {admin && !data && !tlsRead.error && <FactsSkeleton rows={3} />}
      {admin && !data && tlsRead.error && (
        <CouldNotRead
          what="whether it speaks TLS"
          error={tlsRead.error}
          onRetry={tlsRead.refresh}
        />
      )}

      {data && words && (
        <p className="text-body leading-relaxed text-muted-foreground">
          {working ? <TextShimmer>Changing TLS…</TextShimmer> : words.sentence}
        </p>
      )}

      {data && !data.supported && data.reason && (
        <FormNote data-slot="tls-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {data.reason}
        </FormNote>
      )}

      {data?.supported && !data.enabled && (
        <div>
          <Button disabled={!mayChange} onClick={() => change("on")}>
            <ShieldCheck aria-hidden />
            Turn on TLS
          </Button>
        </div>
      )}

      {data?.enabled && data.canOptional && (
        <ChoiceGrid columns={2} role="group" aria-label="Whether plain text is accepted">
          <ChoiceCard
            selected={!data.require}
            disabled={!mayChange}
            onClick={() => !data.require || change("allow")}
          >
            <ChoiceCardTitle>TLS allowed</ChoiceCardTitle>
            <ChoiceCardHint>
              Clients may use TLS or plain text. This dashboard always uses TLS.
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.require}
            disabled={!mayChange}
            onClick={() => data.require || change("require")}
          >
            <ChoiceCardTitle>TLS required</ChoiceCardTitle>
            <ChoiceCardHint>A client that does not use TLS is refused.</ChoiceCardHint>
          </ChoiceCard>
        </ChoiceGrid>
      )}

      {data?.enabled && (
        <>
          <DetailList className="animate-rise gap-y-2.5 border-t border-hairline pt-5">
            <Detail label="Certificate">{certificateLife(data, now)}</Detail>
            {data.notAfter && <Detail label="Ends">{calendarDate(data.notAfter)}</Detail>}
            {data.renewedAt && <Detail label="Last renewed">{calendarDate(data.renewedAt)}</Detail>}
            <Detail label="Names" className="font-mono wrap-anywhere">
              {namesLine(data.names)}
            </Detail>
            {data.authority && (
              <Detail label="Authority" className="wrap-anywhere">
                {data.authority.subject}
                <span className="ml-2 font-mono text-muted-foreground">
                  {shortFingerprint(data.authority.fingerprint)}
                </span>
                <span className="ml-2 text-muted-foreground">
                  ends {calendarDate(data.authority.notAfter)}
                </span>
              </Detail>
            )}
            <Detail label="This dashboard">
              {data.verified ? "Checks the certificate" : "Does not check the certificate"}
            </Detail>
          </DetailList>

          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={download}>
              <Download aria-hidden />
              Download CA certificate
            </Button>
            <Button variant="outline" disabled={!mayChange} onClick={() => change("renew")}>
              <RefreshClockwise aria-hidden />
              Renew certificate
            </Button>
            <Button variant="outline" disabled={!mayChange} onClick={() => change("off")}>
              Turn off TLS
            </Button>
          </div>

          {applicationsWords(data.applications) && (
            <FormNote data-slot="tls-applications">
              <span className="font-medium text-foreground">Linked applications.</span>{" "}
              {applicationsWords(data.applications)} Their traffic stays on this server&rsquo;s
              internal network.
            </FormNote>
          )}

          {data.client && (
            <div className="grid gap-2 border-t border-hairline pt-5">
              <p className="text-xs text-muted-foreground">
                From another machine, with the certificate checked. Save the CA certificate as{" "}
                <span className="font-mono">{data.client.caFile}</span> and replace the password.
              </p>
              {data.client.url && <CopyLine label="Connection string" text={data.client.url} />}
              <CopyLine label="Command" text={data.client.command} />
            </div>
          )}
        </>
      )}

      {admin && data?.supported && !can("destructive") && (
        <FormNote>Changing it recreates the container, which your role may not do.</FormNote>
      )}
    </SettingSection>
  )
}

/** A string to paste, with the tick that says it was copied. */
function CopyLine({ label, text }: { label: string; text: string }) {
  const { copy, copied } = useCopy()
  return (
    <div className="flex min-w-0 items-start gap-2 rounded-md border border-hairline bg-card px-3 py-2">
      <code className={cn("min-w-0 flex-1 font-mono text-xs wrap-anywhere")}>{text}</code>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label={`Copy ${label.toLowerCase()}`}
        onClick={() => void copy(text)}
      >
        {copied ? <Check aria-hidden /> : <Copy aria-hidden />}
      </Button>
    </div>
  )
}
