import { describe, expect, test } from "bun:test"
import { loadProofText, provenLive, reloadState } from "./load-proof"

describe("load proof", () => {
  test("only a load the master was seen taking up, or no proof at all, is live", () => {
    expect(provenLive(undefined)).toBe(true)
    expect(provenLive({ state: "loaded" })).toBe(true)
    for (const state of ["refused", "unconfirmed", "unchecked"]) {
      expect(provenLive({ state })).toBe(false)
      expect(reloadState({ state })).toBe("saved and reloaded")
    }
    expect(reloadState({ state: "loaded" })).toBe("is live")
  })

  test("a loaded proof names its workers and the sockets nginx holds", () => {
    const text = loadProofText({
      state: "loaded",
      workers: 4,
      listens: ["port 80/tcp", "127.0.0.1:8443/tcp"],
      listening: true,
    })
    expect(text).toBe(
      "nginx loaded it on 4 new workers, holding port 80/tcp and 127.0.0.1:8443/tcp.",
    )
    expect(loadProofText({ state: "loaded", workers: 1 })).toBe("nginx loaded it on 1 new worker.")
  })

  test("a refusal carries nginx's words, and the rest their note", () => {
    expect(
      loadProofText({
        state: "refused",
        error: "bind() to 0.0.0.0:8443 failed (98: Address in use)",
      }),
    ).toBe(
      "nginx refused it and serves what it had before: bind() to 0.0.0.0:8443 failed (98: Address in use).",
    )
    expect(
      loadProofText({ state: "unconfirmed", note: "nginx had not taken the reload up." }),
    ).toBe("nginx had not taken the reload up.")
    expect(loadProofText({ state: "unchecked" })).toBe("Whether nginx loaded it could not be read.")
  })
})
