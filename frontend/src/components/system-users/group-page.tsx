"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { useCallback, useMemo, useState } from "react"
import { ArrowLeft, Notes, Shield, Terminal, Trash, Users } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { SaveBarProvider, useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { WireMark } from "@/components/deploy/wire"
import { Field, FieldRow, FormSections, OptionList, OptionRow } from "@/components/form"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { accountHref } from "@/components/system-users/access"
import {
  decodeSegment,
  StepArrows,
  useAddressTab,
  ViewTabs,
  type Step,
  type View,
} from "@/components/system-users/detail-shell"
import { GroupMark, useGroups } from "@/components/system-users/group-parts"
import { GroupRail } from "@/components/system-users/group-rail"
import {
  groupHref,
  groupPeople,
  groupReach,
  listGroups,
  validGid,
  type GroupReach,
  type GroupScope,
} from "@/components/system-users/groups"
import { AccountMark } from "@/components/system-users/marks"
import { ReadFailed } from "@/components/system-users/read-state"
import { AccountWireMark, GroupWireMark } from "@/components/system-users/wire-marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { Workspace } from "@/components/workspace/workspace"
import { usePoll } from "@/hooks/use-poll"
import { del, get, patch } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemGroup, SystemUser } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"

const TABS = ["overview", "members", "settings"] as const
type Tab = (typeof TABS)[number]

/** A list of accounts longer than this gets a box to find one in. */
const SEARCH_FROM = 9

const BACK = (
  <Link
    href="/system-users/groups"
    className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
  >
    <ArrowLeft className="size-3" /> Groups
  </Link>
)

/**
 * One group, as a place of its own.
 *
 * It was a sheet over the Groups list until 0.8.7: its members as a column of
 * switches and its name and number under them, in a drawer's width. What a
 * group is opened to find out (what being in it hands over, and through which
 * rule) was one grey line there. So a group is a destination with a way back,
 * on the shape a service's page has (`procs/service-page.tsx`): its identity
 * line, its views as an underlined strip with the one on screen in the address,
 * and from `xl` the list beside it with its search and its order, so the next
 * group is one press away and Alt+↑ and Alt+↓ walk it.
 *
 * Membership, name and number are still one write, saved by the one bar at the
 * foot of the page whichever view the edit was made in.
 */
export function GroupPage() {
  const params = useParams<{ name: string }>()
  const router = useRouter()
  const name = decodeSegment(params.name)
  const [tab, setTab] = useAddressTab(TABS)
  const groups = useGroups()
  const [showSystem, setShowSystem] = useViewState("system-groups.show-system", false)
  const [query, setQuery] = useSessionState("system-groups.query", "")
  const [scope, setScope] = useSessionState<GroupScope>("system-groups.scope", "all")
  const listed = useMemo(
    () => listGroups(groups.data ?? [], { showSystem, query, scope }),
    [groups.data, showSystem, query, scope],
  )
  const group = groups.data?.find((g) => g.name === name)
  const open = useCallback((next: string) => router.push(groupHref(next, tab)), [router, tab])
  const at = listed.findIndex((g) => g.name === name)
  const adjacent = (direction: number) => {
    const next = at >= 0 ? listed[at + direction] : listed[direction > 0 ? 0 : -1]
    if (next) open(next.name)
  }
  const previous = { disabled: at <= 0, run: () => adjacent(-1) }
  const next = {
    disabled: listed.length === 0 || at >= listed.length - 1,
    run: () => adjacent(1),
  }
  const total = groups.data?.length ?? 0
  const caption = !groups.data
    ? "Reading the groups…"
    : scope === "root"
      ? `${listed.length} reach root`
      : query
        ? `${listed.length} of ${total}`
        : `${plural(listed.length, "group")} · root first`

  return (
    <Workspace
      name="Group"
      stateKey="system-users.group.page"
      refresh={groups.refresh}
      commands={[
        {
          id: "previous",
          label: "Previous group",
          keys: "Alt+↑",
          chord: "Alt+ArrowUp",
          disabled: previous.disabled,
          run: previous.run,
        },
        {
          id: "next",
          label: "Next group",
          keys: "Alt+↓",
          chord: "Alt+ArrowDown",
          disabled: next.disabled,
          run: next.run,
        },
      ]}
    >
      {/* Held to the window where the rail stands beside it, so the list and
          the identity line stay put while a view scrolls; narrower, the page
          is the one pane and scrolls as a whole. */}
      <Page fill="xl" className="gap-0 md:gap-0">
        <div className="flex min-w-0 flex-1 gap-6 xl:min-h-0">
          <GroupRail
            className="hidden w-72 shrink-0 xl:flex"
            groups={listed}
            current={name}
            currentGroup={group}
            loading={groups.loading}
            query={query}
            onQuery={setQuery}
            showSystem={showSystem}
            onShowSystem={setShowSystem}
            filtered={scope === "root"}
            onClearFilters={() => setScope("all")}
            caption={caption}
            onOpen={open}
          />
          <SaveBarProvider key={name}>
            <GroupMain
              name={name}
              group={group}
              loading={groups.loading}
              error={groups.error}
              refresh={groups.refresh}
              tab={tab}
              setTab={setTab}
              previous={previous}
              next={next}
            />
          </SaveBarProvider>
        </div>
      </Page>
    </Workspace>
  )
}

