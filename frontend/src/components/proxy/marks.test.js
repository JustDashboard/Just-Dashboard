import { describe, expect, test } from "bun:test"
import { certificateProduct } from "./marks"

describe("the mark a certificate is drawn with", () => {
  test("Let's Encrypt, by its issuer or certbot's directory", () => {
    expect(certificateProduct({ issuer: "R11", source: "imported" })).toBe("lets-encrypt")
    expect(
      certificateProduct({ issuer: "Unknown", source: "certbot", path: "/etc/letsencrypt/live/a" }),
    ).toBe("lets-encrypt")
  })

  // A staging issuer matches no Let's Encrypt pattern, but certbot's directory
  // drew the mark anyway, and a certificate browsers refuse read as trusted.
  test("a test certificate gets no mark, wherever it lives", () => {
    expect(
      certificateProduct({
        issuer: "(STAGING) Riddling Rhubarb R12",
        source: "certbot",
        path: "/etc/letsencrypt/live/test.example.com/fullchain.pem",
        staging: true,
      }),
    ).toBeUndefined()
    expect(certificateProduct({ issuer: "R11", staging: true })).toBeUndefined()
  })
})
