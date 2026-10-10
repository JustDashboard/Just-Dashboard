import { clock, plural } from "@/lib/format"
import type { LoadProof } from "@/lib/types"

/**
 * Reading what nginx's master showed of a reload (`loadProof` on a site save
 * or POST /proxy/reload). `nginx -s reload` exiting 0 says only that the
 * signal went out; "live" is said only of a load the master was seen taking
 * up, and a backend that sends no proof is read as before.
 */

/** Whether a reload may be called live. */
export function provenLive(proof: LoadProof | undefined): boolean {
  return !proof || proof.state === "loaded"
}

/**
 * The proof as a sentence: when nginx loaded it, on how many new workers and
 * holding which sockets; or why it was not seen loading it.
 */
export function loadProofText(proof: LoadProof): string {
  switch (proof.state) {
    case "loaded": {
      const at = proof.loadedAt ? ` at ${clock(proof.loadedAt)}` : ""
      const workers = proof.workers ? ` on ${plural(proof.workers, "new worker")}` : ""
      const listens =
        proof.listening && proof.listens?.length ? `, holding ${proof.listens.join(" and ")}` : ""
      return `nginx loaded it${at}${workers}${listens}.`
    }
    case "refused":
      return `nginx refused it and serves what it had before: ${proof.error ?? "see its error log"}.`
    default:
      return proof.note ?? "Whether nginx loaded it could not be read."
  }
}

/** The toast title's verb for a reload that went out: live, or only sent. */
export function reloadState(proof: LoadProof | undefined): "is live" | "saved and reloaded" {
  return provenLive(proof) ? "is live" : "saved and reloaded"
}

/**
 * The toast for a reload that went out: success only for a load nginx was
 * seen taking up (or a backend sending no proof), with the proof's sentence;
 * a warning saying what was not seen otherwise.
 */
export function reloadToast(
  proof: LoadProof | undefined,
  said: { title: string; description?: string },
): { tone: "success" | "warning"; title: string; description?: string } {
  if (provenLive(proof)) return { tone: "success", ...said }
  return {
    tone: "warning",
    title: `${said.title.replace(/ reloaded$/, "")} reload not confirmed`,
    description: loadProofText(proof!),
  }
}
