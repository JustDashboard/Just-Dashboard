"use client"

import Link from "next/link"
import { useMemo, useState } from "react"
import { ArrowLeft, ArrowRight, Plus, ShieldCheck, Trash, Users } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { InitialsMark } from "@/components/account/user-avatar"
import { Modal } from "@/components/modal"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingPanel, EmptyNote } from "@/components/state"
import { groupGrant, isPrivateGroup } from "@/components/system-users/access"
import { groupProduct } from "@/components/system-users/marks"
import { FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { usePoll } from "@/hooks/use-poll"
import { del, get, patch, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SystemGroup, SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"

/** A group's own words: what it hands over, and to whom, before its number. */
function grants(group: SystemGroup) {
  if (group.sudoRules.length > 0) {
    const rule = group.sudoRules[0]
    if (rule.full && rule.noPassword)
      return { text: "Root with no password", ink: "text-destructive" }
    if (rule.full) return { text: "Root, asks for a password", ink: "text-warning" }
    return { text: `Runs ${rule.commands.join(", ")} as root`, ink: "text-foreground" }
  }
  const words = groupGrant(group.name)
  if (group.rootEquivalent) return { text: words ?? "Root by another name", ink: "text-warning" }
  return { text: words, ink: "text-muted-foreground" }
}

/**
 * Who is in what, and what being in it hands over. A group's power is the reason
 * anyone comes here, so it is the column the list leads with after the name, and
 * the ones that reach root come first. Everything a package made is behind the
 * same switch as the accounts page.
 */
export default function SystemGroupsPage() {
  const [showSystem, setShowSystem] = useViewState("system-groups.show-system", false)
  const [query, setQuery] = useState("")
  const [open, setOpen] = useState<string | null>(null)
  const [adding, setAdding] = useState(false)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<SystemGroup[]>("/system-accounts/groups", undefined, signal),
    30000,
    [],
  )
  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    const rank = (g: SystemGroup) =>
      g.sudoRules.length > 0 || g.rootEquivalent ? 0 : g.system ? 2 : 1
    return (data ?? [])
      .filter(
        (g) =>
          showSystem ||
          g.sudoRules.length > 0 ||
          g.rootEquivalent ||
          (!g.system && !isPrivateGroup(g)),
      )
      .filter((g) => !q || g.name.includes(q) || g.members.some((m) => m.includes(q)))
      .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
  }, [data, query, showSystem])
  const chosen = data?.find((g) => g.name === open)

  return (
    <Page className="animate-rise">
      <PageContext
        eyebrow={
          <Link
            href="/system-users"
            className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> System users
          </Link>
        }
        title="Groups"
        actions={
          <Button size="sm" onClick={() => setAdding(true)}>
            <Plus className="size-4" />
            New group
          </Button>
        }
      />
      <Panel plain>
        <PanelToolbar>
          <SearchInput
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Group or member"
            containerClassName="sm:w-56"
          />
          <FilterChip
            className="sm:ml-auto"
            selected={showSystem}
            onClick={() => setShowSystem(!showSystem)}
          >
            System groups
          </FilterChip>
        </PanelToolbar>
        <PanelBody flush>
          {loading && !data && <LoadingPanel />}
          {error && !data && <ErrorState error={error} />}
          {data && rows.length === 0 && <EmptyNote>No groups match.</EmptyNote>}
          {data && rows.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Group</TableHead>
                  <TableHead>Gives</TableHead>
                  <TableHead>Members</TableHead>
                  <TableHead className="w-8" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((group) => {
                  const grant = grants(group)
                  const everyone = [
                    ...group.members,
                    ...group.primary.filter((p) => !group.members.includes(p)),
                  ]
                  return (
                    <TableRow
                      key={group.name}
                      data-slot="group-row"
                      onActivate={() => setOpen(group.name)}
                    >
                      <TableCell>
                        <span className="flex items-center gap-3">
                          <ProductLogo
                            id={groupProduct(group.name)}
                            size="sm"
                            fallback={grant.ink === "text-muted-foreground" ? Users : ShieldCheck}
                          />
                          <span className="min-w-0">
                            <span className="block font-mono text-body font-medium">
                              {group.name}
                            </span>
                            <span className="numeric block text-hint text-muted-foreground">
                              gid {group.gid}
                            </span>
                          </span>
                        </span>
                      </TableCell>
                      <TableCell className={cn("whitespace-normal", grant.ink)}>
                        {grant.text ?? <span className="text-muted-foreground/60">—</span>}
                      </TableCell>
                      <TableCell>
                        {everyone.length === 0 ? (
                          <span className="text-muted-foreground/60">Nobody</span>
                        ) : (
                          <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                            {everyone.slice(0, 5).map((member) => (
                              <span
                                key={member}
                                className="inline-flex items-center gap-1 font-mono"
                              >
                                <InitialsMark name={member} size="xs" />
                                {member}
                              </span>
                            ))}
                            {everyone.length > 5 && (
                              <span className="numeric text-muted-foreground">
                                +{everyone.length - 5}
                              </span>
                            )}
                          </span>
                        )}
                      </TableCell>
                      <TableCell className="pl-0 text-right text-muted-foreground">
                        <ArrowRight className="inline size-3.5" />
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
      {chosen && (
        <GroupSheet
          key={chosen.name}
          group={chosen}
          onClose={() => setOpen(null)}
          onChanged={refresh}
        />
      )}
      {adding && <NewGroupDialog onClose={() => setAdding(false)} onChanged={refresh} />}
    </Page>
  )
}

function GroupSheet({
  group,
  onClose,
  onChanged,
}: {
  group: SystemGroup
  onClose: () => void
  onChanged: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const [members, setMembers] = useState(group.members)
  const [name, setName] = useState(group.name)
  const [gid, setGid] = useState(String(group.gid))
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
  const path = `/system-accounts/groups/${encodeURIComponent(group.name)}`
  const everyone = (users ?? []).filter((u) => !group.primary.includes(u.username))
  const dirty =
    name !== group.name ||
    gid !== String(group.gid) ||
    [...members].sort().join() !== [...group.members].sort().join()

  const save = async () => {
    setBusy(true)
    try {
      await patch(
        path,
        {
          members,
          ...(gid !== String(group.gid) ? { gid: Number(gid) } : {}),
          ...(name !== group.name ? { newName: name } : {}),
        },
        { query },
      )
      notify.success("Group saved")
      onChanged()
      onClose()
    } catch (err) {
      notify.error("The group was not changed", err)
    } finally {
      setBusy(false)
    }
  }
  const remove = () =>
    confirm({
      title: `Delete the group ${group.name}`,
      confirmLabel: "Delete",
      description: <p>Files it owns keep its number, with no name behind it.</p>,
      action: async (typed) => {
        await del(path, { confirm: typed, query })
        onChanged()
        onClose()
      },
    })
  return (
    <>
      <SidePanel
        open
        onOpenChange={(open) => !open && onClose()}
        width="md"
        title={<span className="font-mono">{group.name}</span>}
        footer={
          <div className="flex w-full items-center justify-between gap-2">
            <Button
              size="sm"
              variant="ghost"
              className="text-destructive"
              disabled={!editable}
              onClick={remove}
            >
              <Trash className="size-4" />
              Delete group
            </Button>
            <Button size="sm" onClick={() => void save()} disabled={!dirty} pending={busy}>
              Save
            </Button>
          </div>
        }
      >
        <div className="grid gap-5">
          {group.sudoRules.map((rule) => (
            <p key={rule.id} className="min-w-0 text-hint text-muted-foreground">
              <span className="block font-mono text-foreground">{rule.text}</span>
              {rule.file}
            </p>
          ))}
          <div>
            <p className="pb-1 text-body font-medium">Members</p>
            {everyone.map((user) => (
              <OptionRow
                key={user.username}
                title={<span className="font-mono">{user.username}</span>}
                checked={members.includes(user.username)}
                onCheckedChange={(on) =>
                  setMembers((now) =>
                    on ? [...now, user.username] : now.filter((m) => m !== user.username),
                  )
                }
              />
            ))}
            {group.primary.length > 0 && (
              <FormNote className="pt-2">
                Its main group for {group.primary.join(", ")}, which is set on the account.
              </FormNote>
            )}
          </div>
          {group.system && !group.protected && !allowed && (
            <div className="flex flex-wrap items-center justify-between gap-2 text-hint text-muted-foreground">
              A package made this group and finds it by name and number.
              <Button size="xs" variant="outline" onClick={() => setAllowed(true)}>
                Allow changes
              </Button>
            </div>
          )}
          {editable && (
            <FieldRow>
              <Field label="Name" htmlFor="group-name">
                <Input
                  id="group-name"
                  className="font-mono"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                />
              </Field>
              <Field label="Number" htmlFor="group-gid" hint="Files keep the old one.">
                <Input
                  id="group-gid"
                  type="number"
                  value={gid}
                  onChange={(e) => setGid(e.target.value)}
                />
              </Field>
            </FieldRow>
          )}
        </div>
      </SidePanel>
      {dialog}
    </>
  )
}

function NewGroupDialog({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const create = async () => {
    setBusy(true)
    try {
      await post("/system-accounts/groups", { name })
      notify.success(`Created ${name}`)
      onChanged()
      onClose()
    } catch (err) {
      notify.error("The group was not created", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title="New group"
      footer={
        <Button onClick={() => void create()} disabled={!name.trim()} pending={busy}>
          Create
        </Button>
      }
    >
      <Field label="Name" htmlFor="new-group-name">
        <Input
          id="new-group-name"
          className="font-mono"
          autoComplete="off"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
    </Modal>
  )
}
