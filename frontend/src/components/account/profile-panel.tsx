"use client"

import { useRef, useState } from "react"
import { del, patch, postForm } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DashboardUser } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Field, FieldRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { UserAvatar, displayNameOf } from "@/components/account/user-avatar"

/** The square the picture is drawn into before upload. */
const AVATAR_EDGE = 256

/**
 * A photo, squared and shrunk in the browser. The server keeps what it is
 * sent, so the sending side is where a 4 MB phone photo becomes the 20 KB
 * square the rail actually draws — and what is uploaded is always a PNG the
 * server can prove is one.
 */
async function squareImage(file: File): Promise<Blob> {
  const url = URL.createObjectURL(file)
  try {
    const img = await new Promise<HTMLImageElement>((resolve, reject) => {
      const el = new Image()
      el.onload = () => resolve(el)
      el.onerror = () => reject(new Error("That file is not an image the browser can read"))
      el.src = url
    })
    const side = Math.min(img.naturalWidth, img.naturalHeight)
    const canvas = document.createElement("canvas")
    canvas.width = AVATAR_EDGE
    canvas.height = AVATAR_EDGE
    const ctx = canvas.getContext("2d")
    if (!ctx) throw new Error("The browser could not prepare the picture")
    ctx.drawImage(
      img,
      (img.naturalWidth - side) / 2,
      (img.naturalHeight - side) / 2,
      side,
      side,
      0,
      0,
      AVATAR_EDGE,
      AVATAR_EDGE,
    )
    return await new Promise<Blob>((resolve, reject) =>
      canvas.toBlob(
        (blob) => (blob ? resolve(blob) : reject(new Error("Could not encode the picture"))),
        "image/png",
      ),
    )
  } finally {
    URL.revokeObjectURL(url)
  }
}

/**
 * The picture and the two names, side by side.
 *
 * No frame: the section title above is the block's edge. The form only offers
 * to save what has actually changed, so the button is the reader's answer to
 * "did that take" — it goes quiet again once the profile in the auth status
 * matches what is typed.
 */
export function ProfilePanel({ user }: { user: DashboardUser }) {
  const { refresh } = useAuth()
  const [displayName, setDisplayName] = useState(displayNameOf(user))
  const [username, setUsername] = useState(user.username)
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const dirty = displayName.trim() !== displayNameOf(user) || username.trim() !== user.username

  const save = async () => {
    setSaving(true)
    try {
      const body: { displayName?: string; username?: string } = {}
      if (displayName.trim() !== displayNameOf(user)) body.displayName = displayName.trim()
      if (username.trim() !== user.username) body.username = username.trim()
      const updated = await patch<DashboardUser>("/account/profile", body)
      setDisplayName(updated.displayName)
      setUsername(updated.username)
      await refresh()
      notify.success("Profile saved")
    } catch (err) {
      notify.error("Could not save the profile", err)
    } finally {
      setSaving(false)
    }
  }

  const upload = async (file: File | undefined) => {
    if (!file) return
    setUploading(true)
    try {
      const blob = await squareImage(file)
      const form = new FormData()
      form.append("file", blob, "avatar.png")
      await postForm("/account/avatar", form)
      await refresh()
      notify.success("Picture updated")
    } catch (err) {
      notify.error("Could not set the picture", err)
    } finally {
      setUploading(false)
      if (fileInput.current) fileInput.current.value = ""
    }
  }

  const remove = async () => {
    setUploading(true)
    try {
      await del("/account/avatar")
      await refresh()
      notify.success("Picture removed")
    } catch (err) {
      notify.error("Could not remove the picture", err)
    } finally {
      setUploading(false)
    }
  }

  return (
    <div className="grid gap-6 md:grid-cols-[auto_minmax(0,1fr)] md:gap-10">
      <div className="flex items-start gap-4">
        <UserAvatar key={user.avatarVersion} user={user} size="xl" />
        <div className="space-y-2">
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="outline"
              pending={uploading}
              disabled={uploading}
              onClick={() => fileInput.current?.click()}
            >
              {user.avatarVersion ? "Change picture" : "Upload picture"}
            </Button>
            {user.avatarVersion > 0 && (
              <Button size="sm" variant="ghost" disabled={uploading} onClick={remove}>
                Remove
              </Button>
            )}
          </div>
          <p className="max-w-56 text-hint leading-relaxed text-muted-foreground">
            Any image. It is cropped to a square and shrunk to {AVATAR_EDGE} px before it leaves the
            browser.
          </p>
          <input
            ref={fileInput}
            type="file"
            accept="image/*"
            className="sr-only"
            onChange={(e) => upload(e.target.files?.[0])}
          />
        </div>
      </div>

      <div className="max-w-xl space-y-4">
        <FieldRow>
          <Field
            label="Name"
            htmlFor="profile-name"
            hint="How you appear in the rail and the users list."
          >
            <Input
              id="profile-name"
              value={displayName}
              maxLength={64}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </Field>
          <Field
            label="Username"
            htmlFor="profile-username"
            hint="What you sign in with, and how the audit log names you. One word; case does not matter."
          >
            <Input
              id="profile-username"
              value={username}
              maxLength={64}
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
        </FieldRow>
        <div className="flex items-center gap-2">
          <Button size="sm" onClick={save} disabled={!dirty || saving} pending={saving}>
            Save changes
          </Button>
          {dirty && (
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setDisplayName(displayNameOf(user))
                setUsername(user.username)
              }}
            >
              Discard
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}
