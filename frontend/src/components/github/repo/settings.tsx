"use client"

import { useState } from "react"
import { Field, FieldRow } from "@/components/form"
import { DangerRow, DangerRows } from "@/components/settings/danger-row"
import { SaveBarProvider } from "@/components/settings/save-bar"
import { SettingForm, SettingSection, settingStatus } from "@/components/settings/setting-card"
import { SettingsColumn, SwitchRow, SwitchRows } from "@/components/settings/settings-column"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { useRepoActs } from "@/components/github/repo/acts"
import {
  changeCount,
  draftOf,
  nameProblem,
  parseTopics,
  settingsChanges,
  topicProblem,
  type SettingsDraft,
} from "@/components/github/repo/model"
import { useActReason, useRepo } from "@/components/github/repo/shared"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, get, patch } from "@/lib/api"
import { notify } from "@/lib/toast"
import type {
  GitHubBranchPage,
  GitHubRepositoryDetail,
  GitHubRepositorySettings,
} from "@/lib/github-types"

type Toggle = { key: keyof GitHubRepositorySettings; title: string; info: string }

const FEATURES: Toggle[] = [
  {
    key: "hasIssues",
    title: "Issues",
    info: "Turned off, the Issues tab disappears for everyone.",
  },
  { key: "hasWiki", title: "Wiki", info: "Pages anyone with access can read." },
  { key: "hasProjects", title: "Projects", info: "Boards for planning work in this repository." },
  { key: "hasDiscussions", title: "Discussions", info: "A forum for questions and ideas." },
]

const MERGING: Toggle[] = [
  {
    key: "allowMergeCommit",
    title: "Merge commits",
    info: "Keeps every commit and adds one that merges them.",
  },
  {
    key: "allowSquashMerge",
    title: "Squash merging",
    info: "Folds a pull request into one commit.",
  },
  {
    key: "allowRebaseMerge",
    title: "Rebase merging",
    info: "Replays each commit on the base branch.",
  },
  {
    key: "allowAutoMerge",
    title: "Auto-merge",
    info: "A pull request merges by itself once its requirements are met.",
  },
  {
    key: "deleteBranchOnMerge",
    title: "Delete head branches",
    info: "A merged pull request's branch is deleted.",
  },
  {
    key: "allowUpdateBranch",
    title: "Suggest updating branches",
    info: "Offers to bring a pull request's branch up to date with its base.",
  },
]

/**
 * The repository's settings as a form you read (D8): one centred column,
 * one floating save bar that sends only what changed. Only the settings
 * GitHub stated are drawn; it states the merging ones only to people who
 * may change them. Without the right to change them, every value is still
 * read, in its control's place, with the reason at the form's foot.
 *
 * A rename moves the page to the repository's new address. The danger
 * zone is last: archiving, visibility and deletion, each asking first.
 */
export function RepoSettings({ onRenamed }: { onRenamed: (name: string) => void }) {
  const { repo } = useRepo()
  return (
    <SaveBarProvider>
      <SettingsColumn>
        {/* Keyed on what GitHub said, so a save that lands starts a fresh draft. */}
        <SettingsForm key={JSON.stringify(draftOf(repo))} onRenamed={onRenamed} />
        <DangerZone />
      </SettingsColumn>
    </SaveBarProvider>
  )
}

