"use client"

import { useState } from "react"
import { Cross, LockOpen, SettingsSliders, Shield, Slash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import type { Fail2banJail, JailConfig, JailParamResult } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, EmptyState, Notice } from "@/components/state"
import { IconAction, RowActions } from "@/components/icon-action"
import { SidePanel } from "@/components/side-panel"
import { RowLink } from "@/components/page"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * Every jail in one table, with the addresses it is holding one click away.
 *
 * This was a card per jail in a two-column grid, and a jail is not a card's
 * worth of anything: a busy sshd jail with four bans and an idle nginx jail
 * with none were the same size box, so the grid ended every visit with a
 * ragged edge and a half-page of empty card. Three jails is a table of three
 * rows — the numbers line up in a column, which is the only way "which jail is
 * doing the work" is answerable at a glance — and the addresses, which are the
 * part you act on, get the detail surface the rest of the product uses for
 * exactly that.
 */
export function JailsPanel({
  jails,
  canManage,
  onChanged,
}: {
  jails: Fail2banJail[]
  canManage: boolean
  onChanged: () => void
}) {
  const [open, setOpen] = useState<string | null>(null)
  const [tuning, setTuning] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true)
    try {
      await fn()
      notify.success(ok)
      onChanged()
    } catch (err) {
      notify.error("Could not apply", err)
    } finally {
      setBusy(false)
    }
  }

  const selected = jails.find((j) => j.name === open)

  return (
    <>
      <Panel>
        <PanelHeader
          icon={Slash}
          title="Jails"
          actions={
            <Status
              verdict={jails.some((j) => j.currentlyBanned > 0) ? "notice" : "ok"}
              label={`${jails.reduce((n, j) => n + j.currentlyBanned, 0)} banned now`}
            />
          }
        />
        <PanelBody flush>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Jail</TableHead>
                <TableHead>Banned now</TableHead>
                <TableHead>Failing now</TableHead>
                <TableHead>Bans in total</TableHead>
                <TableHead>Failures in total</TableHead>
                <TableHead className="w-full">Watching</TableHead>
                <TableHead className="w-px" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {jails.map((jail) => (
                <TableRow key={jail.name} className="group" onActivate={() => setOpen(jail.name)}>
                  <TableCell>
                    <RowLink onClick={() => setOpen(jail.name)}>{jail.name}</RowLink>
                  </TableCell>
                  <TableCell>
                    <span
                      className={countClass(jail.currentlyBanned > 0)}
                    >{`${jail.currentlyBanned}`}</span>
                  </TableCell>
                  <TableCell>
                    <span
                      className={countClass(jail.currentlyFailed > 0)}
                    >{`${jail.currentlyFailed}`}</span>
                  </TableCell>
                  <TableCell className="numeric text-muted-foreground">
                    {jail.totalBanned}
                  </TableCell>
                  <TableCell className="numeric text-muted-foreground">
                    {jail.totalFailed}
                  </TableCell>
                  <TableCell className="font-mono text-hint text-muted-foreground">
                    {jail.fileList.join(", ") || "—"}
                  </TableCell>
                  <TableCell>
                    {canManage && (
                      <RowActions className="justify-end">
                        <IconAction
                          label={`Tune ${jail.name}`}
                          onClick={() => setTuning(jail.name)}
                        >
                          <SettingsSliders />
                        </IconAction>
                        <IconAction
                          label={`Release every ban in ${jail.name}`}
                          disabled={busy || jail.bannedIps.length === 0}
                          onClick={() =>
                            act(
                              () =>
                                post(`/fail2ban/${encodeURIComponent(jail.name)}/unban-all`, {}),
                              `Released every ban in ${jail.name}`,
                            )
                          }
                        >
                          <LockOpen />
                        </IconAction>
                      </RowActions>
                    )}
                  </TableCell>
                </TableRow>
              ))}
              {jails.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="p-0">
                    <EmptyState
                      icon={Slash}
                      title="No jails configured"
                      description="A running fail2ban with no jails bans nobody. Enable at least the sshd jail."
                      className="border-0"
                    />
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </PanelBody>
      </Panel>

      <SidePanel
        open={Boolean(selected)}
        onOpenChange={(o) => !o && setOpen(null)}
        icon={Slash}
        width="sm"
        title={selected?.name ?? ""}
        description={
          selected
            ? `${selected.currentlyBanned} banned now · ${selected.totalBanned} in total · ${selected.currentlyFailed} failing`
            : undefined
        }
        actions={
          canManage &&
          selected && (
            <>
              <Button size="xs" variant="outline" onClick={() => setTuning(selected.name)}>
                <SettingsSliders className="size-3.5" />
                Tune
              </Button>
              <Button
                size="xs"
                variant="ghost"
                disabled={busy || selected.bannedIps.length === 0}
                onClick={() =>
                  act(
                    () => post(`/fail2ban/${encodeURIComponent(selected.name)}/unban-all`, {}),
                    `Released every ban in ${selected.name}`,
                  )
                }
              >
                Release all
              </Button>
            </>
          )
        }
      >
        {selected && selected.bannedIps.length === 0 ? (
          <p className="text-body leading-relaxed text-muted-foreground">
            Nothing currently banned. Bans expire, so an empty list is not the same as a quiet night
            — the ban activity on the page behind this is the record.
          </p>
        ) : (
          <div className="divide-y divide-hairline">
            {selected?.bannedIps.map((ip) => (
              <div key={ip} className="flex items-center justify-between gap-2 py-1.5">
                <span className="truncate font-mono text-xs">{ip}</span>
                {/* Always visible, not revealed on hover: in a table the
                    actions are an aside to the data, but this sheet was opened
                    *to* release an address, so hiding the release button until
                    the pointer finds the row hides the only thing here. */}
                {canManage && (
                  <span className="flex shrink-0 items-center gap-0.5">
                    <IconAction
                      label={`Unban ${ip}`}
                      disabled={busy}
                      onClick={() =>
                        act(
                          () =>
                            post(`/fail2ban/${encodeURIComponent(selected.name)}/unban`, { ip }),
                          `${ip} unbanned`,
                        )
                      }
                    >
                      <LockOpen />
                    </IconAction>
                    <IconAction
                      label={`Never ban ${ip} again`}
                      disabled={busy}
                      onClick={() =>
                        act(
                          () =>
                            post(`/fail2ban/${encodeURIComponent(selected.name)}/ignore`, {
                              ip,
                              add: true,
                            }),
                          `${ip} added to the allowlist`,
                        )
                      }
                    >
                      <Shield />
                    </IconAction>
                  </span>
                )}
              </div>
            ))}
          </div>
        )}
      </SidePanel>

      {tuning && (
        <JailTuning
          jail={tuning}
          open
          onOpenChange={(o) => !o && setTuning(null)}
          onSaved={onChanged}
        />
      )}
    </>
  )
}