function GroupMain({
  name,
  group,
  loading,
  error,
  refresh,
  tab,
  setTab,
  previous,
  next,
}: {
  name: string
  group?: SystemGroup
  loading: boolean
  error?: Error
  refresh: () => void
  tab: Tab
  setTab: (tab: string) => void
  previous: Step
  next: Step
}) {
  const arrows = <StepArrows noun="group" previous={previous} next={next} />
  if (!group) {
    if (loading) {
      return (
        <div className="flex min-w-0 flex-1 flex-col gap-5">
          <PageContext eyebrow={BACK} title={name} actions={arrows} />
          <HostIdentity
            className="border-b-0 pb-0"
            fallback={Users}
            title={<span className="font-mono">{name}</span>}
            state={null}
            facts={<TextShimmer>{`Reading ${name}…`}</TextShimmer>}
          />
          <LoadingRows />
        </div>
      )
    }
    return (
      <div className="flex min-w-0 flex-1 flex-col gap-5">
        <PageContext eyebrow={BACK} title="Group" actions={arrows} />
        {error ? (
          <ReadFailed error={error} onRetry={refresh} />
        ) : (
          <EmptyState
            icon={Users}
            title={`No group named ${name || "that"} on this host`}
            description="It may have been deleted or renamed. Pick another from the list."
            action={
              <Link
                href="/system-users/groups"
                className="text-body font-medium text-brand hover:underline"
              >
                Back to the groups
              </Link>
            }
          />
        )}
      </div>
    )
  }
  return (
    <GroupViews
      key={group.name}
      group={group}
      refresh={refresh}
      tab={tab}
      setTab={setTab}
      arrows={arrows}
    />
  )
}

