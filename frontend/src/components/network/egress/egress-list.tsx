"use client"

import { useState } from "react"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { plural, relativeTime } from "@/lib/format"
import {
  egressDraftFromGroup,
  emptyEgressDraft,
  type EgressDraft,
  type EgressGroup,
  type EgressView,
} from "@/lib/network-egress"
import { cn } from "@/lib/utils"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Lifebuoy, Plus, Route } from "@/components/icons"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext } from "@/components/page"
import { EmptyState, ErrorState, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbActions } from "@/components/verbs"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { EgressGroupForm } from "@/components/network/egress/egress-form"
import { useEgressVerbs } from "@/components/network/egress/egress-verbs"
import { DOT, EgressMark, TONE_TEXT } from "@/components/network/egress/marks"
import {
  egressAutomationWord,
  egressCarrying,
  egressHref,
  egressRowLine,
} from "@/components/network/egress/reading"
import { useEgressLinks, useEgressView } from "@/components/network/egress/use-egress"

const TITLE = "Egress groups"

type FormState = { draft: EgressDraft; editing?: EgressGroup; key: number }

/**
 * Monitored egress groups, as a list of things to open.
 *
 * It opened on four grey tiles — groups, carrying, automated, members
 * proven — over every group laid out in full, about a thousand pixels each:
 * a row of six equal buttons, a members table, the decisions and the
 * simulation. The figures are the identity line's facts now, and each group
 * is one card: its mark with the carrying state in the corner, its name over
 * how it pins and how many members it has, the member carrying its traffic
 * in that state's colour, how it switches, and its verbs in a `⋯`. The rest
 * of a group is its own page (`egress-page.tsx`).
 */
export function EgressListPage() {
  const read = useEgressView()
  const { can } = useAuth()
  const admin = can("system.admin")
  const [form, setForm] = useState<FormState>()
  const links = useEgressLinks(Boolean(form))
  const data = read.data
  const create = () => setForm({ draft: emptyEgressDraft(), key: Date.now() })
  const edit = (group: EgressGroup) =>
    setForm({ draft: egressDraftFromGroup(group), editing: group, key: Date.now() })

  return (
    <Page className="animate-rise">
      <PageContext title={TITLE} />
      <ListIdentity
        data={data}
        command={
          admin ? (
            <Button
              size="sm"
              disabled={!data || data.groups.length >= data.capacity}
              title={
                data && data.groups.length >= data.capacity
                  ? `Every one of the ${data.capacity} slots is taken`
                  : undefined
              }
              onClick={create}
            >
              <Plus className="size-3.5" />
              New group
            </Button>
          ) : undefined
        }
      />
      {!data ? (
        read.error ? (
          <ErrorState error={read.error} onRetry={read.refresh} />
        ) : (
          <LoadingCards />
        )
      ) : (
        <>
          <NetworkReadWarning
            error={read.error}
            refresh={read.refresh}
            lastSuccess={read.lastSuccess}
            reading="egress readings"
          />
          {!data.persistent && <MemoryOnly />}
          {data.groups.length === 0 ? (
            <EmptyState
              icon={Lifebuoy}
              title="No egress groups"
              description="A group moves selected traffic between gateways or tunnels by latency, loss and failure."
              action={
                admin ? (
                  <Button size="sm" variant="outline" onClick={create}>
                    <Plus aria-hidden />
                    New group
                  </Button>
                ) : undefined
              }
            />
          ) : (
            <ChoiceList aria-label="Egress groups">
              {data.groups.map((group) => (
                <EgressRow
                  key={group.id}
                  group={group}
                  running={data.running}
                  onChanged={read.refresh}
                  onEdit={edit}
                />
              ))}
            </ChoiceList>
          )}
        </>
      )}
      {form && (
        <EgressGroupForm
          key={form.key}
          open
          onOpenChange={(open) => !open && setForm(undefined)}
          initial={form.draft}
          editing={form.editing}
          links={links.data ?? []}
          onSaved={read.refresh}
        />
      )}
    </Page>
  )
}

/** The notice a store that cannot keep the record has always shown. */
export function MemoryOnly() {
  return (
    <Notice tone="warning" title="Decisions are kept in memory only">
      The dashboard&rsquo;s database is not available to this module, so the decision record and
      sample history end with this process.
    </Notice>
  )
}

/**
 * The section's opening line: the four tiles' figures as facts, and whether
 * any member is down as the state. Drawn at its final height before the read
 * lands, so the cards under it do not move.
 */
