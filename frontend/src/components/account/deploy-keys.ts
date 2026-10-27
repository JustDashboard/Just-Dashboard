import type { ApiToken } from "@/lib/types"

/**
 * A deploy key's reads, kept apart from the dialog that draws them so each is
 * a fact a test can pin: the command that uses the key, which deployments it
 * reaches, and the body the server is sent to make one.
 *
 * A deploy key is the credential `jd` and a CI pipeline run on. It reaches the
 * deployment routes and nothing else on the dashboard, writes variables it can
 * never read back, and leaves the secrets — reveals, saved credentials, the
 * GitHub account's voice — to a person signed in. The lists below are what the
 * dialog says of it, in the order an operator asks: what will this do for my
 * pipeline, and what can it never do if it leaks.
 */

/** Where the dashboard serves `jd`, built for this server's own Linux. */
export const CLI_DOWNLOAD = "/api/v1/cli/jd"

/** Where `install.sh` puts `jd` on the server itself. */
export const CLI_ON_SERVER = "/usr/local/bin/jd"

/** The one line that signs `jd` in with a key, with this dashboard's address in it. */
export function jdLogin(origin: string, key = "vpsd_…") {
  return `jd login ${origin} --key ${key}`
}

/** Which deployments a deploy key reaches, in the words a row has room for. */
export function deployReach(token: Pick<ApiToken, "deploymentIds">) {
  const count = token.deploymentIds?.length ?? 0
  if (count === 0) return "every deployment"
  return count === 1 ? "1 deployment" : `${count} deployments`
}

/**
 * What `POST /tokens/deploy` is sent. An empty list is every deployment, so a
 * key meant for some is refused here rather than made for all of them.
 */
export function deployKeyBody(name: string, ttl: string, deploymentIds: number[]) {
  return {
    name: name.trim(),
    ttlDays: Number(ttl),
    deploymentIds: [...new Set(deploymentIds)].sort((a, b) => a - b),
  }
}

/** Whether a deploy key's form can be sent: a name, and somewhere for it to reach. */
export function deployKeyReady(name: string, every: boolean, chosen: ReadonlySet<number>) {
  return name.trim() !== "" && (every || chosen.size > 0)
}

/** What a deploy key can do, one line each. */
export const DEPLOY_KEY_CAN = [
  "List deployments, runs and releases, and read run logs",
  "Deploy, cancel, retry and roll back",
  "Create deployments from drafts, when it reaches every deployment",
  "Change configuration, source, Git policy and triggers",
  "Set, import, generate, rotate and delete variables and shared variables",
]

/** What a deploy key can never do, whichever deployments it reaches. */
export const DEPLOY_KEY_CANNOT = [
  "Read a variable back: it sees names and digests, never a value",
  "See or change saved registry and Git credentials",
  "Merge or comment on pull requests as the dashboard’s GitHub account, or approve previews",
  "Archive or delete a deployment",
  "Reach anything outside Deployments",
]

/**
 * The one exception to "change configuration", said beside the lists: a
 * deployment a person gave host-level reach, or a saved credential, keeps
 * that decision with a person.
 */
export const DEPLOY_KEY_HOST_RULE =
  "A deployment with host-level settings — privileged, the host’s network, host folders, release tasks on the host, Docker Compose — or a variable that reads a saved credential can still be deployed and rolled back, but its configuration and source change only in the dashboard."
