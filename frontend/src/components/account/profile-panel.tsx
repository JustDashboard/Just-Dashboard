"use client"

import { useRef, useState } from "react"
import { del, patch, postForm } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DashboardUser } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { Field, FieldRow, InfoTip } from "@/components/form"
import { useSaveBarEntry } from "@/components/deploy/settings/save-bar"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { FactDot } from "@/components/metrics/host-identity"
import { EditedTag } from "@/components/settings-column"
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
 * The profile as one section of the account's settings column: the picture
 * and the two names under a head that says how the rail and the audit log
 * will draw them.
 *
 * It was three columns across the page — the picture, the fields, and a card
 * of the rail's own beside them — with a sentence under each. The card is the
 * head's line now, drawn from the draft, so the initials and their hue (taken
 * from the username) still change as the names are typed; what each field is
 * for is behind the ⓘ beside its name. The picture applies the moment it is
 * chosen; the names are a draft on the settings pages' one Save at the foot
 * of the window, which comes up with the first keystroke and leaves when the
 * profile in the auth status matches what is typed, and *Edited* stands in
 * the head for as long as it is up.
 */
export function ProfileSection({ user }: { user: DashboardUser }) {
  const { refresh } = useAuth()
  const [displayName, setDisplayName] = useState(displayNameOf(user))
  const [username, setUsername] = useState(user.username)
  const [saving, setSaving] = useState(false)
  const [uploading, setUploading] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null)

  const nameChanged = displayName.trim() !== displayNameOf(user)
  const usernameChanged = username.trim() !== user.username
  const dirty = nameChanged || usernameChanged
  const invalid = !displayName.trim() || !username.trim()

  const saveAll = useSaveBarEntry(
    true,
    {
      name: "Profile",
      dirty,
      changes: Number(nameChanged) + Number(usernameChanged),
      saving,
      invalid,
      applies: "immediately",
    },
    {
      save: async () => {
        setSaving(true)
        try {
          const body: { displayName?: string; username?: string } = {}
          if (nameChanged) body.displayName = displayName.trim()
          if (usernameChanged) body.username = username.trim()
          const updated = await patch<DashboardUser>("/account/profile", body)
          setDisplayName(updated.displayName)
          setUsername(updated.username)
          await refresh()
          return true
        } catch (err) {
          notify.error("Could not save the profile", err)
          return false
        } finally {
          setSaving(false)
        }
      },
      discard: () => {
        setDisplayName(displayNameOf(user))
        setUsername(user.username)
      },
    },
  )

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

  const draft = {
    ...user,
    displayName: displayName.trim() || user.username,
    username: username.trim() || user.username,
  }

  return (
    <SettingSection
      title="Profile"
      status={dirty ? <EditedTag /> : undefined}
      state={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
          <span
            role="img"
            aria-label={`The rail draws this account as ${displayNameOf(draft)}, ${user.role}`}
            className="inline-flex min-w-0 items-center gap-1.5"
          >
            <UserAvatar key={user.avatarVersion} user={draft} size="xs" />
            <span className="truncate font-medium text-foreground">{displayNameOf(draft)}</span>
            <span className="shrink-0 capitalize">{user.role}</span>
          </span>
          <span>in the rail</span>
          <FactDot />
          <span className="truncate">
            <span className="font-mono text-[var(--tag-blue)]">@{draft.username}</span> in the audit
            log
          </span>
        </span>
      }
    >
      <div className="flex min-w-0 items-center gap-4">
        <UserAvatar key={user.avatarVersion} user={draft} size="xl" />
        <div className="flex min-w-0 flex-wrap items-center gap-2">
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
          <InfoTip label="What happens to the picture">
            Any image. It is cropped to a square and shrunk to {AVATAR_EDGE} px before it leaves the
            browser.
          </InfoTip>
          <input
            ref={fileInput}
            type="file"
            accept="image/*"
            // Opened by the button beside it, which is what a keyboard reaches.
            aria-label="Choose a picture to upload"
            tabIndex={-1}
            className="sr-only"
            onChange={(e) => upload(e.target.files?.[0])}
          />
        </div>
      </div>

      {/* A form, so Enter in either field is the bar's Save. */}
      <form
        aria-label="Profile"
        onSubmit={(event) => {
          event.preventDefault()
          void saveAll?.()
        }}
      >
        <FieldRow>
          <Field
            label="Name"
            htmlFor="profile-name"
            info="How you appear in the rail and the users list."
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
            info="What you sign in with, and how the audit log names you. One word; case does not matter."
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
        {/* Enter submits a form only where it has a submit control. This
            one is never drawn or reached: the floating bar is the control. */}
        <button type="submit" hidden tabIndex={-1} aria-label="Save" />
      </form>
    </SettingSection>
  )
}
