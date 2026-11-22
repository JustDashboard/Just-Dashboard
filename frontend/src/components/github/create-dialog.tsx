"use client"

import { useMemo, useState } from "react"
import { useRouter } from "next/navigation"
import { Plus } from "@/components/icons"
import { ApiError, errorMessage, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { githubRepoHref } from "@/lib/github"
import type {
  GitHubCreateOptions,
  GitHubCreateRepository,
  GitHubCreateResult,
} from "@/lib/github-types"
import { usePoll } from "@/hooks/use-poll"
import { Modal } from "@/components/modal"
import { Field, FieldRow, OptionList, OptionRow } from "@/components/form"
import { Segments } from "@/components/segments"
import { Explain } from "@/components/explain"
import { ErrorState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { githubRepoName } from "@/components/github/list-model"

const NONE = "__none"

/**
 * A new repository on GitHub, through one connected account: on the account
 * itself or an organisation the App is installed on. GitHub's name rule is
 * held as it is typed, and the name GitHub will use is said when it differs;
 * a refusal from GitHub lands on the field it names. A repository the App
 * cannot reach yet (an installation on selected repositories) is said, and
 * the dialog stays rather than opening a page that would not load.
 */
export function CreateRepositoryDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
  connection: number
  /** The connected account, the owner chosen first. */
  login: string
  onCreated: () => void
}) {
  if (!props.open) return null
  return <CreateBody {...props} />
}

