import { describe, expect, test } from "bun:test"
import { sshKeyShape, tokenProduct } from "./secrets"

describe("tokenProduct", () => {
  test("a token is named by the prefix its provider gave it", () => {
    expect(tokenProduct("ghp_example")).toEqual({
      product: "github",
      word: "GitHub token",
    })
    expect(tokenProduct("github_pat_11ABCDEFG0123456789_abcdef")).toEqual({
      product: "github",
      word: "GitHub token",
    })
    expect(tokenProduct("ghs_example")?.word).toBe("GitHub App token")
    expect(tokenProduct("gho_abc")?.word).toBe("GitHub OAuth token")
    expect(tokenProduct("glpat-xxxxxxxxxxxxxxxxxxxx")).toEqual({
      product: "gitlab",
      word: "GitLab token",
    })
    expect(tokenProduct("gldt-xxxxxxxxxxxxxxxxxxxx")?.word).toBe("GitLab deploy token")
    expect(tokenProduct("glrt-xxxxxxxxxxxxxxxxxxxx")?.product).toBe("gitlab")
    expect(tokenProduct("ATBB3gHb7X9vL2")).toEqual({
      product: "bitbucket",
      word: "Bitbucket app password",
    })
    expect(tokenProduct("ATATT3xFfGF0abc")?.product).toBe("bitbucket")
    expect(tokenProduct("dckr_pat_example")).toEqual({
      product: "docker",
      word: "Docker Hub token",
    })
  })

  test("the whitespace a paste brings along does not hide the prefix", () => {
    expect(tokenProduct("  glpat-abc\n")?.product).toBe("gitlab")
  })

  test("a secret that says nothing about itself gets no reading", () => {
    expect(tokenProduct("hunter2")).toBeUndefined()
    expect(tokenProduct("")).toBeUndefined()
    // Prefixes are case-sensitive: an all-lowercase password is not an app password.
    expect(tokenProduct("atbbcorrecthorse")).toBeUndefined()
    expect(tokenProduct("my ghp_ token")).toBeUndefined()
  })
})

describe("sshKeyShape", () => {
  test("a private key is named by the block its first line opens", () => {
    expect(sshKeyShape("-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n")).toBe(
      "openssh",
    )
    expect(sshKeyShape("-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA")).toBe("rsa")
    expect(sshKeyShape("-----BEGIN EC PRIVATE KEY-----\nMHcCAQEE")).toBe("ec")
    expect(sshKeyShape("-----BEGIN DSA PRIVATE KEY-----\nMIIBuwIBAAKBgQ")).toBe("dsa")
    expect(sshKeyShape("-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEI")).toBe("pkcs8")
  })

  test("leading blank lines and Windows line endings are the paste, not the key", () => {
    expect(sshKeyShape("\n\n  -----BEGIN OPENSSH PRIVATE KEY-----\r\nb3Bl\r\n")).toBe("openssh")
  })

  test("the public half is recognised in every form it is handed out in", () => {
    expect(sshKeyShape("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI me@laptop")).toBe("public")
    expect(sshKeyShape("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQ deploy")).toBe("public")
    expect(sshKeyShape("ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHA")).toBe("public")
    expect(sshKeyShape("sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5")).toBe("public")
    expect(sshKeyShape("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA")).toBe("public")
    expect(sshKeyShape("---- BEGIN SSH2 PUBLIC KEY ----\nComment: me")).toBe("public")
  })

  test("anything else is not named", () => {
    expect(sshKeyShape("")).toBeUndefined()
    expect(sshKeyShape("PuTTY-User-Key-File-3: ssh-ed25519")).toBeUndefined()
    expect(sshKeyShape("-----BEGIN CERTIFICATE-----\nMIID")).toBeUndefined()
    expect(sshKeyShape("-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIF")).toBeUndefined()
    expect(sshKeyShape("ssh-ed25519")).toBeUndefined()
  })
})