/** A count that goes quiet at zero — a column of grey noughts is not a signal. */
function countClass(active: boolean) {
  return active ? "numeric text-xs font-medium" : "numeric text-xs text-muted-foreground"
}

/**
 * One jail's policy: the three numbers that decide whether it is doing
 * anything.
 *
 * A dashboard that can only unban one address at a time is a viewer with a
 * button. How many failures, in what window, for how long is the whole policy,
 * and it lives in a file whose layout differs by distribution.
 * fail2ban-client can set them on the running server, which is both easier to
 * get right and honest about what is in force now rather than what a file
 * says.
 */
function JailTuning({
  jail,
  open,
  onOpenChange,
  onSaved,
}: {
  jail: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const { data, refresh } = usePoll<JailConfig>(
    (signal) => get(`/fail2ban/${encodeURIComponent(jail)}/config`, undefined, signal),
    0,
    [jail],
    { enabled: open },
  )
  const [pending, setPending] = useState<Record<string, string>>({})
  const [ignore, setIgnore] = useState("")
  const [busy, setBusy] = useState(false)

  const value = (key: keyof JailConfig, fallback: number) =>
    pending[key] ?? String((data?.[key] as number | undefined) ?? fallback)

  const save = async () => {
    setBusy(true)
    try {
      // One request for the whole policy: the three numbers are one setting —
      // this many failures inside this window earns this long a ban — and
      // sending them separately leaves a jail half-tuned if the tab closes.
      const params: Record<string, number> = {}
      for (const [param, raw] of Object.entries(pending)) {
        const parsed = Number(raw)
        if (Number.isFinite(parsed)) params[param] = parsed
      }
      const res = await post<JailParamResult>(`/fail2ban/${encodeURIComponent(jail)}/config`, {
        params,
      })
      if (res.warning) {
        notify.warning(`${jail} partly updated`, { description: res.warning })
      } else {
        notify.success(`${jail} updated`, {
          description: res.persisted
            ? `Applied now and written to ${res.file}, so it survives a restart.`
            : undefined,
        })
      }
      setPending({})
      refresh()
      onSaved()
    } catch (err) {
      notify.error("Could not update the jail", err)
    } finally {
      setBusy(false)
    }
  }

  const addIgnore = async () => {
    try {
      await post(`/fail2ban/${encodeURIComponent(jail)}/ignore`, { ip: ignore, add: true })
      setIgnore("")
      refresh()
    } catch (err) {
      notify.error("Could not allowlist", err)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="sm"
      icon={SettingsSliders}
      title={<>Tune {jail}</>}
      description="This many failures inside this window earns a ban of this length."
      footer={
        <>
          <span className="flex-1" />
          <Button onClick={save} disabled={busy || Object.keys(pending).length === 0}>
            Apply
          </Button>
        </>
      }
    >
      <div className="grid gap-4">
        <div className="grid gap-3 sm:grid-cols-3">
          <NumberField
            label="Failures"
            hint="before a ban"
            value={value("maxRetry", 5)}
            onChange={(v) => setPending((p) => ({ ...p, maxretry: v }))}
          />
          <NumberField
            label="Window"
            hint="seconds"
            value={value("findTime", 600)}
            onChange={(v) => setPending((p) => ({ ...p, findtime: v }))}
          />
          <NumberField
            label="Ban for"
            hint="seconds"
            value={value("banTime", 600)}
            onChange={(v) => setPending((p) => ({ ...p, bantime: v }))}
          />
        </div>

        <div className="space-y-1.5">
          <Label>Never ban</Label>
          <div className="flex flex-wrap gap-1.5">
            {data?.ignoreIp.map((ip) => (
              <Tag key={ip} mono>
                {ip}
                <button
                  type="button"
                  aria-label={`Stop allowlisting ${ip}`}
                  className="text-muted-foreground hover:text-destructive"
                  onClick={async () => {
                    try {
                      await post(`/fail2ban/${encodeURIComponent(jail)}/ignore`, {
                        ip,
                        add: false,
                      })
                      refresh()
                    } catch (err) {
                      notify.error("Could not remove", err)
                    }
                  }}
                >
                  <Cross className="size-3" />
                </button>
              </Tag>
            ))}
            {data?.ignoreIp.length === 0 && (
              <EmptyNote className="py-2">Nothing allowlisted.</EmptyNote>
            )}
          </div>
          <div className="flex gap-2">
            <Input
              value={ignore}
              onChange={(e) => setIgnore(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && ignore && addIgnore()}
              placeholder="203.0.113.9 or 10.0.0.0/8"
              className="font-mono text-xs"
            />
            <Button size="sm" variant="outline" onClick={addIgnore} disabled={!ignore}>
              Add
            </Button>
          </div>
        </div>

        <Notice title="In force until fail2ban restarts">
          The change is applied to the running server, not written to jail.local — that file&rsquo;s
          layout differs by distribution, and getting it wrong disables intrusion prevention without
          saying so. Tighten it here, then make it permanent in the file.
        </Notice>
      </div>
    </Modal>
  )
}

function NumberField({
  label,
  hint,
  value,
  onChange,
}: {
  label: string
  hint: string
  value: string
  onChange: (value: string) => void
}) {
  return (
    <div className="space-y-1.5">
      <Label>{label}</Label>
      <Input value={value} inputMode="numeric" onChange={(e) => onChange(e.target.value)} />
      <p className="text-hint text-muted-foreground">{hint}</p>
    </div>
  )
}
