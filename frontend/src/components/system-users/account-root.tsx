"use client"

import { useState } from "react"
import { Pencil, Shield, ShieldCheck, Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import { Disclosure, Field, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { useAccount } from "@/components/system-users/account-page"
import { groupGrant } from "@/components/system-users/access"
import { GroupWord, groupProduct } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import { usePoll } from "@/hooks/use-poll"
import { del, get, patch, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { AccountSudo, RootRoute, SudoLevel, SudoRule, SystemGroup } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * How far the account can reach, read from sudo's own files rather than guessed
 * from a group's name, and the groups that decide it. They are one panel because
 * they are one question: `sudo` and `docker` are memberships and root access at
 * once, and an operator changing one is changing the other.
 */
export function RootPanel() {
  const { user, path, readOnly, refresh, query } = useAccount()
  const { confirm, dialog } = useConfirm()
  const [editingGroups, setEditingGroups] = useState(false)
  const { data, refresh: refreshSudo } = usePoll(
    (signal) => get<AccountSudo>(`${path}/sudo`, undefined, signal),
    0,
    [path, user.groups.join(",")],
  )
  const changed = () => {
    refreshSudo()
    refresh()
  }
  const supplementary = user.groups.filter((g) => g !== user.primaryGroup)

  const leaveGroup = (group: string) =>
    confirm({
      title: `Take ${user.username} out of ${group}`,
      confirmLabel: "Leave group",
      description: <p>It stops working at the next sign-in, not the current one.</p>,
      action: async () => {
        await patch(path, { groups: supplementary.filter((g) => g !== group) }, { query })
        changed()
      },
    })

  const removeRule = (rule: SudoRule) =>
    confirm({
      title: "Remove this sudo rule",
      confirmLabel: "Remove",
      description: (
        <p>
          Rewrites <span className="font-mono">{rule.file}</span> without it. sudo checks the file
          before it takes effect, and the old one comes back if the check fails.
        </p>
      ),
      action: async (typed) => {
        await del(`${path}/sudo/rules`, { confirm: typed, query: { ...query, id: rule.id } })
        changed()
      },
    })

  const setLevel = async (level: SudoLevel, commands: string[] = data?.managed.commands ?? []) => {
    try {
      await put(`${path}/sudo`, { level, commands }, { query })
      notify.success(level === "none" ? "This account's own rule was removed" : "sudo rule written")
      changed()
    } catch (err) {
      notify.error("sudo was not changed", err)
    }
  }

  const rules = data?.rules ?? []
  const ruleOf = (route: RootRoute) => rules.find((rule) => rule.id === route.ruleId)
  const isRoot = user.uid === 0

  return (
    <div className="flex min-w-0 flex-col gap-8">
      <Panel plain aria-label="Root access">
        <PanelHeader title="Root access" />
        <PanelBody flush>
          {user.rootRoutes.length === 0 ? (
            <p className="py-3 text-body text-muted-foreground">
              None. This account cannot become root.
            </p>
          ) : (
            <RowList className="animate-rise">
              {user.rootRoutes.map((route, index) => (
                <RootRouteRow
                  key={route.ruleId ?? route.via ?? index}
                  route={route}
                  rule={ruleOf(route)}
                  readOnly={readOnly}
                  onLeave={leaveGroup}
                  onRemove={removeRule}
                />
              ))}
            </RowList>
          )}
          {!isRoot && !readOnly && data && (
            <div className="space-y-3 pt-4">
              <Field label="This account's own rule">
                <Segments
                  label="This account's own sudo rule"
                  value={data.managed.level}
                  onChange={(level) => void setLevel(level)}
                  options={[
                    { value: "none", label: "None" },
                    { value: "password", label: "Asks for password" },
                    { value: "nopassword", label: "No password" },
                  ]}
                />
              </Field>
              {data.managed.level !== "none" && (
                <CommandsFold
                  commands={data.managed.commands}
                  onSave={(commands) => setLevel(data.managed.level, commands)}
                />
              )}
            </div>
          )}
        </PanelBody>
      </Panel>

      <Panel plain aria-label="Groups">
        <PanelHeader
          title="Groups"
          actions={
            !readOnly && (
              <Button size="xs" variant="outline" onClick={() => setEditingGroups(true)}>
                <Pencil className="size-3" />
                Edit
              </Button>
            )
          }
        />
        <PanelBody className="pt-3">
          <p className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5 text-body">
            {user.groups.map((group) => (
              <span
                key={group}
                className={cn(
                  "inline-flex items-center",
                  group === user.primaryGroup && "text-muted-foreground",
                )}
                title={group === user.primaryGroup ? "Main group" : groupGrant(group)}
              >
                <GroupWord
                  name={group}
                  sudo={user.rootRoutes.some((r) => r.kind === "sudo" && r.via === group)}
                />
              </span>
            ))}
          </p>
        </PanelBody>
      </Panel>
      {editingGroups && (
        <GroupsDialog onClose={() => setEditingGroups(false)} onChanged={changed} />
      )}
      {dialog}
    </div>
  )
}

function routeWords(route: RootRoute, rule?: SudoRule): { title: string; ink: string } {
  if (route.limited) {
    return { title: `Only ${rule?.commands.join(", ") ?? "some commands"}`, ink: "" }
  }
  if (!route.usable) {
    return { title: "Would be root, but has no password", ink: "text-muted-foreground" }
  }
  if (route.noPassword)
    return { title: "Anything as root, no password asked", ink: "text-destructive" }
  return { title: "Anything as root, asks for its password", ink: "text-warning" }
}

function RootRouteRow({
  route,
  rule,
  readOnly,
  onLeave,
  onRemove,
}: {
  route: RootRoute
  rule?: SudoRule
  readOnly: boolean
  onLeave: (group: string) => void
  onRemove: (rule: SudoRule) => void
}) {
  if (route.kind === "uid0") {
    return (
      <Row
        leading={<ProductLogo size="sm" fallback={Shield} />}
        title={<span className="text-destructive">This account is root</span>}
        subtitle="It can do anything, and nothing here can take that away."
      />
    )
  }
  if (route.kind === "group") {
    const group = route.via ?? ""
    return (
      <Row
        leading={<ProductLogo id={groupProduct(group)} size="sm" fallback={ShieldCheck} />}
        title={<span className="text-warning">Through {group}</span>}
        subtitle={groupGrant(group)}
        trailing={
          !readOnly && (
            <Button size="xs" variant="outline" onClick={() => onLeave(group)}>
              Leave group
            </Button>
          )
        }
      />
    )
  }
  const words = routeWords(route, rule)
  return (
    <Row
      leading={<ProductLogo size="sm" fallback={ShieldCheck} />}
      title={<span className={words.ink}>{words.title}</span>}
      subtitle={
        <span className="flex flex-wrap items-center gap-x-1.5">
          {route.via && <span>Through {route.via} ·</span>}
          <span className="truncate font-mono">{route.file}</span>
        </span>
      }
      trailing={
        readOnly || (rule?.managed ?? false) ? undefined : route.via ? (
          <Button size="xs" variant="outline" onClick={() => onLeave(route.via as string)}>
            Leave group
          </Button>
        ) : (
          rule && (
            <Button size="xs" variant="outline" onClick={() => onRemove(rule)}>
              <Trash className="size-3" />
              Remove
            </Button>
          )
        )
      }
    />
  )
}

function CommandsFold({
  commands,
  onSave,
}: {
  commands: string[]
  onSave: (commands: string[]) => Promise<void>
}) {
  const [text, setText] = useState(commands.join("\n"))
  const [busy, setBusy] = useState(false)
  const list = text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
  return (
    <Disclosure
      quiet
      summary="Only these commands"
      facts={commands.length > 0 ? `${commands.length} named` : "Everything"}
    >
      <div className="space-y-2 pt-2">
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={3}
          className="font-mono text-xs"
          placeholder="/usr/bin/systemctl restart nginx"
        />
        <Button
          size="xs"
          variant="outline"
          pending={busy}
          onClick={async () => {
            setBusy(true)
            await onSave(list)
            setBusy(false)
          }}
        >
          Save
        </Button>
      </div>
    </Disclosure>
  )
}

function GroupsDialog({ onClose, onChanged }: { onClose: () => void; onChanged: () => void }) {
  const { user, path, query } = useAccount()
  const [chosen, setChosen] = useState(user.groups.filter((g) => g !== user.primaryGroup))
  const [search, setSearch] = useState("")
  const [busy, setBusy] = useState(false)
  const { data } = usePoll(
    (signal) => get<SystemGroup[]>("/system-accounts/groups", undefined, signal),
    0,
    [],
  )
  // What hands over power first: those are the memberships somebody came here to
  // decide, and an alphabetical list puts `adm` above them and `sudo` on page two.
  const rank = (g: SystemGroup) =>
    g.sudoRules.length > 0 || g.rootEquivalent ? 0 : !g.system ? 1 : groupGrant(g.name) ? 2 : 3
  const groups = (data ?? [])
    .filter((g) => g.name !== user.primaryGroup && g.name.includes(search.trim().toLowerCase()))
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))

  const save = async () => {
    setBusy(true)
    try {
      await patch(path, { groups: chosen }, { query })
      notify.success("Groups saved")
      onChanged()
      onClose()
    } catch (err) {
      notify.error("The groups were not changed", err)
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={`Groups for ${user.username}`}
      footer={
        <Button onClick={() => void save()} pending={busy}>
          Save
        </Button>
      }
    >
      <div className="grid gap-2">
        <SearchInput
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          placeholder="Find a group"
        />
        <div className="max-h-96 overflow-y-auto">
          {groups.map((group) => (
            <OptionRow
              key={group.name}
              title={<GroupWord name={group.name} sudo={group.sudoRules.length > 0} />}
              hint={
                group.sudoRules.length > 0
                  ? "Runs commands as root through sudo"
                  : groupGrant(group.name)
              }
              checked={chosen.includes(group.name)}
              onCheckedChange={(on) =>
                setChosen((now) =>
                  on ? [...now, group.name] : now.filter((name) => name !== group.name),
                )
              }
            />
          ))}
        </div>
      </div>
    </Modal>
  )
}