function GroupViews({
  group,
  refresh,
  tab,
  setTab,
  arrows,
}: {
  group: SystemGroup
  refresh: () => void
  tab: Tab
  setTab: (tab: string) => void
  arrows: React.ReactNode
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  // What the form holds apart from the host: nothing, until something is
  // changed. The host's own answer shows through an untouched field, so a
  // refresh that brings somebody else's change is not read as an edit here.
  const [membersDraft, setMembersDraft] = useState<string[] | null>(null)
  const [nameDraft, setNameDraft] = useState<string | null>(null)
  const [gidDraft, setGidDraft] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // Who is in a package's group is the operator's to say. What it is called and
  // numbered is not, until they say they mean it: the package finds it by both.
  const [allowed, setAllowed] = useState(false)
  const query = allowed ? { allowSystem: true } : undefined
  const editable = !group.protected && (!group.system || allowed)
  const { data: users } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", undefined, signal),
    0,
    [],
  )

  const members = membersDraft ?? group.members
  const name = nameDraft ?? group.name
  const gid = gidDraft ?? String(group.gid)
  const path = `/system-accounts/groups/${encodeURIComponent(group.name)}`
  const toggled = [
    ...members.filter((m) => !group.members.includes(m)),
    ...group.members.filter((m) => !members.includes(m)),
  ].length
  const renamed = name !== group.name
  const renumbered = gid !== String(group.gid)
  const dirty = toggled > 0 || renamed || renumbered
  const invalid = (renamed && name.trim() === "") || (renumbered && !validGid(gid))
  const reach = groupReach(group)
  const people = groupPeople(group)

  const discard = () => {
    setMembersDraft(null)
    setNameDraft(null)
    setGidDraft(null)
  }

  const save = async () => {
    setBusy(true)
    try {
      await patch(
        path,
        {
          members,
          ...(renumbered ? { gid: Number(gid) } : {}),
          ...(renamed ? { newName: name } : {}),
        },
        { query },
      )
      notify.success("Group saved")
      refresh()
      // A renamed group lives at another address; the form there starts clean.
      if (renamed) router.replace(groupHref(name, tab))
      else discard()
      return true
    } catch (err) {
      notify.error("The group was not changed", err)
      return false
    } finally {
      setBusy(false)
    }
  }

  const saveAll = useSaveBarEntry(
    true,
    {
      name:
        [toggled > 0 && "Members", (renamed || renumbered) && "Name and number"]
          .filter(Boolean)
          .join(", ") || "Group",
      dirty,
      changes: toggled + Number(renamed) + Number(renumbered),
      saving: busy,
      invalid,
      applies: "immediately",
    },
    { save, discard },
  )

  const remove = () =>
    confirm({
      title: `Delete the group ${group.name}`,
      confirmLabel: "Delete",
      description: <p>Files it owns keep its number, with no name behind it.</p>,
      action: async (typed) => {
        await del(path, { confirm: typed, query })
        refresh()
        router.push("/system-users/groups")
      },
    })

  const verbs: Verb[] = editable
    ? [{ key: "delete", label: "Delete group", icon: Trash, danger: true, run: remove }]
    : []

  const views: View<Tab>[] = [
    { key: "overview", label: "Overview" },
    { key: "members", label: "Members", count: people.length },
    { key: "settings", label: "Settings" },
  ]

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-5">
      <div className="flex min-w-0 shrink-0 flex-col gap-5">
        <PageContext eyebrow={BACK} title={group.name} actions={arrows} />
        <HostIdentity
          className="animate-rise border-b-0 pb-0"
          logo={<GroupMark group={group} size="lg" />}
          title={<span className="truncate font-mono">{group.name}</span>}
          state={
            reach.label ? (
              <Status tone={REACH_DOT[reach.tone]} label={reach.label} />
            ) : (
              <Status tone="unknown" label="Hands nothing over" />
            )
          }
          facts={
            <>
              <span className="numeric font-mono">gid {group.gid}</span>
              <FactDot />
              <span className="numeric">
                {people.length === 0 ? "nobody in it" : plural(people.length, "account")}
              </span>
              {(group.system || group.protected) && (
                <>
                  <FactDot />
                  <span>{group.protected ? "the system depends on it" : "made by a package"}</span>
                </>
              )}
            </>
          }
          menu={
            verbs.length > 0 ? (
              <VerbMenu verbs={verbs} label={`More for ${group.name}`} />
            ) : undefined
          }
        />
      </div>

      <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1 gap-0">
        <ViewTabs label={`Views of ${group.name}`} views={views} shown={tab} />

        <TabsContent value="overview" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          <GroupOverview
            group={group}
            reach={reach}
            people={people}
            users={users}
            onMembers={() => setTab("members")}
          />
        </TabsContent>

        <TabsContent value="members" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "members" && (
            <div className="mx-auto w-full max-w-3xl min-w-0 animate-rise pb-20">
              <MembersForm
                group={group}
                users={users}
                members={members}
                onChange={setMembersDraft}
                changed={toggled}
              />
            </div>
          )}
        </TabsContent>

        <TabsContent value="settings" className="min-h-0 flex-1 pt-6 xl:overflow-y-auto">
          {tab === "settings" && (
            <form
              aria-label="Name and number"
              onSubmit={(event) => {
                event.preventDefault()
                void saveAll?.()
              }}
              className="mx-auto w-full max-w-3xl min-w-0 animate-rise pb-20"
            >
              <FormSections>
                <SettingSection
                  title="Name and number"
                  state={
                    <span className="font-mono">
                      {group.name} · gid {group.gid}
                    </span>
                  }
                  status={
                    renamed || renumbered ? (
                      <Status tone="warning" label="Unsaved changes" className="animate-rise" />
                    ) : undefined
                  }
                >
                  {group.protected ? (
                    <Notice title="The system depends on this group">
                      Its name and number stay as they are. Who is in it is yours to say.
                    </Notice>
                  ) : (
                    group.system &&
                    !allowed && (
                      <Notice title="A package made this group">
                        <span className="flex flex-wrap items-center justify-between gap-2">
                          It finds the group by name and number, so both are held here.
                          <Button
                            type="button"
                            size="xs"
                            variant="outline"
                            onClick={() => setAllowed(true)}
                          >
                            Allow changes
                          </Button>
                        </span>
                      </Notice>
                    )
                  )}
                  <FieldRow>
                    <Field
                      label="Name"
                      htmlFor="group-name"
                      error={renamed && name.trim() === "" ? "A group needs a name." : undefined}
                    >
                      <Input
                        id="group-name"
                        className="font-mono"
                        autoComplete="off"
                        value={name}
                        disabled={!editable}
                        onChange={(e) => setNameDraft(e.target.value)}
                      />
                    </Field>
                    <Field
                      label="Number"
                      htmlFor="group-gid"
                      info="Files the group owns keep the old number."
                      error={
                        renumbered && !validGid(gid) ? "A whole number, 0 or more." : undefined
                      }
                    >
                      <Input
                        id="group-gid"
                        type="number"
                        min={0}
                        className="numeric"
                        value={gid}
                        disabled={!editable}
                        onChange={(e) => setGidDraft(e.target.value)}
                      />
                    </Field>
                  </FieldRow>
                </SettingSection>
                {!group.protected && (
                  <SettingSection
                    title="Delete group"
                    tone="danger"
                    state="Files it owns keep its number, with no name behind it."
                    actions={
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        className="text-destructive"
                        disabled={!editable}
                        onClick={remove}
                      >
                        <Trash className="size-4" />
                        Delete group
                      </Button>
                    }
                  />
                )}
              </FormSections>
            </form>
          )}
        </TabsContent>
      </Tabs>
      {dialog}
    </div>
  )
}

