/**
 * What a pasted secret says about itself, read before it is saved.
 *
 * The credentials sheet takes a token, a password or a private key into a
 * field whose contents are never read back, so the moment of pasting is the
 * only one in which a wrong paste can be caught: a GitLab token saved for
 * github.com, or the public half of a key pair where the private half belongs.
 * Both are readings of the text itself — providers prefix their tokens so that
 * secret scanners can find them, and a key's first line names its format — so
 * nothing here guesses. A secret that says nothing gets no reading.
 */

/**
 * Token prefixes, longest first where one begins another. GitHub's are
 * documented per token type (`ghs_` is an App's installation token, the one
 * the dashboard's own App mints); GitLab's `glpat-` is a personal or project
 * token, `gldt-` a deploy token, `glrt-` a runner's; Bitbucket's app passwords
 * begin `ATBB` and Atlassian's API tokens, which Bitbucket also takes, `ATATT`;
 * Docker Hub's personal and organisation tokens begin `dckr_pat_` and
 * `dckr_oat_`.
 */
const TOKENS: [prefix: string, product: string, word: string][] = [
  ["github_pat_", "github", "GitHub token"],
  ["ghp_", "github", "GitHub token"],
  ["gho_", "github", "GitHub OAuth token"],
  ["ghu_", "github", "GitHub App user token"],
  ["ghs_", "github", "GitHub App token"],
  ["ghr_", "github", "GitHub refresh token"],
  ["glpat-", "gitlab", "GitLab token"],
  ["gldt-", "gitlab", "GitLab deploy token"],
  ["glrt-", "gitlab", "GitLab runner token"],
  ["ATBB", "bitbucket", "Bitbucket app password"],
  ["ATATT", "bitbucket", "Atlassian API token"],
  ["dckr_pat_", "docker", "Docker Hub token"],
  ["dckr_oat_", "docker", "Docker Hub organisation token"],
]

/**
 * The service a token belongs to, and what it is called there, from its
 * prefix: the `product-logo` id to draw beside the field, and the words for
 * it ("GitLab deploy token").
 */
export function tokenProduct(secret: string): { product: string; word: string } | undefined {
  const token = secret.trim()
  const match = TOKENS.find(([prefix]) => token.startsWith(prefix))
  return match && { product: match[1], word: match[2] }
}

/**
 * The shapes a key's first line can name. The four private ones are what the
 * server accepts — a PEM block whose type contains PRIVATE KEY — told apart so
 * the field can say which it read; `pkcs8` is the typeless `BEGIN PRIVATE KEY`
 * block. `public` is either half of the mistake the field exists to catch: an
 * OpenSSH `authorized_keys` line or a PEM public key.
 */
export type SshKeyShape = "openssh" | "rsa" | "ec" | "dsa" | "pkcs8" | "public"

const PRIVATE: Record<string, SshKeyShape> = {
  "OPENSSH ": "openssh",
  "RSA ": "rsa",
  "EC ": "ec",
  "DSA ": "dsa",
  "": "pkcs8",
}

const PUBLIC =
  /^(?:(?:ssh-(?:ed25519|rsa|dss)|ecdsa-sha2-nistp\d+|sk-(?:ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) |-----BEGIN (?:RSA )?PUBLIC KEY-----|---- BEGIN SSH2 PUBLIC KEY ----)/

export function sshKeyShape(text: string): SshKeyShape | undefined {
  const line = text.trimStart().split("\n")[0].trim()
  const pem = /^-----BEGIN (OPENSSH |RSA |EC |DSA |)PRIVATE KEY-----$/.exec(line)
  if (pem) return PRIVATE[pem[1]]
  return PUBLIC.test(line) ? "public" : undefined
}
