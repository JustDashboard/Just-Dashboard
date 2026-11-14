"use client"

import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { Suspense, useEffect, useMemo, useState } from "react"
import { ArrowLeft, Plus, Trash, Users } from "@/components/icons"
import { InitialsMark } from "@/components/account/user-avatar"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { DimActions } from "@/components/icon-action"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  ChoiceRowsWaiting,
  GroupMark,
  NewGroupDialog,
  REACH_INK,
  useGroups,
} from "@/components/system-users/group-parts"
import {
  groupFigures,
  groupHref,
  groupPeople,
  groupReach,
  listGroups,
  type GroupScope,
} from "@/components/system-users/groups"
import { FaceRun } from "@/components/system-users/marks"
import { AdminOnly, ReadFailed } from "@/components/system-users/read-state"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useMediaQuery } from "@/hooks/use-mobile"
import { del } from "@/lib/api"
import { plural } from "@/lib/format"
import type { SystemGroup } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"

const BACK = (
  <Link
    href="/system-users"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> System users
  </Link>
)

/**
 * Who is in what, and what being in it hands over. A group's power is the
 * reason anyone comes here, so the page opens on how many of them are root by
 * another road, the ones that are come first, and what each gives is the column
 * after its name. Everything a package made is behind the same switch as on
 * the accounts page.
 *
 * A group is opened as a page of its own (`group-page.tsx`). It was a sheet
 * over this list until 0.8.7, which had room for its members and nowhere to
 * say what the group was for.
 */
export default function SystemGroupsPage() {
  return (
    <AdminOnly title="Groups">
      <Suspense>
        <SystemGroups />
      </Suspense>
    </AdminOnly>
  )
}

function SystemGroups() {
  const router = useRouter()
  const search = useSearchParams()
  const { confirm, dialog } = useConfirm()
  const wide = useMediaQuery("(min-width: 1280px)")
  const [showSystem, setShowSystem] = useViewState("system-groups.show-system", false)
  const [query, setQuery] = useSessionState("system-groups.query", "")
  const [scope, setScope] = useSessionState<GroupScope>("system-groups.scope", "all")
  const [adding, setAdding] = useState(false)
  const { data, error, loading, refresh } = useGroups()

  // The sheet had no address of its own. A link that names a group anyway
  // (`?group=docker`) lands on that group's page rather than on the list.
  const named = search.get("group")
  useEffect(() => {
    if (named) router.replace(groupHref(named))
  }, [named, router])

  const figures = useMemo(() => groupFigures(data ?? []), [data])
  const rows = useMemo(
    () => listGroups(data ?? [], { showSystem, query, scope }),
    [data, showSystem, query, scope],
  )
  const listed = useMemo(() => listGroups(data ?? [], { showSystem }), [data, showSystem])

  const remove = (group: SystemGroup) =>
    confirm({
      title: `Delete the group ${group.name}`,
      confirmLabel: "Delete",
      description: <p>Files it owns keep its number, with no name behind it.</p>,
      action: async (typed) => {
        await del(`/system-accounts/groups/${encodeURIComponent(group.name)}`, { confirm: typed })
        refresh()
      },
    })

  const verbsFor = (group: SystemGroup): Verb[] => [
    {
      key: "members",
      label: "Members",
      icon: Users,
      run: () => router.push(groupHref(group.name, "members")),
    },
    // A package's group asks first, and that question is on the group's page.
    ...(group.protected || group.system
      ? []
      : [{ key: "delete", label: "Delete", icon: Trash, danger: true, run: () => remove(group) }]),
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow={BACK} title="Groups" />

      <HostIdentity
        fallback={Users}
        title="Groups"
        state={
          data ? (
            figures.reachRoot > 0 ? (
              <button
                type="button"
                onClick={() => setScope(scope === "root" ? "all" : "root")}
                aria-label="Only the groups that reach root"
                aria-pressed={scope === "root"}
                // A flex box, so the line is the height of the words in it
                // and not of a button's own line box around them.
                className="flex rounded-sm focus-ring"
              >
                <Status
                  tone="warning"
                  label={`${figures.reachRoot} ${figures.reachRoot === 1 ? "reaches" : "reach"} root`}
                />
              </button>
            ) : (
              <Status verdict="ok" label="none reaches root" />
            )
          ) : undefined
        }
        facts={
          data ? (
            <>
              <span className="numeric">{plural(figures.total, "group")}</span>
              {figures.rootMembers > 0 && (
                <>
                  <FactDot />
                  <span className="numeric">
                    {plural(figures.rootMembers, "account")} in one of those
                  </span>
                </>
              )}
              <FactDot />
              <span className="numeric">{figures.system} made by packages</span>
            </>
          ) : error ? (
            <span>Not read</span>
          ) : (
            <TextShimmer>Reading the groups…</TextShimmer>
          )
        }
        command={
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus className="size-4" />
            New group
          </Button>
        }
      />

      {error && !data ? (
        <ReadFailed error={error} onRetry={refresh} />
      ) : (
        <Panel plain>
          <PanelToolbar>
            <SearchInput
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Group or member"
              aria-label="Search the groups"
              containerClassName="sm:w-56"
            />
            <ChipStrip>
              <FilterChip selected={scope === "all"} onClick={() => setScope("all")}>
                All {data && <ChipCount>{listed.length}</ChipCount>}
              </FilterChip>
              <FilterChip selected={scope === "root"} onClick={() => setScope("root")}>
                Reach root {data && <ChipCount>{figures.reachRoot}</ChipCount>}
              </FilterChip>
            </ChipStrip>
            <FilterChip
              className="sm:ml-auto"
              selected={showSystem}
              onClick={() => setShowSystem(!showSystem)}
            >
              System groups
            </FilterChip>
          </PanelToolbar>
          <PanelBody flush className="pt-3">
            {loading && !data && <ChoiceRowsWaiting />}
            {data && rows.length === 0 && (
              <EmptyState
                mark={<GroupFaces />}
                title={data.length === 0 ? "No groups on this host" : "No group matches"}
                description={
                  data.length === 0
                    ? undefined
                    : "Clear the search, pick All, or show the system groups."
                }
                action={
                  <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
                    <Plus className="size-4" />
                    New group
                  </Button>
                }
              />
            )}
            {data && rows.length > 0 && (
              <ChoiceList>
                {rows.map((group) => (
                  <GroupRow key={group.name} group={group} wide={wide} verbs={verbsFor(group)} />
                ))}
              </ChoiceList>
            )}
          </PanelBody>
        </Panel>
      )}
      {adding && (
        <NewGroupDialog
          onClose={() => setAdding(false)}
          onCreated={(name) => {
            refresh()
            router.push(groupHref(name, "members"))
          }}
        />
      )}
      {dialog}
    </Page>
  )
}