const REACH_DOT = {
  danger: "danger",
  warning: "warning",
  default: "notice",
  muted: "unknown",
} as const

const REACH_LINE: Record<GroupReach["tone"], WireLine> = {
  danger: { state: "danger" },
  warning: { state: "warning" },
  default: { state: "up" },
  muted: { state: "idle" },
}

/**
 * What the group is for, as a picture: who is in it, the group, and what being
 * in it reaches. The line to what it reaches is the tone of that reach, and a
 * group that hands nothing over ends in a dashed ring rather than in nothing.
 * Under it, the rules that say so, each with the file it is in.
 */
function GroupOverview({
  group,
  reach,
  people,
  users,
  onMembers,
}: {
  group: SystemGroup
  reach: GroupReach
  people: string[]
  users?: SystemUser[]
  onMembers: () => void
}) {
  const known = new Map((users ?? []).map((user) => [user.username, user]))
  // An account the people's list does not hold is one a package made.
  const face = (username: string) =>
    known.get(username) ?? { username, system: Boolean(users), uid: -1 }
  const line = REACH_LINE[reach.tone]
  const start: WireFlowNode[] = people.map((username) => ({
    id: `member:${username}`,
    mark: (size) => <AccountWireMark user={face(username)} size={size === "lg" ? "md" : size} />,
    title: <span className="font-mono">{username}</span>,
    hint: group.primary.includes(username) ? "its main group" : undefined,
    name: username,
    href: accountHref(username),
    label: `Open ${username}`,
    line: { state: "up" },
  }))
  const rule = group.sudoRules[0]
  const end: WireFlowNode[] = !reach.label
    ? []
    : [
        {
          id: "reach",
          mark: (size) => (
            <WireMark
              tone={
                reach.tone === "danger" ? "danger" : reach.tone === "warning" ? "warning" : "logo"
              }
              size={size === "lg" ? "md" : size}
            >
              {rule ? (
                rule.full ? (
                  <Shield aria-hidden />
                ) : (
                  <Terminal aria-hidden />
                )
              ) : group.rootEquivalent ? (
                <Shield aria-hidden />
              ) : (
                <Notes aria-hidden />
              )}
            </WireMark>
          ),
          eyebrow: rule ? "Through sudo" : group.rootEquivalent ? "Without sudo" : undefined,
          title: reach.label,
          hint: reach.detail ? <span className="break-words">{reach.detail}</span> : undefined,
          name: reach.label,
          line,
        },
      ]

  return (
    <div className="flex min-w-0 animate-rise flex-col gap-8 pb-6">
      <Panel plain aria-label="What it gives">
        <PanelHeader title="What it gives" />
        <PanelBody flush>
          <WireFlow
            phone="stack"
            start={{
              title: "In it",
              nodes: start,
              count: people.length,
              max: 5,
              more: { onPress: onMembers, words: (hidden) => `${hidden} more accounts` },
              empty: "Nobody is in this group.",
            }}
            hub={{
              mark: <GroupWireMark name={group.name} root={reach.tone !== "muted"} size="lg" />,
              eyebrow: "A group",
              title: <span className="font-mono">{group.name}</span>,
              hint: <span className="numeric">gid {group.gid}</span>,
            }}
            end={{
              title: "Reaches",
              nodes: end,
              empty: "Nothing beyond the files it owns.",
            }}
          />
        </PanelBody>
      </Panel>

      {group.sudoRules.length > 0 && (
        <Panel plain aria-label="Rules">
          <PanelHeader
            title="Rules"
            actions={
              <span className="numeric text-hint text-muted-foreground">
                {plural(group.sudoRules.length, "rule")}
              </span>
            }
          />
          <PanelBody flush>
            <ul className="divide-y divide-hairline">
              {group.sudoRules.map((sudoRule) => (
                <li
                  key={sudoRule.id}
                  data-slot="group-rule"
                  className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-6 gap-y-1 py-2.5"
                >
                  <span className="min-w-0 font-mono text-body break-words">{sudoRule.text}</span>
                  <span className="min-w-0 truncate font-mono text-hint text-muted-foreground">
                    {sudoRule.file}:{sudoRule.line}
                  </span>
                </li>
              ))}
            </ul>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

/**
 * Who is in the group, as a switch each. An account the group is the main one
 * of is in it by its own setting, so it is said and not offered. The switches
 * are staged: nothing is sent until the page's Save.
 */
function MembersForm({
  group,
  users,
  members,
  onChange,
  changed,
}: {
  group: SystemGroup
  users?: SystemUser[]
  members: string[]
  onChange: (members: string[]) => void
  changed: number
}) {
  const [search, setSearch] = useState("")
  const known = new Map((users ?? []).map((user) => [user.username, user]))
  // The people of this host, and whoever is in the group already that the
  // people's list does not hold (a package's account): otherwise a member
  // nobody can see could never be taken out.
  const names = [
    ...group.members.filter((member) => !known.has(member)),
    ...(users ?? []).map((user) => user.username),
  ].filter((username) => !group.primary.includes(username))
  const q = search.trim().toLowerCase()
  const shown = names.filter((username) => !q || username.toLowerCase().includes(q))
  const set = (username: string, on: boolean) =>
    onChange(on ? [...members, username] : members.filter((m) => m !== username))

  return (
    <FormSections>
      <SettingSection
        title="Members"
        state={
          <span className="numeric">
            {members.length === 0 ? "Nobody" : plural(members.length, "account")}
            {group.primary.length > 0 &&
              ` · the main group of ${group.primary.length === 1 ? group.primary[0] : plural(group.primary.length, "account")}`}
          </span>
        }
        status={
          changed > 0 ? (
            <Status tone="warning" label="Unsaved changes" className="animate-rise" />
          ) : undefined
        }
      >
        {names.length >= SEARCH_FROM && (
          <SearchInput
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Find an account"
            aria-label="Find an account"
            containerClassName="sm:w-64"
          />
        )}
        {!users && <LoadingRows rows={4} />}
        {users && (
          <OptionList data-slot="group-members">
            {group.primary.map((username) => (
              <div
                key={`primary:${username}`}
                className="flex min-h-11 min-w-0 items-center justify-between gap-6 py-2 sm:min-h-9"
              >
                <MemberName user={known.get(username) ?? { username, system: true, uid: -1 }} />
                <Link
                  href={`${accountHref(username)}?tab=settings`}
                  className="shrink-0 rounded-sm text-hint text-muted-foreground focus-ring hover:text-foreground"
                >
                  its main group
                </Link>
              </div>
            ))}
            {shown.map((username) => (
              <OptionRow
                key={username}
                title={
                  <MemberName user={known.get(username) ?? { username, system: true, uid: -1 }} />
                }
                checked={members.includes(username)}
                onCheckedChange={(on) => set(username, on)}
              />
            ))}
          </OptionList>
        )}
        {users && shown.length === 0 && group.primary.length === 0 && (
          <p className="text-body text-muted-foreground">
            {q ? "No account matches." : "This host has no accounts to add."}
          </p>
        )}
      </SettingSection>
    </FormSections>
  )
}

function MemberName({ user }: { user: Pick<SystemUser, "username" | "system" | "uid"> }) {
  return (
    <span className="flex min-w-0 items-center gap-2.5">
      <AccountMark user={user} size="sm" />
      <span className="min-w-0 truncate font-mono">{user.username}</span>
    </span>
  )
}
