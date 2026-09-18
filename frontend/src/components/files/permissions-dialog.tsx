"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import type { FileEntry } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Checkbox } from "@/components/ui/checkbox"

import { Modal } from "@/components/modal"

type Scope = "owner" | "group" | "other"
const SCOPES: Scope[] = ["owner", "group", "other"]
const BITS = [
  { key: "r", label: "Read", value: 4 },
  { key: "w", label: "Write", value: 2 },
  { key: "x", label: "Execute", value: 1 },
] as const

/**
 * chmod and chown for one entry, together because they are the same question —
 * "who may do what with this" — asked twice, and splitting them made people
 * open two dialogs to set a file up. The permission grid and the octal box are
 * two views of one number kept in sync, so an expert types 644 and everyone
 * else ticks boxes. Both operations need system.admin; the caller gates the
 * entry point, this just does the work.
 */
export function PermissionsDialog({
  entry,
  onOpenChange,
  onDone,
}: {
  entry: FileEntry | null
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  if (!entry) return null
  return <Body key={entry.path} entry={entry} onOpenChange={onOpenChange} onDone={onDone} />
}

function Body({
  entry,
  onOpenChange,
  onDone,
}: {
  entry: FileEntry
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  // The last three octal digits are the rwx we edit; a leading special digit is
  // left alone (the backend's chmod only applies the permission bits anyway).
  const initialOctal = (entry.modeOctal || "0644").replace(/\D/g, "").slice(-3).padStart(3, "0")
  const [octal, setOctal] = useState(initialOctal)
  const [owner, setOwner] = useState(entry.owner)
  const [group, setGroup] = useState(entry.group)
  const [recursive, setRecursive] = useState(false)
  const [busy, setBusy] = useState(false)

  const digits = octal
    .padStart(3, "0")
    .split("")
    .map((d) => parseInt(d, 10) || 0)
  const setBit = (scopeIndex: number, value: number, on: boolean) => {
    const next = [...digits]
    next[scopeIndex] = on ? next[scopeIndex] | value : next[scopeIndex] & ~value
    setOctal(next.join(""))
  }

  const modeChanged = octal !== initialOctal
  const ownerChanged = owner !== entry.owner || group !== entry.group
  const isSymlink = entry.isSymlink

  const apply = async () => {
    setBusy(true)
    try {
      if (modeChanged && !isSymlink) {
        await post("/files/chmod", { path: entry.path, mode: octal, recursive })
      }
      if (ownerChanged) {
        await post("/files/chown", {
          path: entry.path,
          owner: owner === entry.owner ? "" : owner,
          group: group === entry.group ? "" : group,
          recursive,
        })
      }
      notify.success("Permissions updated", { description: entry.name })
      onDone()
      onOpenChange(false)
    } catch (err) {
      notify.error("Could not update permissions", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="sm"
      title={entry.name}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={apply} disabled={busy || (!modeChanged && !ownerChanged)} pending={busy}>
            Apply
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {isSymlink && (
          <p className="rounded-md border border-hairline bg-muted/40 px-2.5 py-1.5 text-hint text-muted-foreground">
            This is a symlink — its mode cannot be changed (Linux has no lchmod), but its owner can.
          </p>
        )}

        {!isSymlink && (
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <Label className="text-xs text-muted-foreground">Permissions</Label>
              <Input
                value={octal}
                onChange={(e) => setOctal(e.target.value.replace(/\D/g, "").slice(0, 3))}
                className="h-7 w-16 text-center font-mono text-xs"
                aria-label="Octal mode"
              />
            </div>
            <div className="overflow-hidden rounded-md border border-hairline">
              <table className="w-full text-center text-xs">
                <thead>
                  <tr className="border-b border-hairline bg-surface-header text-hint text-muted-foreground">
                    <th className="py-1.5 ps-3 text-left font-normal"></th>
                    {BITS.map((b) => (
                      <th key={b.key} className="py-1.5 font-normal">
                        {b.label}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {SCOPES.map((scope, si) => (
                    <tr key={scope} className="border-t border-hairline first:border-t-0">
                      <td className="py-1.5 ps-3 text-left text-xs capitalize">{scope}</td>
                      {BITS.map((b) => (
                        <td key={b.key} className="py-1.5">
                          <Checkbox
                            className="mx-auto"
                            checked={(digits[si] & b.value) !== 0}
                            onCheckedChange={(v) => setBit(si, b.value, v === true)}
                            aria-label={`${scope} ${b.label}`}
                          />
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="perm-owner" className="text-xs text-muted-foreground">
              Owner
            </Label>
            <Input
              id="perm-owner"
              value={owner}
              onChange={(e) => setOwner(e.target.value)}
              className="h-8 font-mono text-xs"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="perm-group" className="text-xs text-muted-foreground">
              Group
            </Label>
            <Input
              id="perm-group"
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              className="h-8 font-mono text-xs"
            />
          </div>
        </div>

        {entry.isDir && (
          <label className="flex items-center gap-2 text-xs">
            <Checkbox checked={recursive} onCheckedChange={(v) => setRecursive(v === true)} />
            Apply to everything inside this folder
          </label>
        )}
      </div>
    </Modal>
  )
}