function ListIdentity({ data, command }: { data?: EgressView; command?: React.ReactNode }) {
  const groups = data?.groups ?? []
  const members = groups.flatMap((g) => g.members)
  const down = members.filter((m) => m.state === "down").length
  const failing = groups.filter((g) => egressCarrying(g).tone === "danger").length
  const proven = members.filter((m) => m.proven).length
  return (
    <HostIdentity
      slot="egress-identity"
      logo={
        <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border bg-card text-foreground [&_svg]:size-5">
          <Route aria-hidden />
        </span>
      }
      title={TITLE}
      state={
        !data ? (
          <Status tone="unknown" label="Reading" />
        ) : groups.length === 0 ? (
          <Status tone="stopped" label="None yet" />
        ) : failing > 0 ? (
          <Status
            tone="danger"
            label={`${plural(failing, "group")} carrying through a down member`}
          />
        ) : down > 0 ? (
          <Status tone="warning" label={`${plural(down, "member")} down`} />
        ) : (
          <Status tone="running" label="Every member up" />
        )
      }
      command={command}
      facts={
        !data ? (
          <TextShimmer>Reading the groups…</TextShimmer>
        ) : (
          <>
            <span className="numeric">
              {groups.length} of {data.capacity} groups
            </span>
            {groups.length > 0 && (
              <>
                <FactDot />
                <span className="numeric">
                  {groups.filter((g) => g.enabled).length} carrying traffic
                </span>
                <FactDot />
                <span className="numeric">
                  {groups.filter((g) => g.enabled && g.automation).length} automatic
                </span>
                <FactDot />
                <span
                  className={cn("numeric", down > 0 ? "text-destructive" : undefined)}
                  title="Members whose path answered its probes recently"
                >
                  {proven} of {members.length} members proven
                </span>
              </>
            )}
            {data.monitoring && (
              <>
                <FactDot />
                <span title={new Date(data.monitoring).toLocaleString()}>
                  measuring since {relativeTime(data.monitoring).replace(/ ago$/, "")}
                </span>
              </>
            )}
          </>
        )
      }
    />
  )
}

/** Two cards' worth of ground while the first read is out, at a card's height. */
function LoadingCards() {
  return (
    <ul aria-busy className="space-y-2" aria-label="Reading the egress groups">
      {[0, 1].map((i) => (
        <li key={i}>
          <Skeleton className="h-14 w-full rounded-xl" />
        </li>
      ))}
    </ul>
  )
}

/**
 * One group as a card. On a phone the trailing columns have no room, so the
 * line under the name says the carrying member and how it switches instead
 * of how it pins; the card keeps its one height either way.
 */
function EgressRow({
  group,
  running,
  onChanged,
  onEdit,
}: {
  group: EgressGroup
  running?: EgressView["running"]
  onChanged: () => void
  onEdit: (group: EgressGroup) => void
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const { simulation, simulating, verbs, dialog } = useEgressVerbs({
    group,
    running,
    onChanged,
    onEdit,
  })
  const carrying = egressCarrying(group)
  const automation = egressAutomationWord(group)
  const all = simulation ? [simulation, ...verbs] : verbs
  const how = simulating ? (
    <TextShimmer className="text-hint">{`Simulating ${running!.step + 1} of ${running!.steps}`}</TextShimmer>
  ) : (
    automation
  )
  return (
    <>
      <ChoiceRow
        workspaceItem={{ id: String(group.id), name: group.name }}
        leading={<EgressMark group={group} />}
        title={group.name}
        verb={`Open ${group.name}`}
        href={egressHref(group.name)}
        busy={simulating}
        description={
          wide ? (
            <span className="font-mono">{egressRowLine(group)}</span>
          ) : (
            <>
              <span className={TONE_TEXT[carrying.tone]}>{carrying.label}</span>
              {" · "}
              {how}
            </>
          )
        }
        trailing={
          wide && (
            <span className="flex items-center gap-4">
              <span className="flex w-52 min-w-0 justify-start">
                <Status
                  tone={DOT[carrying.tone]}
                  label={<span className="block truncate">{carrying.label}</span>}
                  className="max-w-full min-w-0"
                />
              </span>
              <span className="w-28 truncate text-hint text-muted-foreground">{how}</span>
            </span>
          )
        }
        actions={
          all.length > 0 ? (
            <VerbActions dim verbs={all} menuLabel={`Actions for ${group.name}`} />
          ) : undefined
        }
      />
      {dialog}
    </>
  )
}
