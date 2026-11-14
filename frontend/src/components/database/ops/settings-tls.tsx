"use client"

import { useRef, useState } from "react"
import {
  Check,
  Copy,
  Download,
  LockClosed,
  LockOpen,
  RefreshClockwise,
  ShieldCheck,
  Trash,
} from "@/components/icons"
import { downloadUrl, errorMessage, post, put } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbTLS } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Field, FormFact, FormNote, InfoTip } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { read } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import { useAsk } from "@/components/database/ops/settings-ask"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import {
  applicationsWords,
  certificateLife,
  changeEffects,
  changedWords,
  namesLine,
  portWords,
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
 * It opens on the connection drawn with its lock: this dashboard, the
 * certificate between, the server's port. The lock is closed and green while
 * the port speaks TLS and a ring while it does not, and the three captions
 * carry what a sentence and a list of facts did: whether the dashboard checks
 * the certificate, how long it has left, and what the port accepts. A state
 * that could not be read is the same picture with its lock unknown and Try
 * again at the head, not a notice where the section should be.
 *
 * Whether plain text is still accepted is a field: two segments, held as a
 * draft and saved by the page's bar. Turning TLS on or off and renewing the
 * certificate are acts, at the section's head. Every one of them recreates
 * the container (the same act as changing where it can be reached from, with
 * the same dropped sessions) so each says what it does before it is taken,
 * and a renewal is asked for the same way, because the engine that cannot
 * read the new certificate while running is restarted.
 *
 * Any role reads the word the summary has. The certificate, the connection
 * string and the changes are an administrator's.
 */
export function TransportSecuritySection() {
  const { id, conn, engine, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [working, setWorking] = useState<TLSChange>()
  const [edit, setEdit] = useState<boolean>()
  const [refusal, setRefusal] = useState<string>()
  const [now] = useState(() => Date.now())
  const { ask, dialog, asking } = useAsk()
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
  const chooses = Boolean(data?.enabled && data.canOptional)
  const require = edit ?? Boolean(data?.require)
  const dirty = chooses && mayChange && require !== Boolean(data?.require)

  /** Asks for a change with what it does, sends it once confirmed, and says how it ended. */
  const change = (to: TLSChange) => {
    if (!data) return Promise.resolve(false)
    setRefusal(undefined)
    const effects = changeEffects(to, data, engine.label)
    const body =
      to === "off"
        ? { enabled: false }
        : { enabled: true, require: to === "require" || (to === "on" && !data.canOptional) }
    return ask(
      {
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
      },
      async () => {
        // The recreate can take a minute: the section says it is happening.
        setWorking(to)
        try {
          if (to === "renew") await post<DbTLS>(`/databases/${id}/tls/renew`)
          else await put<DbTLS>(`/databases/${id}/tls`, body)
          notify.success(changedWords(to, conn.name))
          setEdit(undefined)
          return true
        } catch (err) {
          // In the section, where the bar still holds the choice: a toast would lie over its Save.
          setRefusal(errorMessage(err))
          return false
        } finally {
          setWorking(undefined)
          tlsRead.refresh()
          status.refresh()
        }
      },
    )
  }

  const download = () => {
    const link = document.createElement("a")
    link.href = downloadUrl("/databases/tls/authority")
    link.download = "just-dashboard-database-ca.pem"
    link.click()
  }

  usePendingReport("tls", {
    edits: dirty ? [{ name: "Plain text", state: "changed" }] : [],
  })

  const verbs: Verb[] = [
    {
      key: "renew",
      label: "Renew certificate",
      icon: RefreshClockwise,
      disabled: !mayChange,
      run: () => void change("renew"),
    },
    {
      key: "off",
      label: "Turn off TLS",
      icon: Trash,
      danger: true,
      disabled: !mayChange,
      run: () => void change("off"),
    },
  ]

  const section = (
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
        ) : admin ? (
          // The line the state takes, kept while it is read and when it could not be.
          <Status tone="unknown" label={tlsRead.error ? "Not known" : "Reading…"} />
        ) : undefined
      }
      status={
        working ? (
          <Status key="working" tone="running" label={<TextShimmer>Changing TLS…</TextShimmer>} />
        ) : (
          settingStatus({ dirty, refused: Boolean(refusal) })
        )
      }
      actions={
        admin && !data && tlsRead.error ? (
          <Button size="sm" variant="outline" onClick={tlsRead.refresh}>
            <RefreshClockwise />
            Try again
          </Button>
        ) : data?.supported && !data.enabled ? (
          <Button
            size="sm"
            variant="outline"
            disabled={!mayChange}
            onClick={() => void change("on")}
          >
            <ShieldCheck />
            Turn on TLS
          </Button>
        ) : data?.enabled ? (
          <>
            <Button size="sm" variant="outline" onClick={download}>
              <Download />
              CA certificate
            </Button>
            <VerbMenu verbs={verbs} label="More for the certificate" />
          </>
        ) : undefined
      }
    >
      {admin ? (
        <TlsPicture tls={data} error={data ? undefined : tlsRead.error} now={now} />
      ) : (
        <FormNote>
          The certificate and the connection string are an administrator&rsquo;s to read and to
          change.
        </FormNote>
      )}

      {data && !data.supported && data.reason && (
        <FormNote data-slot="tls-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {data.reason}
        </FormNote>
      )}

      {data?.enabled && data.canOptional && (
        <Field
          label="Plain text"
          info="Allowed: a client may use TLS or plain text, and this dashboard always uses TLS. Refused: a client that does not use TLS cannot connect. Save asks first: either way the container is replaced and its sessions dropped."
        >
          <Segments
            label="Whether plain text is accepted"
            value={require ? "require" : "allow"}
            disabled={!mayChange || asking}
            onChange={(next) => {
              setEdit(next === "require")
              setRefusal(undefined)
            }}
            options={[
              { value: "allow", label: "Allowed" },
              { value: "require", label: "Refused" },
            ]}
          />
        </Field>
      )}

      {data?.enabled && (
        <>
          <DetailList className="animate-rise gap-y-2.5">
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
          </DetailList>

          {applicationsWords(data.applications) && (
            <FormNote data-slot="tls-applications">
              <span className="font-medium text-foreground">Linked applications.</span>{" "}
              {applicationsWords(data.applications)} Their traffic stays on this server&rsquo;s
              internal network.
            </FormNote>
          )}

          {data.client && (
            <div className="grid gap-2">
              <p className="flex items-center gap-1.5 text-body font-medium">
                From another machine
                <InfoTip label="How another machine connects">
                  With the certificate checked. Save the CA certificate as{" "}
                  <span className="font-mono">{data.client.caFile}</span> and replace the password.
                </InfoTip>
              </p>
              {data.client.url && <CopyLine label="Connection string" text={data.client.url} />}
              <CopyLine label="Command" text={data.client.command} />
            </div>
          )}
        </>
      )}

      {refusal && (
        <FormNote tone="danger" role="alert" className="animate-rise">
          It was not changed. {refusal}
        </FormNote>
      )}
      {admin && data?.supported && !can("destructive") && (
        <FormNote>Changing it recreates the container, which your role may not do.</FormNote>
      )}
    </SettingSection>
  )

  return (
    <>
      {/* One form in every state, so the picture is not drawn again when the read lands. */}
      <SettingForm
        name="Encryption in transit"
        onSave={() => change(require ? "require" : "allow")}
        dirty={dirty}
        changes={dirty ? 1 : 0}
        saving={Boolean(working)}
        canEdit={chooses && admin && can("destructive")}
        applies="immediately"
        onDiscard={() => {
          setEdit(undefined)
          setRefusal(undefined)
        }}
      >
        {section}
      </SettingForm>
      {dialog}
    </>
  )
}

