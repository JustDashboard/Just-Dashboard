"use client"

import { useRouter } from "next/navigation"
import { useState } from "react"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { ErrorState, LoadingRows, Notice } from "@/components/state"
import { useAccount } from "@/components/system-users/account-page"
import { AccountMark } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { usePoll } from "@/hooks/use-poll"
import { del, get } from "@/lib/api"
import { bytes, plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { AccountRemoval, SystemUser } from "@/lib/types"

type HomeChoice = "keep" | "archive" | "delete"

const NOBODY = "__nobody__"

/**
 * Deleting an account, with what would go laid out before anything does. A home
 * is the obvious part; the rest is what bites later: a process running under a
 * number nobody owns, a file elsewhere that the next account given that number
 * inherits. Removing a home with no archive is the one choice nothing brings
 * back, so it alone asks for the name to be typed, and the server checks it.
 */
export function RemoveAccountDialog({ onClose }: { onClose: () => void }) {
  const router = useRouter()
  const { user, path, query } = useAccount()
  const [home, setHome] = useState<HomeChoice>("keep")
  const [endSessions, setEndSessions] = useState(true)
  const [transferTo, setTransferTo] = useState(NOBODY)
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const { data, error, loading } = usePoll(
    (signal) => get<AccountRemoval>(`${path}/removal`, undefined, signal),
    0,
    [path],
  )
  const { data: people } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", undefined, signal),
    0,
    [],
  )
  const others = (people ?? []).filter((u) => !u.system && u.username !== user.username)
  const blocked = Boolean(data?.blocked)
  const shared = data?.sharesNumber
  const needsPhrase = home === "delete"
  const ready = data && !blocked && (!needsPhrase || typed === user.username)

  const remove = async () => {
    setBusy(true)
    try {
      const result = await del<{ archive?: string }>(path, {
        confirm: needsPhrase ? typed : "",
        query: {
          ...query,
          home: shared ? "keep" : home,
          endSessions: shared ? false : endSessions,
          ...(!shared && transferTo !== NOBODY ? { transferTo } : {}),
        },
      })
      notify.success(
        result?.archive
          ? `${user.username} was deleted. Its home is in ${result.archive}`
          : `${user.username} was deleted`,
      )
      router.push("/system-users")
    } catch (err) {
      notify.error("The account was not deleted", err)
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={
        <span className="flex items-center gap-2">
          <AccountMark user={user} size="sm" />
          <span>
            Delete <span className="font-mono">{user.username}</span>
          </span>
        </span>
      }
      footer={
        <Button
          variant="destructive"
          onClick={() => void remove()}
          disabled={!ready}
          pending={busy}
        >
          Delete account
        </Button>
      }
    >
      {loading && !data && <LoadingRows rows={3} />}
      {error && <ErrorState error={error} />}
      {data && (
        <div className="grid gap-4" data-slot="account-removal">
          {data.blocked && (
            <Notice tone="danger" title="This account cannot be deleted">
              {data.blocked}
            </Notice>
          )}
          <FormFacts>
            <FormFact label="Home" mono>
              {data.home}
            </FormFact>
            {data.homeExists && <FormFact label="Holds">{bytes(data.homeBytes)}</FormFact>}
            <FormFact label="Running">{plural(data.processes, "process", "processes")}</FormFact>
            <FormFact label="Keys">{data.sshKeys}</FormFact>
            {data.cronJobs && <FormFact label="Crontab">Goes with it</FormFact>}
            {data.sudoRules.length > 0 && (
              <FormFact label="sudo">{plural(data.sudoRules.length, "rule")} go with it</FormFact>
            )}
          </FormFacts>
          {data.elsewhere.length > 0 && (
            <Notice
              tone="warning"
              title={`It also owns files elsewhere${data.elsewhereTruncated ? ", at least" : ""}: ${data.elsewhere.length}`}
            >
              <span className="block font-mono">{data.elsewhere.slice(0, 5).join("\n")}</span>
              Left alone, they belong to whichever account is next given this number. Hand them to
              someone below.
            </Notice>
          )}
          {shared && !data.blocked && (
            <Notice tone="warning" title={`It has the same number as ${shared}`}>
              A process or a file has a number and no name, so everything here is {shared}&apos;s
              too. Deleting this removes the name and touches nothing else.
            </Notice>
          )}
          {!data.blocked && !shared && (
            <>
              <Field label="Its home directory">
                <Segments
                  label="Its home directory"
                  value={home}
                  onChange={(next) => {
                    setHome(next)
                    setTyped("")
                  }}
                  fill
                  options={[
                    { value: "keep", label: "Keep it" },
                    { value: "archive", label: "Archive, then remove", disabled: data.homeUnsafe },
                    { value: "delete", label: "Remove", disabled: data.homeUnsafe },
                  ]}
                />
              </Field>
              {data.homeUnsafe && (
                <FormNote>
                  {data.home} is shared or too near the root to remove, so it stays.
                </FormNote>
              )}
              {home === "archive" && (
                <FormNote>
                  It goes to a root-only file under /var/backups/just-dashboard/accounts before
                  anything is removed.
                </FormNote>
              )}
              {home === "keep" && others.length > 0 && (
                <Field label="Hand its files to" htmlFor="removal-transfer">
                  <Select value={transferTo} onValueChange={setTransferTo}>
                    <SelectTrigger id="removal-transfer">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value={NOBODY}>Nobody, leave them as they are</SelectItem>
                      {others.map((other) => (
                        <SelectItem key={other.username} value={other.username}>
                          {other.username}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              )}
              {data.processes > 0 && (
                <OptionRow
                  title="End what it is running"
                  hint="Without this the delete is refused while it has processes."
                  checked={endSessions}
                  onCheckedChange={setEndSessions}
                />
              )}
              {needsPhrase && (
                <Field
                  label={`Type ${user.username} to remove its home for good`}
                  htmlFor="removal-phrase"
                >
                  <Input
                    id="removal-phrase"
                    className="font-mono"
                    autoComplete="off"
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                  />
                </Field>
              )}
            </>
          )}
        </div>
      )}
    </Modal>
  )
}
