"use client"

import { useState } from "react"
import { GitHubMark } from "@/components/icons"
import { post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { GitHubManifestStart } from "@/lib/github-types"
import { Modal } from "@/components/modal"
import { Field, OptionList, OptionRow } from "@/components/form"
import { Segments } from "@/components/segments"
import { Explain } from "@/components/explain"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { MANIFEST_PERMISSIONS, loginProblem, permissionWords } from "@/components/github/list-model"

/**
 * Posts the manifest to GitHub the way GitHub requires it: a form, from the
 * browser. The deploy App's card does the same (`deploy/github-app-card.tsx`).
 */
function submitManifest(start: GitHubManifestStart) {
  const form = document.createElement("form")
  form.method = "post"
  form.action = start.action
  const field = document.createElement("input")
  field.type = "hidden"
  field.name = "manifest"
  field.value = JSON.stringify(start.manifest)
  form.appendChild(field)
  document.body.appendChild(form)
  form.submit()
}

/**
 * Creating the dashboard's repository App, for an administrator. GitHub's
 * manifest flow: the server writes the manifest from the browser's own
 * address (so a private address works: every hop is a browser navigation),
 * the page posts it to GitHub, GitHub asks the person to confirm and sends
 * the browser back to `/github/callback`, which finishes it.
 */
export function SetupDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [name, setName] = useState("")
  const [owner, setOwner] = useState<"me" | "org">("me")
  const [organization, setOrganization] = useState("")
  const [shared, setShared] = useState(true)
  const [starting, setStarting] = useState(false)
  const orgProblem = owner === "org" ? loginProblem(organization.trim()) : undefined

  const begin = async () => {
    setStarting(true)
    try {
      const start = await post<GitHubManifestStart>("/github/app/manifest", {
        origin: window.location.origin,
        organization: owner === "org" ? organization.trim() : undefined,
        name: name.trim() || undefined,
        public: shared,
      })
      submitManifest(start)
    } catch (error) {
      notify.error("Could not start the GitHub App setup", error)
      setStarting(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !starting && onOpenChange(next)}
      title="Set up GitHub"
      description="Create this dashboard's GitHub App, which every account here connects through."
      size="md"
      progress={starting && { label: "Opening GitHub…" }}
      footer={
        <>
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={starting}>
            Cancel
          </Button>
          <Button onClick={() => void begin()} disabled={Boolean(orgProblem)} pending={starting}>
            <GitHubMark className="size-4" />
            Continue on GitHub
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="App name" htmlFor="github-app-name" hint="Left empty, the server picks one.">
          <Input
            id="github-app-name"
            autoComplete="off"
            value={name}
            maxLength={34}
            onChange={(event) => setName(event.target.value)}
            placeholder="Just Dashboard Repos"
          />
        </Field>
        <Field label="Owner">
          <Segments
            value={owner}
            onChange={setOwner}
            options={[
              { value: "me", label: "My account" },
              { value: "org", label: "An organisation" },
            ]}
          />
        </Field>
        {owner === "org" && (
          <Field label="Organisation" htmlFor="github-app-org" error={organization && orgProblem}>
            <Input
              id="github-app-org"
              autoComplete="off"
              spellCheck={false}
              value={organization}
              onChange={(event) => setOrganization(event.target.value)}
              placeholder="northwind"
              className="font-mono"
            />
          </Field>
        )}
        <OptionList>
          <OptionRow
            title={
              <span className="inline-flex items-center gap-1.5">
                Let organisations and other accounts install it
                <Explain label="What installing it elsewhere means">
                  Another account can install the App on its own repositories. Nothing reaches this
                  dashboard from that alone: a person still signs in here with their own GitHub
                  account, and sees only what that account may see.
                </Explain>
              </span>
            }
            checked={shared}
            onCheckedChange={setShared}
          />
        </OptionList>
        <div className="space-y-1.5">
          <p className="text-xs font-medium text-muted-foreground">It will ask to</p>
          <ul className="space-y-1 text-body" aria-label="What the App asks for">
            {permissionWords(MANIFEST_PERMISSIONS).map((words) => (
              <li key={words} className="flex gap-2">
                <span aria-hidden className="text-faint">
                  ·
                </span>
                {words}
              </li>
            ))}
          </ul>
        </div>
      </div>
    </Modal>
  )
}
