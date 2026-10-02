"use client"

import { useState } from "react"
import Link from "next/link"
import { Warning } from "@/components/icons"
import { put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbAccess, DbAccessChange } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { FormFact, FormNote, FormSection } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineMark } from "@/components/database/kit"
import {
  EXPOSURE,
  firewallOutcome,
  firewallWords,
  reachRefusal,
} from "@/components/database/ops/settings-model"
import { UNDER_STRIP } from "@/components/database/ops/settings-nav"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * Where the server can be reached from, and the one change this page can
 * make to it: a container the dashboard owns is published on this machine
 * only, or on every interface.
 *
 * The change is not a switch. It recreates the container with another port
 * binding — every session is dropped while it is replaced — and writes or
 * removes a firewall rule, so each answer says what it does before it is
 * taken, and the one that publishes the port is confirmed with the rule
 * named. Where the binding is not the dashboard's to change, the page says
 * whose it is instead of offering a control the server would refuse.
 *
 * Any role reads the summary's word. The addresses and the firewall are an
 * administrator's read, and only an administrator asks for them.
 */
export function ReachabilitySection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, summary, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [changing, setChanging] = useState<"local" | "public">()
  const access = usePoll(
    (signal) =>
      read<DbAccess>(
        `/databases/${id}/access`,
        (answer) => record(answer.firewall) && Array.isArray(answer.publicAddresses),
        undefined,
        signal,
      ),
    0,
    [id],
    { enabled: admin },
  )

  const data = access.data
  const exposure = data?.exposure ?? summary?.exposure ?? "unknown"
  const words = EXPOSURE[exposure] ?? EXPOSURE.unknown
  const refusal = data ? reachRefusal(data) : undefined
  const mayChange = admin && can("destructive") && data !== undefined && !refusal
  const port = data?.port ?? (Number(conn.port) || undefined)
  const firewall = data?.firewall
  const rule = firewall?.backend ?? "the firewall"

  const change = (to: "local" | "public") => {
    if (!data || to === data.exposure) return
    const container = data.container ?? "its container"
    confirm({
      title: to === "public" ? `Publish ${conn.name} to anywhere` : `Restrict ${conn.name}`,
      confirmLabel: to === "public" ? "Publish" : "Restrict",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Container" mono>
              {container}
            </FormFact>
            <FormFact label="Port" mono>
              {port ?? "—"}
            </FormFact>
          </>
        ),
      },
      description:
        to === "public" ? (
          <>
            <p>
              Recreates {container} with port {port} bound to every interface of this machine
              {firewall?.active && firewall.editable
                ? `, and adds a rule to ${rule} that allows port ${port} from anywhere.`
                : "."}{" "}
              Anything that can reach this machine can then try the password.
            </p>
            <p>The data is kept. Every open session is dropped while the container is replaced.</p>
          </>
        ) : (
          <>
            <p>
              Recreates {container} with port {port} bound to this machine only, and removes the
              firewall rule this dashboard wrote for it. Anything that reaches it from another
              machine stops reaching it.
            </p>
            <p>The data is kept. Every open session is dropped while the container is replaced.</p>
          </>
        ),
      action: async () => {
        // The recreate can take a minute: the dialog begins it and the
        // section says it is happening.
        void (async () => {
          setChanging(to)
          try {
            const done = await put<DbAccessChange>(`/databases/${id}/access`, { exposure: to })
            const said = firewallOutcome(done.firewall, done.access.port)
            if (done.firewallError) {
              notify.warning(
                to === "public"
                  ? `${conn.name} is bound to every interface, and the firewall was not opened`
                  : `${conn.name} is bound to this server, and the firewall rule was not removed`,
                { description: done.firewallError },
              )
            } else {
              notify.success(
                to === "public"
                  ? `${conn.name} is reachable from anywhere`
                  : `${conn.name} is reachable from this server only`,
                { description: said || undefined },
              )
            }
          } catch (err) {
            notify.error(`Could not change where ${conn.name} is reachable from`, err)
          } finally {
            setChanging(undefined)
            access.refresh()
            status.refresh()
          }
        })()
        return "reported"
      },
    })
  }

  return (
    <FormSection
      aside
      id="reachability"
      className={UNDER_STRIP}
      title="Reachability"
      hint={
        <span className="flex">
          {exposure === "public" ? (
            <Status verdict="warning" label={words.word} />
          ) : (
            <Status tone="unknown" label={words.word} />
          )}
        </span>
      }
    >
      <div className="space-y-1">
        <p className="text-body font-medium">
          {changing ? <TextShimmer>Changing where it is reachable from…</TextShimmer> : words.word}
        </p>
        <p className="text-body leading-relaxed text-muted-foreground">{words.sentence}</p>
      </div>

      {admin &&
        (data ? (
          <DetailList className="animate-rise gap-y-2.5">
            {conn.port && (
              <Detail label="Listens on" className="font-mono">
                {conn.host}:{conn.port}
              </Detail>
            )}
            {data.container && (
              <Detail label="Container" className="font-mono wrap-anywhere">
                <Link
                  href={`/docker/containers/${encodeURIComponent(data.container)}`}
                  className="rounded-sm underline focus-ring"
                >
                  {data.container}
                </Link>
                {data.composeProject ? ` · compose project ${data.composeProject}` : ""}
              </Detail>
            )}
            {data.publicAddresses.length > 0 && data.exposure !== "remote" && (
              <Detail label="This machine" className="font-mono wrap-anywhere">
                {data.publicAddresses.join(", ")}
              </Detail>
            )}
            {data.exposure !== "remote" && <Detail label="Firewall">{firewallWords(data)}</Detail>}
          </DetailList>
        ) : access.error ? (
          <CouldNotRead
            what="where it is reachable from"
            error={access.error}
            onRetry={access.refresh}
          />
        ) : (
          <FactsSkeleton rows={3} />
        ))}

      {mayChange && data && (
        <ChoiceGrid columns={2} role="group" aria-label="Where it is reachable from">
          <ChoiceCard
            selected={data.exposure === "local"}
            disabled={Boolean(changing)}
            onClick={() => change("local")}
          >
            <ChoiceCardTitle>This server only</ChoiceCardTitle>
            <ChoiceCardHint>
              Port {port} bound to loopback. Programs on this machine and deployments linked to it
              reach it; nothing else does.
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.exposure === "public"}
            disabled={Boolean(changing)}
            onClick={() => change("public")}
          >
            <ChoiceCardTitle>Anywhere</ChoiceCardTitle>
            <ChoiceCardHint>
              Port {port} on every interface
              {firewall?.active && firewall.editable
                ? `, with a rule in ${rule} allowing it from anywhere.`
                : "."}{" "}
              Only the password stands between it and the internet.
            </ChoiceCardHint>
          </ChoiceCard>
        </ChoiceGrid>
      )}

      {data?.exposure === "public" && (
        <Notice
          tone="warning"
          icon={Warning}
          title="Anything that can reach this machine can try it"
        >
          {firewall?.active && !firewall.open
            ? `${rule} is on and port ${port} is closed in it, so for now only this machine's own networks get through.`
            : "The password is the only thing in the way. Restrict it to this server unless something outside has to connect."}
        </Notice>
      )}

      {admin && refusal && (
        <FormNote data-slot="reach-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {refusal}
        </FormNote>
      )}
      {admin && data && !refusal && !can("destructive") && (
        <FormNote>Changing it recreates the container, which your role may not do.</FormNote>
      )}
      {!admin && (
        <FormNote>
          The addresses and the firewall are an administrator&rsquo;s to read and to change.
        </FormNote>
      )}
    </FormSection>
  )
}