/** What an empty list of groups would hold: people, drawn as three of them. */
function GroupFaces() {
  return (
    <span aria-hidden className="flex items-center gap-1">
      <FaceRun names={["ada", "lin", "sam"]} max={3} />
    </span>
  )
}

/**
 * One group on one line: its mark, its name over its number, what it hands over
 * in the tone that deserves, and who is in it. Narrow, what it hands over takes
 * the line under the name and the members are a count, so a row is one height
 * at every width and with or without anybody in it.
 */
function GroupRow({ group, wide, verbs }: { group: SystemGroup; wide: boolean; verbs: Verb[] }) {
  const reach = groupReach(group)
  const people = groupPeople(group)
  return (
    <ChoiceRow
      href={groupHref(group.name)}
      verb={`Open ${group.name}`}
      leading={<GroupMark group={group} />}
      title={<span className="font-mono">{group.name}</span>}
      description={
        // A block each, so the line is one height whichever face it is set in.
        wide || !reach.label ? (
          <span className="numeric block truncate font-mono">gid {group.gid}</span>
        ) : (
          <span className={cn("block truncate", REACH_INK[reach.tone])}>{reach.label}</span>
        )
      }
      trailing={
        wide ? (
          <span className="flex min-w-0 items-center gap-6 text-xs">
            <span
              data-slot="group-gives"
              title={reach.text || undefined}
              className={cn("w-88 truncate", REACH_INK[reach.tone])}
            >
              {reach.text || <span className="text-muted-foreground/60">Nothing by itself</span>}
            </span>
            <span
              data-slot="group-members"
              className="flex w-60 min-w-0 items-center gap-2.5 overflow-hidden whitespace-nowrap"
            >
              {people.length === 0 ? (
                <span className="text-muted-foreground/60">Nobody</span>
              ) : (
                <>
                  {people.slice(0, 3).map((member) => (
                    <span
                      key={member}
                      className="inline-flex shrink-0 items-center gap-1 font-mono"
                    >
                      <InitialsMark name={member} size="xs" />
                      {member}
                    </span>
                  ))}
                  {people.length > 3 && (
                    <span className="numeric shrink-0 text-muted-foreground">
                      +{people.length - 3}
                    </span>
                  )}
                </>
              )}
            </span>
          </span>
        ) : (
          <span className="numeric flex items-center gap-1 text-xs text-muted-foreground">
            <Users aria-hidden className="size-3.5" />
            {people.length}
          </span>
        )
      }
      actions={
        <DimActions>
          <VerbMenu verbs={verbs} label={`More for ${group.name}`} />
        </DimActions>
      }
    />
  )
}
