"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import {
  ArrowLeft,
  Copy,
  Key,
  LockClosed,
  SettingsGear,
  Shield,
  UserSettings,
  type Icon,
} from "@/components/icons"
import { ChoiceCard, ChoiceGrid } from "@/components/choice-card"
import { SecretField } from "@/components/database/ops/access-parts"
import { generatePassword } from "@/components/database/connect/rules"
import { Segments } from "@/components/deploy/settings/segments"
import { FlowActions, FlowHeader, FlowPanel, FlowPanelBody } from "@/components/flow"
import { Field, FieldRow, FormNote, OptionRow } from "@/components/form"
import { Page } from "@/components/page"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { accountHref, groupGrant, isPrivateGroup } from "@/components/system-users/access"
import { GroupWord, ShellGlyph } from "@/components/system-users/marks"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { usePoll } from "@/hooks/use-poll"
import { get, post, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import type {
  AccountDefaults,
  AccountTemplate,
  SudoLevel,
  SystemGroup,
  SystemUser,
} from "@/lib/types"

type Way = "key" | "github" | "password" | "none"

const MARKS: Record<string, { icon?: Icon; product?: string; hint: string }> = {
  person: { icon: UserSettings, hint: "Signs in and works here. No root." },
  administrator: { icon: Shield, hint: "Signs in and can become root with its password." },
  developer: { product: "docker", hint: "Signs in and runs containers." },
  deploy: { product: "github", hint: "A key for CI to sign in with. No root." },
  service: { icon: SettingsGear, hint: "Runs a program. Nobody signs in as it." },
}

const USERNAME = /^[a-z_][a-z0-9_-]{0,31}$/

/**
 * Making an account, as a flow (§17): what it is for, then who it is and how they
 * get in, ending in the one command that creates it. The old dialog asked for a
 * name and offered a key as optional, and that made `zoro`: an administrator
 * nobody could ever sign in as. So here the way in is the question, and an account
 * nobody can sign in to has to be asked for by name.
 */
export function NewAccount() {
  const router = useRouter()
  const [templateId, setTemplateId] = useState("person")
  const [username, setUsername] = useState("")
  const [comment, setComment] = useState("")
  const [way, setWay] = useState<Way>("key")
  const [key, setKey] = useState("")
  const [github, setGithub] = useState("")
  const [password, setPassword] = useState(() => generatePassword(20))
  const [mustChange, setMustChange] = useState(true)
  const [sudo, setSudo] = useState<SudoLevel>("none")
  const [groups, setGroups] = useState<string[]>([])
  const [shell, setShell] = useState("")
  const [busy, setBusy] = useState(false)
  const [created, setCreated] = useState<{ username: string; password?: string } | null>(null)

  const { data: templates, refresh: refreshTemplates } = usePoll(
    (signal) => get<AccountTemplate[]>("/system-accounts/templates", undefined, signal),
    0,
    [],
  )
  const { data: defaults } = usePoll(
    (signal) => get<AccountDefaults>("/system-accounts/defaults", undefined, signal),
    0,
    [],
  )
  const { data: hostGroups } = usePoll(
    (signal) => get<SystemGroup[]>("/system-accounts/groups", undefined, signal),
    0,
    [],
  )
  const template = useMemo(
    () => templates?.find((t) => t.id === templateId),
    [templates, templateId],
  )
  const isService = Boolean(template?.system)

  const pick = (next: AccountTemplate) => {
    setTemplateId(next.id)
    setSudo(next.sudo)
    setShell(next.shell)
    // A template names groups a host may not have; only real ones can be asked for.
    const known = new Set((hostGroups ?? []).map((g) => g.name))
    setGroups(next.groups.filter((g) => known.has(g)))
    // An administrator's sudo asks for a password, so that is the way in it starts
    // with: a key alone would make the account that this flow exists to prevent.
    setWay(next.system ? "none" : next.sudo === "password" ? "password" : "key")
  }

  const nameError =
    username && !USERNAME.test(username)
      ? "Lower-case letters, digits, - and _, starting with a letter."
      : undefined

  const wayReady =
    way === "key"
      ? key.trim() !== ""
      : way === "github"
        ? github.trim() !== ""
        : way === "password"
          ? password.length >= 8
          : true
  const ready = USERNAME.test(username) && wayReady
  // What is sent if the operator leaves the picker alone, and so what it must show.
  const chosenShell = shell || template?.shell || defaults?.shell || ""
  const shells = Array.from(new Set([...(defaults?.shells ?? []), chosenShell].filter(Boolean)))

  const create = async () => {
    setBusy(true)
    try {
      const account = await post<SystemUser & { inheritedHome?: string }>("/system-users/", {
        username,
        comment,
        shell: chosenShell,
        groups,
        system: template?.system ?? false,
        noHome: template?.noHome ?? false,
        sudo: { level: sudo, commands: [] },
        ...(way === "key" ? { sshKey: key.trim() } : {}),
        ...(way === "github" ? { githubUser: github.trim() } : {}),
        ...(way === "password" ? { password, mustChangePassword: mustChange } : {}),
        ...(way === "none" ? { noSignIn: true } : {}),
      })
      // A home kept from an earlier delete came with the name, and any keys in it
      // sign in as this account now. The operator should hear that from here.
      if (account.inheritedHome) {
        notify.warning(`${account.inheritedHome} was already there, and is now this account's`, {
          description: "Check its SSH keys and files: they came with the name.",
        })
      }
      if (way === "password") {
        setCreated({ username: account.username, password })
        return
      }
      notify.success(`Created ${account.username}`)
      router.push(accountHref(account.username))
    } catch (err) {
      notify.error("The account was not created", err)
    } finally {
      setBusy(false)
    }
  }

  const saveTemplate = async () => {
    const name = comment.trim() || username.trim()
    if (!name || !templates) return
    const own = templates.filter((t) => !t.builtin)
    const id = name
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-|-$/g, "")
    try {
      await put("/system-accounts/templates", [
        ...own.filter((t) => t.id !== id),
        {
          id,
          name,
          builtin: false,
          shell: chosenShell || "/bin/bash",
          groups,
          sudo,
          system: template?.system ?? false,
          noHome: template?.noHome ?? false,
        },
      ])
      notify.success(`Saved "${name}" as a template`)
      refreshTemplates()
    } catch (err) {
      notify.error("The template was not saved", err)
    }
  }

  if (created) {
    return (
      <Page register="flow">
        <FlowHeader question={`${created.username} is ready`} />
        <FlowPanel className="max-w-2xl">
          <FlowPanelBody>
            <div className="grid gap-4" data-slot="created-password">
              <Notice tone="success" title="Copy the password now">
                The server keeps only a hash, so this is the only time it can be read.
              </Notice>
              <div className="flex min-w-0 items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-md bg-surface-sunken px-3 py-2 font-mono text-xs">
                  {created.password}
                </code>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void copyText(created.password ?? "", "Password copied")}
                >
                  <Copy />
                  Copy
                </Button>
              </div>
            </div>
          </FlowPanelBody>
          <FlowActions>
            <Button onClick={() => router.push(accountHref(created.username))}>Open account</Button>
          </FlowActions>
        </FlowPanel>
      </Page>
    )
  }

  return (
    <Page register="flow">
      <FlowHeader
        eyebrow={
          <Link
            href="/system-users"
            className="inline-flex items-center gap-1 rounded-sm normal-case focus-ring hover:underline"
          >
            <ArrowLeft className="size-3" /> System users
          </Link>
        }
        question="Who is this account for?"
      />
      <ChoiceGrid columns="compact">
        {(templates ?? []).map((option, index) => {
          const mark = MARKS[option.id]
          return (
            <ChoiceCard
              key={option.id}
              index={index}
              verb={`Make a ${option.name} account`}
              selected={option.id === templateId}
              onClick={() => pick(option)}
              logo={
                mark?.product ? (
                  <ProductLogo id={mark.product} />
                ) : (
                  <ProductLogo fallback={mark?.icon ?? Key} />
                )
              }
              title={option.name}
              description={mark?.hint ?? `${option.groups.join(", ") || "No extra groups"}`}
            />
          )
        })}
      </ChoiceGrid>

      <FlowPanel>
        <FlowPanelBody>
          <div className="grid gap-5">
            <FieldRow>
              <Field label="Username" htmlFor="new-username" error={nameError}>
                <Input
                  id="new-username"
                  className="font-mono"
                  autoComplete="off"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </Field>
              <Field label="Full name" htmlFor="new-comment" hint="Optional.">
                <Input
                  id="new-comment"
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                />
              </Field>
            </FieldRow>

            {!isService && (
              <Field label="How do they sign in?">
                <div className="grid gap-3">
                  <Segments
                    label="How they sign in"
                    value={way}
                    onChange={setWay}
                    fill
                    options={[
                      {
                        value: "key",
                        label: (
                          <span className="inline-flex items-center gap-1.5">
                            <Key className="size-3.5" />
                            SSH key
                          </span>
                        ),
                      },
                      {
                        value: "github",
                        label: (
                          <span className="inline-flex items-center gap-1.5">
                            <ProductGlyph id="github" />
                            GitHub keys
                          </span>
                        ),
                      },
                      {
                        value: "password",
                        label: (
                          <span className="inline-flex items-center gap-1.5">
                            <LockClosed className="size-3.5" />
                            Password
                          </span>
                        ),
                      },
                      { value: "none", label: "Nobody directly" },
                    ]}
                  />
                  {way === "key" && (
                    <Textarea
                      aria-label="SSH public key"
                      value={key}
                      onChange={(e) => setKey(e.target.value)}
                      className="font-mono text-xs"
                      rows={3}
                      placeholder="ssh-ed25519 AAAA…"
                    />
                  )}
                  {way === "github" && (
                    <Input
                      aria-label="GitHub username"
                      value={github}
                      onChange={(e) => setGithub(e.target.value)}
                      autoComplete="off"
                      placeholder="GitHub username"
                    />
                  )}
                  {way === "password" && (
                    <>
                      <SecretField
                        id="new-password"
                        label="Password"
                        value={password}
                        onChange={setPassword}
                      />
                      <OptionRow
                        title="Make them change it"
                        hint="The first sign-in has to choose a new one."
                        checked={mustChange}
                        onCheckedChange={setMustChange}
                      />
                    </>
                  )}
                  {way === "none" && (
                    <FormNote>
                      Nobody can sign in as this account until you give it a key or a password. It
                      can still be reached with sudo or from the terminal here.
                    </FormNote>
                  )}
                </div>
              </Field>
            )}

            {!isService && (
              <Field label="Root access">
                <Segments
                  label="Root access"
                  value={sudo}
                  onChange={setSudo}
                  options={[
                    { value: "none", label: "None" },
                    { value: "password", label: "Asks for password" },
                    { value: "nopassword", label: "No password" },
                  ]}
                />
                {sudo === "password" && way !== "password" && (
                  <FormNote className="pt-2">
                    sudo will ask for a password this account will not have. Choose Password above,
                    or No password here.
                  </FormNote>
                )}
              </Field>
            )}

            {!isService && (
              <Field label="Groups" hint={groups.map(groupGrant).filter(Boolean)[0]}>
                <div className="flex flex-wrap items-center gap-x-3.5 gap-y-1.5 text-body">
                  {(hostGroups ?? [])
                    .filter((g) => (g.rootEquivalent || !g.system) && !isPrivateGroup(g))
                    .map((group) => (
                      <label
                        key={group.name}
                        className="inline-flex cursor-pointer items-center gap-1.5"
                      >
                        <Checkbox
                          aria-label={group.name}
                          checked={groups.includes(group.name)}
                          onCheckedChange={(on) =>
                            setGroups((now) =>
                              on ? [...now, group.name] : now.filter((name) => name !== group.name),
                            )
                          }
                        />
                        <GroupWord name={group.name} />
                      </label>
                    ))}
                </div>
              </Field>
            )}

            {!isService && shells.length > 0 && (
              <Field label="Shell" htmlFor="new-shell">
                <Select value={chosenShell || shells[0]} onValueChange={setShell}>
                  <SelectTrigger id="new-shell" className="max-w-xs font-mono">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {shells.map((option) => (
                      <SelectItem key={option} value={option} className="font-mono">
                        <span className="inline-flex items-center gap-1.5">
                          <ShellGlyph shell={option} />
                          {option}
                        </span>
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
          </div>
        </FlowPanelBody>
        <FlowActions>
          <Button
            variant="ghost"
            onClick={() => void saveTemplate()}
            disabled={!comment.trim() && !username.trim()}
          >
            Save as template
          </Button>
          <Button onClick={() => void create()} disabled={!ready} pending={busy}>
            Create account
          </Button>
        </FlowActions>
      </FlowPanel>
    </Page>
  )
}