function SettingsForm({ onRenamed }: { onRenamed: (name: string) => void }) {
  const { repo, path, refresh } = useRepo()
  const administer = useActReason("administer")
  const held =
    administer ??
    (repo.archived
      ? `${repo.fullName} is archived, so its settings are read-only until it is unarchived.`
      : undefined)
  const canEdit = !held
  const [draft, setDraft] = useState<SettingsDraft>(() => draftOf(repo))
  const [topicsText, setTopicsText] = useState((repo.topics ?? []).join(", "))
  const [saving, setSaving] = useState(false)
  const [refused, setRefused] = useState<{ message: string; field?: string }>()
  const branches = usePoll(
    (signal) => get<GitHubBranchPage>(path("/branches"), { page: 1 }, signal),
    0,
    [repo.fullName],
    { enabled: canEdit },
  )
  const set = (patchDraft: Partial<SettingsDraft>) => setDraft((d) => ({ ...d, ...patchDraft }))
  const changes = settingsChanges(repo, draft)
  const count = changeCount(changes)
  const dirty = count > 0
  const errors = {
    name: draft.name !== repo.name ? nameProblem(draft.name) : undefined,
    topics: draft.topics.map(topicProblem).find(Boolean),
  }
  const branchNames = [
    repo.defaultBranch,
    ...(branches.data?.branches ?? [])
      .map((b) => b.name)
      .filter((name) => name !== repo.defaultBranch),
  ]
  const stated = (list: Toggle[]) =>
    list.filter((toggle) => typeof repo.settings[toggle.key] === "boolean")
  const merging = stated(MERGING)
  const signoff = typeof repo.settings.webCommitSignoffRequired === "boolean"

  const discard = () => {
    setDraft(draftOf(repo))
    setTopicsText((repo.topics ?? []).join(", "))
    setRefused(undefined)
  }
  const save = async () => {
    setSaving(true)
    setRefused(undefined)
    try {
      const saved = await patch<GitHubRepositoryDetail>(path(), changes)
      notify.success("Saved")
      if (changes.name && saved.name !== repo.name) onRenamed(saved.name)
      else refresh()
      return true
    } catch (err) {
      setRefused({
        message: err instanceof Error ? err.message : String(err),
        field: err instanceof ApiError ? err.field : undefined,
      })
      return false
    } finally {
      setSaving(false)
    }
  }
  const sectionStatus = (keys: string[]) =>
    settingStatus({
      dirty: keys.some((key) => key in changes),
      refused: Boolean(refused && (!refused.field || keys.includes(refused.field))),
    })

  return (
    <SettingForm
      name="Repository settings"
      canEdit={canEdit}
      dirty={dirty}
      changes={count}
      saving={saving}
      invalid={Boolean(errors.name || errors.topics)}
      onSave={save}
      onDiscard={discard}
      applies="immediately"
      error={refused?.message}
      note={held}
    >
      <SettingSection
        title="General"
        state={<span className="font-mono">{repo.fullName}</span>}
        status={sectionStatus([
          "name",
          "description",
          "homepage",
          "topics",
          "defaultBranch",
          "isTemplate",
        ])}
      >
        <FieldRow>
          <Field
            label="Name"
            htmlFor="repo-name"
            error={errors.name}
            info="GitHub sends the old address on to the new one."
          >
            <Input
              id="repo-name"
              className="font-mono"
              autoComplete="off"
              spellCheck={false}
              value={draft.name}
              disabled={!canEdit}
              onChange={(e) => set({ name: e.target.value })}
            />
          </Field>
          <Field label="Default branch" htmlFor="repo-default-branch">
            <Select
              value={draft.defaultBranch}
              onValueChange={(defaultBranch) => set({ defaultBranch })}
              disabled={!canEdit}
            >
              <SelectTrigger id="repo-default-branch" className="w-full font-mono">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {branchNames.map((name) => (
                  <SelectItem key={name} value={name} className="font-mono">
                    {name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        </FieldRow>
        <Field label="Description" htmlFor="repo-description">
          <Input
            id="repo-description"
            value={draft.description}
            disabled={!canEdit}
            onChange={(e) => set({ description: e.target.value })}
          />
        </Field>
        <FieldRow>
          <Field label="Homepage" htmlFor="repo-homepage">
            <Input
              id="repo-homepage"
              className="font-mono"
              inputMode="url"
              value={draft.homepage}
              disabled={!canEdit}
              onChange={(e) => set({ homepage: e.target.value })}
            />
          </Field>
          <Field
            label="Topics"
            htmlFor="repo-topics"
            error={errors.topics}
            info="Separated by commas or spaces."
          >
            <Input
              id="repo-topics"
              value={topicsText}
              disabled={!canEdit}
              onChange={(e) => {
                setTopicsText(e.target.value)
                set({ topics: parseTopics(e.target.value) })
              }}
            />
          </Field>
        </FieldRow>
        <SwitchRows>
          <SwitchRow
            title="Template repository"
            info="People with access can make new repositories from it."
            checked={draft.isTemplate}
            disabled={!canEdit}
            onCheckedChange={(isTemplate) => set({ isTemplate })}
          />
        </SwitchRows>
      </SettingSection>

      <SettingSection title="Features" status={sectionStatus(FEATURES.map((t) => t.key))}>
        <Toggles toggles={stated(FEATURES)} draft={draft} set={set} disabled={!canEdit} />
      </SettingSection>

      {merging.length > 0 && (
        <SettingSection title="Pull requests" status={sectionStatus(merging.map((t) => t.key))}>
          <Toggles toggles={merging} draft={draft} set={set} disabled={!canEdit} />
        </SettingSection>
      )}

      {signoff && (
        <SettingSection title="Commits" status={sectionStatus(["webCommitSignoffRequired"])}>
          <SwitchRows>
            <SwitchRow
              title="Require sign-off on web commits"
              info="Commits made on GitHub's website must be signed off by their author."
              checked={Boolean(draft.webCommitSignoffRequired)}
              disabled={!canEdit}
              onCheckedChange={(webCommitSignoffRequired) => set({ webCommitSignoffRequired })}
            />
          </SwitchRows>
        </SettingSection>
      )}
    </SettingForm>
  )
}

function Toggles({
  toggles,
  draft,
  set,
  disabled,
}: {
  toggles: Toggle[]
  draft: SettingsDraft
  set: (patch: Partial<SettingsDraft>) => void
  disabled: boolean
}) {
  return (
    <SwitchRows>
      {toggles.map((toggle) => (
        <SwitchRow
          key={toggle.key}
          title={toggle.title}
          info={toggle.info}
          checked={Boolean(draft[toggle.key])}
          disabled={disabled}
          onCheckedChange={(on) => set({ [toggle.key]: on })}
        />
      ))}
    </SwitchRows>
  )
}

function DangerZone() {
  const { repo } = useRepo()
  const acts = useRepoActs()
  const administer = useActReason("administer")
  const visibility = useActReason("administer", { destructive: true })
  const remove = useActReason("delete", { destructive: true })
  return (
    <SettingSection title="Danger zone" tone="danger">
      <DangerRows>
        <DangerRow
          title={repo.archived ? "Unarchive" : "Archive"}
          sentence={
            administer ??
            (repo.archived
              ? "Takes pushes, issues and pull requests again."
              : "Makes it read-only for everyone until it is unarchived. Nothing is deleted.")
          }
          action={
            !administer && (
              <Button variant="outline" size="sm" onClick={acts.archive}>
                {repo.archived ? "Unarchive" : "Archive"}
              </Button>
            )
          }
        />
        <DangerRow
          title={acts.goingPublic ? "Make public" : "Make private"}
          sentence={
            visibility ??
            (acts.goingPublic
              ? "Everyone will see its code and history."
              : "Others lose their stars and watches, and public forks may be detached.")
          }
          facts={<>Now {repo.visibility}</>}
          action={
            !visibility && (
              <Button variant="outline" size="sm" onClick={acts.visibility}>
                {acts.goingPublic ? "Make public" : "Make private"}
              </Button>
            )
          }
        />
        <DangerRow
          title="Delete"
          sentence={
            remove ??
            "Deletes it on GitHub with its issues, pull requests and wiki. This cannot be undone from here."
          }
          action={
            !remove && (
              <Button variant="destructive" size="sm" onClick={acts.remove}>
                Delete
              </Button>
            )
          }
        />
      </DangerRows>
    </SettingSection>
  )
}