function CreateBody({
  open,
  onOpenChange,
  connection,
  login,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  connection: number
  login: string
  onCreated: () => void
}) {
  const router = useRouter()
  const options = usePoll(
    (signal) =>
      get<GitHubCreateOptions>(
        `/github/connections/${connection}/create-options`,
        undefined,
        signal,
      ),
    0,
    [connection],
  )
  const [owner, setOwner] = useState("")
  const [typed, setTyped] = useState("")
  const [description, setDescription] = useState("")
  const [visibility, setVisibility] = useState<"public" | "private">("private")
  const [readme, setReadme] = useState(true)
  const [gitignore, setGitignore] = useState(NONE)
  const [license, setLicense] = useState(NONE)
  const [busy, setBusy] = useState(false)
  const [refused, setRefused] = useState<{ field?: "name"; message: string }>()
  const [unreachable, setUnreachable] = useState<GitHubCreateResult>()

  const owners = useMemo(() => options.data?.owners ?? [], [options.data])
  const chosen =
    owners.find((o) => o.login === owner) ??
    owners.find((o) => o.canCreate && o.login.toLowerCase() === login.toLowerCase()) ??
    owners.find((o) => o.canCreate)
  const { name, problem } = githubRepoName(typed)
  const renamed = typed.trim() !== "" && name !== typed.trim()

  const create = async () => {
    if (!chosen) return
    setBusy(true)
    setRefused(undefined)
    const body: GitHubCreateRepository = {
      owner: chosen.login,
      name,
      description: description.trim() || undefined,
      visibility,
      autoInit: readme,
      gitignoreTemplate: gitignore === NONE ? undefined : gitignore,
      licenseTemplate: license === NONE ? undefined : license,
    }
    try {
      const result = await post<GitHubCreateResult>(
        `/github/connections/${connection}/repositories`,
        body,
      )
      onCreated()
      if (!result.reachable) {
        setUnreachable(result)
        notify.success(`Created ${result.repository.fullName}`)
        return
      }
      notify.success(`Created ${result.repository.fullName}`)
      onOpenChange(false)
      router.push(githubRepoHref(result.repository.fullName, { account: connection }))
    } catch (error) {
      const message = errorMessage(error)
      const onName =
        error instanceof ApiError &&
        error.code === "github_validation" &&
        (error.field === "name" || /\bname\b/i.test(message))
      setRefused({ field: onName ? "name" : undefined, message })
    } finally {
      setBusy(false)
    }
  }

  const ready = Boolean(chosen?.canCreate) && !problem && !busy && !unreachable

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !busy && onOpenChange(next)}
      title="New repository"
      description="Create a repository on GitHub."
      size="md"
      progress={busy && { label: "Creating on GitHub…" }}
      footer={
        unreachable ? (
          <Button onClick={() => onOpenChange(false)}>Close</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
              Cancel
            </Button>
            <Button onClick={() => void create()} disabled={!ready} pending={busy}>
              <Plus className="size-4" />
              Create
            </Button>
          </>
        )
      }
    >
      {options.error && !options.data ? (
        <ErrorState error={options.error} what="who can own it" onRetry={options.refresh} />
      ) : !options.data ? (
        <LoadingRows rows={5} />
      ) : unreachable ? (
        <Notice
          title={`${unreachable.repository.fullName} is on GitHub`}
          tone="warning"
          live="status"
        >
          {unreachable.notice ??
            "The App is installed on selected repositories of this owner, so add the new one to the installation on GitHub to see it here."}
        </Notice>
      ) : (
        <div className="space-y-4">
          {refused && !refused.field && (
            <Notice title="GitHub refused it" tone="danger" live="alert">
              {refused.message}
            </Notice>
          )}
          <FieldRow>
            <Field label="Owner" hint={chosen?.note}>
              <Select value={chosen?.login ?? ""} onValueChange={setOwner}>
                <SelectTrigger className="w-full font-mono" aria-label="Owner">
                  <SelectValue placeholder="No owner can take one" />
                </SelectTrigger>
                <SelectContent>
                  {owners.map((o) => (
                    <SelectItem
                      key={o.login}
                      value={o.login}
                      disabled={!o.canCreate}
                      className="font-mono"
                    >
                      {o.login}
                      {!o.canCreate && o.note ? ` — ${o.note}` : ""}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field
              label="Name"
              htmlFor="github-new-name"
              error={refused?.field === "name" ? refused.message : typed ? problem : undefined}
              hint={
                renamed && !problem ? (
                  <>
                    Created as <span className="font-mono">{name}</span>
                  </>
                ) : undefined
              }
            >
              <Input
                id="github-new-name"
                autoComplete="off"
                spellCheck={false}
                value={typed}
                maxLength={120}
                onChange={(event) => {
                  setTyped(event.target.value)
                  if (refused?.field === "name") setRefused(undefined)
                }}
                placeholder="my-project"
                className="font-mono"
              />
            </Field>
          </FieldRow>
          <Field label="Description" htmlFor="github-new-description">
            <Input
              id="github-new-description"
              autoComplete="off"
              value={description}
              maxLength={350}
              onChange={(event) => setDescription(event.target.value)}
            />
          </Field>
          <Field
            label={
              <span className="inline-flex items-center gap-1.5">
                Visibility
                <Explain label="What visibility means">
                  A public repository can be read by anyone on the internet. A private one only by
                  the people and teams given access. It can be changed later on the
                  repository&apos;s Settings.
                </Explain>
              </span>
            }
          >
            <Segments
              label="Visibility"
              value={visibility}
              onChange={setVisibility}
              options={[
                { value: "private", label: "Private" },
                { value: "public", label: "Public" },
              ]}
            />
          </Field>
          <OptionList>
            <OptionRow title="Add a README" checked={readme} onCheckedChange={setReadme} />
          </OptionList>
          <FieldRow>
            <Field label=".gitignore">
              <Select value={gitignore} onValueChange={setGitignore}>
                <SelectTrigger className="w-full" aria-label=".gitignore template">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>None</SelectItem>
                  {options.data.gitignoreTemplates.map((template) => (
                    <SelectItem key={template} value={template}>
                      {template}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="Licence">
              <Select value={license} onValueChange={setLicense}>
                <SelectTrigger className="w-full" aria-label="Licence">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE}>None</SelectItem>
                  {options.data.licenses.map((item) => (
                    <SelectItem key={item.key} value={item.key}>
                      {item.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
          </FieldRow>
        </div>
      )}
    </Modal>
  )
}
