"use client"

import { useMemo, useState } from "react"
import { useSessionState } from "@/lib/view-state"
import { post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { PM2Daemon, PM2StartRequest } from "@/lib/types"
import { useMetrics } from "@/hooks/use-metrics"
import {
  ChoiceCard,
  ChoiceCardHint,
  ChoiceCardTitle,
  ChoiceGrid,
  ProductCard,
} from "@/components/choice-card"
import { ShellWords } from "@/components/deploy/run-evidence"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { ChartActivity, Copy, Terminal } from "@/components/icons"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type Mode = "fork" | "cluster" | "max"

/**
 * What PM2 may run the file with, each as the product it is. "Decide from the
 * file" is PM2's own default, and "None" runs the file as a program.
 */
const INTERPRETERS: { value: string; label: string; detail: string; product?: string }[] = [
  { value: "auto", label: "From the file", detail: "node for .js, else its shebang" },
  { value: "node", label: "Node.js", detail: "node", product: "nodejs" },
  { value: "python3", label: "Python", detail: "python3", product: "python" },
  { value: "bash", label: "Bash", detail: "bash", product: "shellscript" },
  { value: "php", label: "PHP", detail: "php", product: "php" },
  { value: "none", label: "None", detail: "run the file itself" },
]

const ECOSYSTEM = /\.(config\.(c|m)?js|json|ya?ml)$/i

/** What a script path will most likely run as, before PM2 is asked: the mark beside the field. */
function scriptProduct(path: string, interpreter: string): string | undefined {
  const chosen = INTERPRETERS.find((i) => i.value === interpreter)
  if (chosen?.product) return chosen.product
  if (ECOSYSTEM.test(path)) return "pm2"
  if (/\.(c|m)?[jt]sx?$/i.test(path)) return "nodejs"
  if (/\.py$/i.test(path)) return "python"
  if (/\.sh$/i.test(path)) return "shellscript"
  if (/\.php$/i.test(path)) return "php"
  return undefined
}

/**
 * Registering a program with PM2 from here rather than from a shell.
 *
 * Every field is one argument of `pm2 start`, and the command under the form
 * is the one that will run, coloured as a command is — so the dialog can be
 * learned from, and so nothing is hidden about what it does. What runs it and
 * how many copies are choices of a kind, so they are cards with their marks
 * (§16) rather than two selects. Pointing it at an ecosystem file hands the
 * file to PM2 whole, because that file already says everything the other
 * fields would.
 */
export function PM2StartDialog({
  open,
  daemons,
  onOpenChange,
  onStarted,
}: {
  open: boolean
  daemons: PM2Daemon[]
  onOpenChange: (open: boolean) => void
  onStarted: () => void
}) {
  const { snapshot } = useMetrics()
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  // Kept for the tab while the dialog is open; the page forgets it on close.
  const [account, setAccount] = useSessionState(
    "processes.pm2.start.account",
    daemons[0]?.account ?? "",
  )
  const [script, setScript] = useSessionState("processes.pm2.start.script", "")
  const [name, setName] = useSessionState("processes.pm2.start.name", "")
  const [cwd, setCwd] = useSessionState("processes.pm2.start.cwd", "")
  const [interpreter, setInterpreter] = useSessionState("processes.pm2.start.interpreter", "auto")
  const [mode, setMode] = useSessionState<Mode>("processes.pm2.start.mode", "fork")
  const [instances, setInstances] = useSessionState("processes.pm2.start.instances", "2")
  const [watch, setWatch] = useSessionState("processes.pm2.start.watch", false)
  const [memory, setMemory] = useSessionState("processes.pm2.start.memory", "")
  const [args, setArgs] = useSessionState("processes.pm2.start.args", "")
  const [busy, setBusy] = useState(false)

  const chosenAccount = account || daemons[0]?.account || ""
  const ecosystem = ECOSYSTEM.test(script.trim())
  const product = scriptProduct(script.trim(), ecosystem ? "auto" : interpreter)
  const scriptError =
    script.trim() && !script.trim().startsWith("/") ? "An absolute path on the server." : undefined
  const nameError =
    name && !/^[A-Za-z0-9._@:][A-Za-z0-9._@:-]{0,127}$/.test(name)
      ? "Letters, digits, dots, dashes and underscores."
      : undefined
  const cwdError = cwd && !cwd.startsWith("/") ? "An absolute path on the server." : undefined
  const memoryError =
    memory && !/^[0-9]{1,6}[KMG]?$/.test(memory) ? "A size such as 300M or 1G." : undefined
  const count = Number(instances)
  const instancesError =
    mode === "cluster" && (!Number.isInteger(count) || count < 2 || count > 128)
      ? "A whole number from 2 to 128."
      : undefined

  const request = useMemo<PM2StartRequest>(() => {
    const req: PM2StartRequest = { account: chosenAccount, script: script.trim() }
    if (name) req.name = name
    if (ecosystem) return req
    if (cwd) req.cwd = cwd
    if (interpreter !== "auto") req.interpreter = interpreter
    if (mode === "max") req.instances = -1
    else if (mode === "cluster") req.instances = count
    if (watch) req.watch = true
    if (memory) req.maxMemoryRestart = memory
    const argv = args.trim() ? args.trim().split(/\s+/) : []
    if (argv.length) req.args = argv
    return req
  }, [chosenAccount, script, name, ecosystem, cwd, interpreter, mode, count, watch, memory, args])

  const command = useMemo(() => {
    if (!request.script) return ""
    const parts = ["pm2", "start", request.script]
    if (ecosystem) {
      if (request.name) parts.push("--only", request.name)
      return parts.join(" ")
    }
    if (request.name) parts.push("--name", request.name)
    if (request.cwd) parts.push("--cwd", request.cwd)
    if (request.interpreter) parts.push("--interpreter", request.interpreter)
    if (request.instances === -1) parts.push("-i", "max")
    else if (request.instances) parts.push("-i", String(request.instances))
    if (request.watch) parts.push("--watch")
    if (request.maxMemoryRestart) parts.push("--max-memory-restart", request.maxMemoryRestart)
    if (request.args?.length) parts.push("--", ...request.args)
    return parts.join(" ")
  }, [request, ecosystem])

  const valid =
    Boolean(chosenAccount && request.script) &&
    !scriptError &&
    !nameError &&
    !cwdError &&
    !memoryError &&
    !instancesError

  const submit = async () => {
    setBusy(true)
    try {
      await post("/pm2/start", request)
      notify.success(`${request.name || request.script} started under PM2`)
      onStarted()
      onOpenChange(false)
      setScript("")
      setName("")
      setArgs("")
    } catch (err) {
      notify.error("Could not start the application", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <ProductLogo id="pm2" size="sm" />
          <span className="truncate">Start an application</span>
        </span>
      }
      description="Register a script or an ecosystem file with PM2 and run it"
      size="lg"
      footer={
        <>
          <FormNote className="mr-auto">Not in the startup list until it is saved.</FormNote>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={!valid || busy} onClick={() => void submit()}>
            {busy ? "Starting…" : "Start"}
          </Button>
        </>
      }
    >
      <div className="space-y-6">
        <FormSection title="What to run">
          <Field
            label="Script or ecosystem file"
            htmlFor="pm2-script"
            hint="A JavaScript, shell or Python file, or an ecosystem.config.js that describes several applications."
            error={scriptError}
          >
            <div className="flex min-w-0 items-center gap-2">
              {/* What the path will run as, redrawn as it is typed. */}
              <ProductLogo
                key={product ?? "none"}
                id={product}
                size="sm"
                fallback={Terminal}
                className="animate-rise"
              />
              <Input
                id="pm2-script"
                value={script}
                onChange={(e) => setScript(e.target.value)}
                placeholder="/srv/api/dist/server.js"
                className="font-mono"
                spellCheck={false}
              />
            </div>
          </Field>
          <FieldRow>
            {daemons.length > 1 && (
              <Field
                label="Account"
                htmlFor="pm2-account"
                hint="Whose PM2 runs it. The script runs as this user."
              >
                <Select value={chosenAccount} onValueChange={setAccount}>
                  <SelectTrigger id="pm2-account" size="sm" className="w-full">
                    <SelectValue placeholder="Account" />
                  </SelectTrigger>
                  <SelectContent>
                    {daemons.map((d) => (
                      <SelectItem key={d.account} value={d.account} hint={d.home}>
                        {d.account}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            <Field
              label="Name"
              htmlFor="pm2-name"
              hint={
                ecosystem
                  ? "Only this application from the file; blank starts them all."
                  : `How PM2 lists it. Blank uses the file name.${daemons.length === 1 ? ` Runs as ${chosenAccount}.` : ""}`
              }
              error={nameError}
            >
              <Input
                id="pm2-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="api"
              />
            </Field>
            {!ecosystem && (
              <Field
                label="Working directory"
                htmlFor="pm2-cwd"
                hint="Blank runs it where the file is."
                error={cwdError}
              >
                <Input
                  id="pm2-cwd"
                  value={cwd}
                  onChange={(e) => setCwd(e.target.value)}
                  placeholder="/srv/api"
                  className="font-mono"
                  spellCheck={false}
                />
              </Field>
            )}
          </FieldRow>
        </FormSection>

        {ecosystem ? (
          <Notice title="An ecosystem file">
            It says how each of its applications runs — the interpreter, the workers, the limits —
            so PM2 is handed the file and nothing else.
          </Notice>
        ) : (
          <>
            <FormSection title="Run with">
              <ChoiceGrid columns={3} role="group" aria-label="Interpreter">
                {INTERPRETERS.map((option) => (
                  <ProductCard
                    key={option.value}
                    product={option.product}
                    fallback={option.value === "none" ? Terminal : ChartActivity}
                    label={option.label}
                    detail={option.detail}
                    mono={option.value !== "auto" && option.value !== "none"}
                    selected={interpreter === option.value}
                    onClick={() => setInterpreter(option.value)}
                  />
                ))}
              </ChoiceGrid>
            </FormSection>

            <FormSection title="How many">
              <ChoiceGrid columns={3} role="group" aria-label="Mode">
                <ChoiceCard selected={mode === "fork"} onClick={() => setMode("fork")}>
                  <ChoiceCardTitle>One process</ChoiceCardTitle>
                  <ChoiceCardHint>Fork mode. Any interpreter.</ChoiceCardHint>
                </ChoiceCard>
                <ChoiceCard selected={mode === "cluster"} onClick={() => setMode("cluster")}>
                  <ChoiceCardTitle>A cluster</ChoiceCardTitle>
                  <ChoiceCardHint>Workers sharing one port. Node only.</ChoiceCardHint>
                </ChoiceCard>
                <ChoiceCard selected={mode === "max"} onClick={() => setMode("max")}>
                  <ChoiceCardTitle>One per core</ChoiceCardTitle>
                  <ChoiceCardHint>
                    {coreCount > 0
                      ? `${plural(coreCount, "worker")} on this host.`
                      : "As many workers as cores."}
                  </ChoiceCardHint>
                </ChoiceCard>
              </ChoiceGrid>
              <FieldRow>
                {mode === "cluster" && (
                  <Field
                    label="Instances"
                    htmlFor="pm2-instances"
                    hint="Workers in the cluster."
                    error={instancesError}
                  >
                    <Input
                      id="pm2-instances"
                      type="number"
                      min={2}
                      max={128}
                      value={instances}
                      onChange={(e) => setInstances(e.target.value)}
                      className="font-mono"
                    />
                  </Field>
                )}
                <Field
                  label="Memory limit"
                  htmlFor="pm2-memory"
                  hint="Restart when it grows past this. Blank is no limit."
                  error={memoryError}
                >
                  <Input
                    id="pm2-memory"
                    value={memory}
                    onChange={(e) => setMemory(e.target.value.toUpperCase())}
                    placeholder="300M"
                    className="font-mono"
                  />
                </Field>
              </FieldRow>
            </FormSection>

            <FormSection title="Options">
              <Field
                label="Arguments"
                htmlFor="pm2-args"
                hint="Passed to the script, split on spaces. Quoting is not interpreted."
              >
                <Input
                  id="pm2-args"
                  value={args}
                  onChange={(e) => setArgs(e.target.value)}
                  placeholder="--port 3000"
                  className="font-mono"
                  spellCheck={false}
                />
              </Field>
              <OptionList>
                <OptionRow
                  title="Restart when files change"
                  hint="PM2 watches the working directory. Meant for development; on a server it restarts on every deploy and every log write inside the tree."
                  checked={watch}
                  onCheckedChange={setWatch}
                />
              </OptionList>
            </FormSection>
          </>
        )}

        <div className="min-w-0 space-y-1.5">
          <div className="flex min-h-6 items-center justify-between gap-3">
            <p className="eyebrow">Command</p>
            {command && (
              <Button
                size="xs"
                variant="ghost"
                onClick={() => void copyText(command, "Command copied")}
              >
                <Copy />
                Copy
              </Button>
            )}
          </div>
          <Well className="max-h-44 text-hint leading-relaxed break-all whitespace-pre-wrap">
            {command ? (
              <ShellWords command={command} />
            ) : (
              <span className="text-muted-foreground italic">
                Name a script and the command PM2 will run appears here.
              </span>
            )}
          </Well>
        </div>
      </div>
    </Modal>
  )
}