/**
 * The connection with its lock: this dashboard, the certificate, the server's
 * port. One shape in every state (read, being read, unreadable, not held
 * here) so the section does not change height when the read lands.
 */
function TlsPicture({
  tls,
  error,
  now,
}: {
  tls: DbTLS | undefined
  error: Error | undefined
  now: number
}) {
  const { conn, engine, summary, status } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const hostMark = useRef<HTMLDivElement>(null)
  const lockMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const on = Boolean(tls?.enabled)
  const running = status.state === "running"
  const port = tls?.port ?? conn.port
  const line = { still: !on || !running, dashed: !on }
  return (
    <SettingPicture
      label="How the connection is encrypted"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam containerRef={container} fromRef={hostMark} toRef={lockMark} {...line} />
          <AnimatedBeam
            containerRef={container}
            fromRef={lockMark}
            toRef={serverMark}
            delay={0.6}
            {...line}
          />
        </>
      }
      start={[
        <WireNode
          key="dashboard"
          nodeRef={hostMark}
          align="end"
          mark={<WireHost size="md" />}
          eyebrow="This dashboard"
          title={
            !tls
              ? "Connects to it"
              : !on
                ? "Connects in plain text"
                : tls.verified
                  ? "Checks the certificate"
                  : "Does not check the certificate"
          }
          hint={on ? "always uses TLS" : undefined}
        />,
      ]}
      middle={
        <WireNode
          nodeRef={lockMark}
          align="center"
          mark={
            on ? (
              <WireMark size="md" tone="success" className="animate-rise">
                <LockClosed />
              </WireMark>
            ) : (
              <WirePlaceholder size="md">{error ? <LockClosed /> : <LockOpen />}</WirePlaceholder>
            )
          }
          eyebrow="Certificate"
          title={
            error ? (
              <span role="status">Could not be read</span>
            ) : !tls ? (
              <Skeleton className="my-1 h-3 w-20 lg:mx-auto" />
            ) : on ? (
              certificateLife(tls, now)
            ) : tls.supported ? (
              "None"
            ) : (
              "Not held here"
            )
          }
          hint={
            error ? (
              <span className="wrap-anywhere">{errorMessage(error)}</span>
            ) : on && tls?.notAfter ? (
              <>
                ends {calendarDate(tls.notAfter)}
                {tls.renewedAt && (
                  <span className="block">renewed {calendarDate(tls.renewedAt)}</span>
                )}
              </>
            ) : tls && !tls.supported ? (
              "the dashboard does not hold it"
            ) : undefined
          }
        />
      }
      end={
        <WireNode
          nodeRef={serverMark}
          align="start"
          mark={
            <WireMark size="md" tone="logo">
              <EngineGlyph engine={engine} />
            </WireMark>
          }
          eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
          title={<span className="font-mono text-xs">:{port || "?"}</span>}
          hint={tls ? portWords(tls) : undefined}
        />
      }
    />
  )
}

/** A string to paste, with the tick that says it was copied. */
function CopyLine({ label, text }: { label: string; text: string }) {
  const { copy, copied } = useCopy()
  return (
    <div className="flex min-w-0 items-start gap-2 rounded-md border border-hairline bg-card px-3 py-2">
      <code className={cn("min-w-0 flex-1 font-mono text-xs wrap-anywhere")}>{text}</code>
      <Button
        type="button"
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
