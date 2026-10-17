"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { Globe, Servers, Warning } from "@/components/icons"
import { get, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbAccess, DbAccessChange, DbPooler } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { FormFact, FormNote } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { ProductGlyph } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import {
  EXPOSURE,
  firewallOutcome,
  firewallWords,
  reachRefusal,
} from "@/components/database/ops/settings-model"
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

  // A connection pooler is published where the database is told to be, by this
  // same switch, so the confirmation says so.
  const pooler = usePoll(
    (signal) => get<DbPooler>(`/databases/${id}/pooler`, undefined, signal),
    0,
    [id],
    { enabled: admin && engine.can("pooler") },
  )
  const pooled = pooler.data?.enabled ? pooler.data : undefined

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
            {pooled && (
              <p>
                {pooled.container} (PgBouncer) is published with it, on port{" "}
                {pooled.port ?? "its own"}
                {firewall?.active && firewall.editable ? ", with a rule of its own" : ""}.
              </p>
            )}
            <p>The data is kept. Every open session is dropped while the container is replaced.</p>
          </>
        ) : (
          <>
            <p>
              Recreates {container} with port {port} bound to this machine only, and removes the
              firewall rule this dashboard wrote for it. Anything that reaches it from another
              machine stops reaching it.
            </p>
            {pooled && pooled.exposure !== "local" && (
              <p>{pooled.container} (PgBouncer) is brought back to this machine with it.</p>
            )}
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
            if (done.pooler?.error) {
              notify.warning(
                done.pooler.removed
                  ? `The connection pooler could not follow ${conn.name} and was removed`
                  : `The connection pooler did not follow ${conn.name}`,
                { description: done.pooler.error },
              )
            }
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
    <SettingSection
      id="reachability"
      title="Where it can be reached from"
      state={
        <span className="flex">
          {exposure === "public" ? (
            <Status verdict="warning" label={words.word} />
          ) : (
            <Status tone="unknown" label={words.word} />
          )}
        </span>
      }
    >
      <ReachPicture access={data} exposure={exposure} changing={Boolean(changing)} />
      <p className="text-body leading-relaxed text-muted-foreground">
        {changing ? (
          <TextShimmer>Changing where it is reachable from…</TextShimmer>
        ) : (
          words.sentence
        )}
      </p>

      {mayChange && data && (
        <ChoiceGrid columns={2} role="group" aria-label="Where it is reachable from">
          <ChoiceCard
            selected={data.exposure === "local"}
            disabled={Boolean(changing)}
            onClick={() => change("local")}
          >
            <span className="flex items-center gap-2">
              <Servers
                aria-hidden
                className={cn(
                  "size-4 shrink-0",
                  data.exposure === "local" ? "text-brand" : "text-muted-foreground",
                )}
              />
              <ChoiceCardTitle>This server only</ChoiceCardTitle>
            </span>
            <ChoiceCardHint>
              Port {port} on loopback. Programs on this machine and the deployments linked to it
              reach it; nothing else does.
            </ChoiceCardHint>
          </ChoiceCard>
          <ChoiceCard
            selected={data.exposure === "public"}
            disabled={Boolean(changing)}
            onClick={() => change("public")}
          >
            <span className="flex items-center gap-2">
              <Globe
                aria-hidden
                className={cn(
                  "size-4 shrink-0",
                  data.exposure === "public" ? "text-warning" : "text-muted-foreground",
                )}
              />
              <ChoiceCardTitle>Anywhere</ChoiceCardTitle>
            </span>
            <ChoiceCardHint>
              Port {port} on every interface
              {firewall?.active && firewall.editable
                ? `, with a rule in ${rule} that lets it in from anywhere.`
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

      {admin &&
        (data ? (
          <DetailList className="animate-rise gap-y-2.5 border-t border-hairline pt-5">
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
    </SettingSection>
  )
}

/**
 * Who can reach the server's port, drawn as the way in: this machine's own
 * programs and the internet on the left, the container that publishes the
 * port in the middle — Docker's whale, its name, the binding — and the server
 * itself, as its product, at the end.
 *
 * The internet's line is the answer to the page's question. Dashed, with the
 * globe a ring not yet there, while the port is on loopback: nothing outside
 * gets in. Amber while it is published on every interface, because it works
 * and should not be relied on (§3's amber, as a project's Runtime draws a
 * port that bypasses the proxy). A server on another machine is one line
 * from here to it, since where it listens is that machine's to say.
 */
function ReachPicture({
  access,
  exposure,
  changing,
}: {
  access?: DbAccess
  exposure: string
  changing: boolean
}) {
  const { conn, engine, summary, status } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const localMark = useRef<HTMLDivElement>(null)
  const outsideMark = useRef<HTMLDivElement>(null)
  const boxMark = useRef<HTMLDivElement>(null)
  const serverMark = useRef<HTMLDivElement>(null)
  const remote = exposure === "remote"
  const open = exposure === "public" || exposure === "private"
  const running = status.state === "running"
  const port = access?.port ?? (Number(conn.port) || undefined)
  const boxed = Boolean(access?.container) && !remote
  const bind =
    exposure === "public" ? "0.0.0.0" : exposure === "private" ? "one address" : "127.0.0.1"
  const firewall = access?.firewall
  const wall = !firewall
    ? undefined
    : !firewall.active
      ? "no firewall in the way"
      : `${firewall.backend ?? "firewall"} ${firewall.open ? "lets it in" : "keeps it out"}`

  const local = (
    <WireNode
      key="local"
      nodeRef={localMark}
      align="end"
      mark={<WireHost size="md" />}
      eyebrow="This server"
      title="Programs and linked deployments"
      hint={remote ? "the dashboard dials it" : "over loopback"}
    />
  )
  const outside = !remote && (
    <WireNode
      key="outside"
      nodeRef={outsideMark}
      align="end"
      mark={
        open ? (
          <WireMark size="md" tone="warning" className="animate-rise">
            <Globe />
          </WireMark>
        ) : (
          <WirePlaceholder size="md" className="animate-rise">
            <Globe />
          </WirePlaceholder>
        )
      }
      eyebrow="Anywhere"
      title={
        open ? (
          <span className="font-mono text-xs">
            {access?.publicAddresses.length ? access.publicAddresses.join(", ") : "every interface"}
          </span>
        ) : (
          "Other machines"
        )
      }
      hint={
        <span className={cn(open && "text-warning")}>
          {open ? (wall ?? "reachable") : "cannot reach it"}
        </span>
      }
    />
  )
  const box = (
    <WireNode
      nodeRef={boxMark}
      align="center"
      mark={
        <WireMark size="md" tone="logo">
          <ProductGlyph id="docker" />
        </WireMark>
      }
      eyebrow="Container"
      title={<span className="font-mono text-xs wrap-anywhere">{access?.container}</span>}
      hint={
        <span className="font-mono">
          {bind}:{port ?? "?"}
        </span>
      }
    />
  )
  const server = (
    <WireNode
      nodeRef={serverMark}
      align="start"
      mark={
        <WireMark size="md" tone="logo">
          <EngineGlyph engine={engine} />
        </WireMark>
      }
      eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
      title={
        <span className="font-mono text-xs wrap-anywhere">
          {remote ? `${conn.host}:${conn.port}` : `:${port ?? conn.port}`}
        </span>
      }
      hint={remote ? "on its own machine" : "listens"}
    />
  )
  const inner = boxed ? boxMark : serverMark
  return (
    <SettingPicture
      label="Who can reach it"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={localMark}
            toRef={inner}
            still={!running || changing}
          />
          {!remote && (
            <AnimatedBeam
              key={open ? "open" : "closed"}
              containerRef={container}
              fromRef={outsideMark}
              toRef={inner}
              still
              dashed={!open}
              tone={open ? "warning" : "default"}
            />
          )}
          {boxed && (
            <AnimatedBeam
              containerRef={container}
              fromRef={boxMark}
              toRef={serverMark}
              still={!running || changing}
              delay={0.6}
            />
          )}
        </>
      }
      start={outside ? [local, outside] : [local]}
      middle={boxed ? box : undefined}
      end={server}
    />
  )
}
