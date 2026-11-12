# The master key

`JD_MASTER_KEY` is 32 bytes, hex, in the install directory's `.env`. `cmd/server` builds one
`auth.Sealer` from it and removes it from the process environment. Everything below follows from that
one type (`internal/auth/crypto.go`).

## What it does

- **Seals.** `Sealer.Seal` is AES-256-GCM under the key itself, a fresh nonce per value, written as
  standard base64 of nonce, ciphertext and tag. Nothing else encrypts with the key: the cipher is a
  private field. About sixty call sites seal, across thirty-odd files.
- **Derives.** `Sealer.DeriveHMAC` keys an HMAC with `sha256("jd-derive-v1" || key)`. Two callers: the
  WebAuthn user handle of an account (`auth.Service.passkeyHandle`), which an authenticator stores and
  the server compares with a fresh derivation at every passkey sign-in, and the suggested hostname of
  a deployment (`handlers_deploy_hostname.go`).

## Where sealed values are

There is no list, and a list kept by hand would be wrong. What is known:

- Most are a column of the store whose name ends in `_enc` or `_sealed` (about twenty distinct names
  over more tables).
- Some are in a column named for something else, whole or as a field of a JSON document: the account
  link's identity is the `value` of a `settings` row, and the backup archive key is `Identity` inside
  the JSON of another.
- Some never rest: a passkey challenge is sealed and opened again within minutes.
- Copies of the store hold them too: `JD_DATA_DIR/pre-update/vpsd-*.db` from each update, a store
  moved to `set-aside/`, and every backup of the dashboard itself, on this server and wherever its
  jobs send them. A backup restores only with the key it was made under.
- So do pages of `vpsd.db` that SQLite has freed and not reused, and the `-wal`.

The only complete way to find them is the cipher's own: a string that authenticates under the key is
a sealed value, and nothing else is (a false match is one in 2^128). A finder walks every text value of
every column of every table, every string inside one that parses as JSON, and every string inside what
it opens, and tries each.

## Where copies of the key are

`.env`; `.env.backup.<epoch>`, which `install.sh` writes and never removes; up to five
`.env.jd-port-previous-*` (`internal/stackports`); `JD_DATA_DIR/install.env` after an uninstall that
kept the data, until the first start under the same key removes it; and the backend container's
environment, which `docker inspect` prints. Every role reads all but the last through the Files page
([what a role is](invariants.md#what-a-role-is-and-what-it-is-not)).

## It cannot be changed yet

Replacing the key in `.env` makes every sealed value unreadable; there is no command that re-seals
them (audit of 10 November 2026, finding SEC-07). `SECURITY.md` says so to the operator, and says what
to do instead when someone who could read the key is no longer trusted: change the secrets
themselves, which that person could open.

It was not built in 0.8.6 because it touches every stored secret on every install and the pass that
looked at it could not make it provably safe. A rotation is finished only when all of this holds:

1. **Offline, and proven offline.** The running server seals with the key it started with, so one
   value written during a rotation is sealed with the old key after the store has moved to the new
   one. The command runs with the backend stopped and proves it: the store's `walGuard` holds a read
   lock for as long as a server has it open, so taking SQLite's exclusive lock fails while one does.
2. **A copy of the store first**, taken after the lock, checked to open, and named in what the command
   prints. It is sealed with the old key, so the command says when to delete it.
3. **Found mechanically**, as above, never from a list of columns.
4. **One transaction.** Every value found is opened with the old key, sealed with the new one and
   written back in place: the same row, the same JSON around it, byte for byte apart from the token.
5. **Proven before the commit.** Inside the transaction the finder runs again with the new key and
   must open exactly the values the first walk found, each to the same plaintext, and a walk with the
   old key must find none. Anything else rolls back.
6. **The derivations carried over.** A new key changes `DeriveHMAC`, so every passkey would stop
   signing in (`passkeys.go` compares the handle the authenticator returns with a fresh derivation).
   The derivation key has to stop following the master key first: kept sealed in the store from the
   first rotation on, and loaded wherever a `Sealer` is built (`cmd/server`, `admin/link.go`,
   `admin/hub.go`). Keeping the old one costs nothing: it opens nothing, and all it yields is a handle
   that names nobody and a suggested hostname.
7. **The key and the store change together or not at all.** They are two files. The new key is
   written durably beside the store before the commit, `.env` is replaced by rename after it, and a
   second run after a crash in between finishes the job instead of generating a third key.
8. **Nothing old left behind**, or the old key still opens something: `VACUUM` and a truncating
   checkpoint for freed pages and the `-wal`, the `pre-update` copies, the `.env` copies listed above,
   and `install.env`. Backups made before stay sealed with the old key; the guide has to say that
   they restore only with it, and that the first backup after a rotation is the first one the old key
   cannot open.
9. **A dry run** that does 1 to 5 on the copy and commits nothing, so an operator rehearses on their
   own data and sees the count per table before trusting it.
10. **Tests that would catch a missed value**: a store filled through the real write paths of every
    package that seals (not rows written by the test), rotated, then every reader run against it; a
    crash at each step of 7; a value nested in JSON and one nested in a sealed document; and a server
    that is still running.

Where it would live: the re-sealing in a package of its own beside `internal/admin`, reached as
`--admin rotate-master-key` through `scripts/manage.sh`, which already runs admin commands in a
one-off container with the backend stopped or not.
