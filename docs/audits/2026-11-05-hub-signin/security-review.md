# Hub sign-in: security review against invariant 2

Hub sign-in lets the Just Hub a server is linked to open that server for an account and land the person on it
signed in, without this server asking for its own authenticator code. That is the one documented exception to
[invariant 2](../../internal/security/invariants.md#invariant-2-what-two-factor-still-guarantees). Condition 1 of
[protocol 9.1](../../internal/backend/hub-link-protocol.md#91-the-exception-to-invariant-2) says a written
security review of section 9 must be done and recorded before a release turns the code on anywhere. This is that
review. [Protocol 9.12](../../internal/backend/hub-link-protocol.md#912-before-turning-it-on) lists what it has to
check, and the sections below go through that list in order.

- **What was reviewed.** JD branch `feat/0.8.5-hub-signin`, stacked on `feat/0.8.5-hub-ui` and
  `feat/0.8.5-hub-link`, at the commit that adds this file. The hub side is Just Hub's `feat/v1` at `a4642ea`.
- **How.** For each item: the code that enforces it, the test that holds it, and whether it holds. Paths are from
  the repository root. Test names are Go test functions unless a file says otherwise.
- **Result.** Every item holds as of this commit. The review found three gaps, listed next, and the same commit
  fixes all three. The residual risks at the end are by design. The owner accepts them by signing the last section.

## What the review found and fixed

1. **A hub session could change the dashboard's own settings.** `PUT /api/v1/dashboard/config` had neither
   `RefuseHubSession` nor `RequireSession`, and a hub session reached its handler. From one request an
   administrator's hub session could widen `JD_ALLOWED_CIDRS`, bind a public address, turn `JD_REQUIRE_2FA` off,
   lengthen session lifetimes and turn the terminal on. Every one of those outlives the session. Turning
   `JD_REQUIRE_2FA` off also loosens condition 5, under which the session was opened, and makes a grant that an
   unenrolled account made before the policy usable again. On an install that narrowed `JD_FILE_ROOTS` and
   turned the terminal off, this form was a hub session's way back to root. **Fixed:** the route now carries
   `httpx.RequireSession` and `httpx.RefuseHubSession` (`backend/internal/api/handlers_selfupdate.go`). The rule in
   9.9 now names the dashboard's own settings, and both route tables list the route.
   `TestEveryRefusedRouteAnswersAHubSessionWith403` and `TestOnlyASessionChangesTheDashboardsOwnSettings` hold
   it. `POST /dashboard/restart` stays open, because it comes back on the configuration already on disk.
2. **The audit trail could not tell a hub session from the account's own session.** Every request a hub session
   made was written as actor `session`, with no hub user and no session id. The device check means a hub
   session's address is the same device the person uses locally, so the address did not tell them apart either.
   After a hub takeover, the operator could not find what the takeover did. **Fixed:** one builder,
   `httpx.AuditWho` and `principalEntry` (`backend/internal/httpx/auditmw.go`), writes who acted on every entry for
   a principal. A hub session is actor `hub-session`, and the assistant acting in one stays `assistant`. Both carry
   `hubUser` and `sessionId` (`audit_log.hub_user`, `session_id`, added through `store.addedColumns`). The session
   id is the `sessionId` of the `hub.signin` entry that opened the session. The handlers that write their own
   entries now start from `httpx.AuditEntry`: sockets at open (`recordAudit`), reads before they run
   (`recordRead`, `AuditRead`), and outcomes after the response. So do hublink's `hub.signin.revoke` entries,
   which are the only entry for a grant removed from a hub session. The audit page says "through Just Hub as
   <hub user>". Protocol 9.10 has a row for it, and `TestWhatAHubSessionDoesIsAuditedApartFromALocalSession`
   and `TestAHubSessionIsAuditedApartFromTheSameAccountsOwnSession` hold it.
3. **No test held the redeem's rate limit.** `POST /auth/hub/enter` sits in the `loginLim` group with
   `POST /auth/login` (`backend/internal/api/routes.go`), but no test said so.
   **Added:** `TestTheRedeemSharesThePasswordFormsBudgetPerAddress`.

Before this review, the end-to-end run had found `POST /auth/2fa/verify` open to a hub session. That route is
refused since (`TestAHubSessionIsRefusedTheCodeRouteBeforeAGuessIsSpent`).

## The checklist of protocol 9.12

### 1. The hub asks for its own TOTP code within 10 minutes, and a recovery code does not count

- **Hub.** `mint` in `backend/internal/signin/tickets.go` refuses to sign a ticket unless the hub session's
  `totp_at` is set, not in the future, and at most `totpFresh = 5 minutes` old. That limit is stricter than the
  protocol's 10 minutes. The ticket's `mfaAt` is that `totp_at`. `elevateTx` in `backend/internal/auth/totp.go`
  sets `totp_at` for a TOTP code only, so a recovery code completes a hub session but never makes it able to
  mint. Tests: `TestTicketRefusals` ("a recovery code only", "a TOTP code 6 minutes old",
  `fresh_2fa_required`, and no ticket row left behind) and `TestTicket` in `backend/internal/signin/signin_test.go`.
  `TestRecoveryCodesProveEverythingButTOTP` and `TestRecoveryCodesFreshnessFollowsTheTOTPCode` are in
  `backend/internal/auth/auth_test.go`.
- **JD.** Step 9 of `checkTicket` (`backend/internal/hublink/ticket.go`) refuses `mfaAt > iat` or
  `iat − mfaAt > 600` with `mfa_stale`. The ticket vectors include two `mfa_stale` cases.
- **Holds.** JD checks only what the hub signed. Whether the code was really asked for is the hub's word (see
  the residual risks).

### 2. The verification order and its tests, using the ticket vectors

- **Code.** `Service.redeem` (`backend/internal/hublink/redeem.go`) runs steps 1 and 2 (the switches, then a link
  that is not idle). Steps 3 to 9 are `checkTicket`: the shape, a pinned key not past `notAfter`, the Ed25519
  signature over the exact payload segment before any JSON is read, the payload's JSON rules scanned token by
  token, the hub and `serverId`, the time with 30 seconds of grace, and the hub's second factor. Step 10 is
  `checkDevice`, and step 11 is `spendNonce`, committed on its own. Steps 12 to 14 run in one `writeTx`. The
  first failure stops the redeem.
- **Tests.** `TestSignInTicketVectors` (`backend/internal/hublink/ticket_vectors_test.go`) runs all 37 vectors of
  `docs/internal/backend/hub-link-vectors.json` through the real `checkTicket`, `checkDevice` and nonce store.
  That is 19 `bad_ticket`, 9 `ok`, 3 `wrong_device`, 2 each of `ticket_expired`, `wrong_server` and `mfa_stale`,
  and the second redeem of a ticket already used. Through the router, `TestATicketIsRefusedAtTheStepThatFailsAndAudited`
  refuses a ticket at each step and audits each refusal. `TestSignInNeedsTheSwitchTheLinkAndTheGrant` covers
  steps 1, 2 and 12, and `TestAValidTicketOpensACompleteSessionForTheAccountThatOptedIn` covers the success.
- **Holds.**

### 3. The device check of step 10

- **Code.** `checkDevice` (`backend/internal/hublink/ticket.go`) skips the check only for a loopback client or
  `tailnetgate.ErrUnavailable`, and records the skip. WhoIs must name the ticket's `tsLogin`, compared in lower
  case, and its `tsNode` (`StableNodeID`). `ErrNotAPeer`, another login or node, and an answer that names no
  login or node all refuse. The check runs after the signature and the time, so a forged ticket costs no WhoIs,
  and before the nonce, so a stolen ticket tried from the wrong device is not spent. In production, WhoIs is
  tailscaled's local API (`hublink.New`).
- **Tests.** The vectors' `wrong_device` and `deviceCheck` cases. `TestADeviceTailscaledCannotNameIsRefused`
  runs through the real local API client. `TestATicketOpensOneSessionOnlyAndTheRightDeviceIsChecked` checks
  that the wrong device does not spend the ticket and the right one can still use it.
- **Holds.**

### 4. The transactions of 9.7, with the disconnect tests

- **Code.** `spendNonce` commits on its own before `writeTx` begins. The grant, the account and the new session
  are one `BEGIN IMMEDIATE` transaction that re-reads the link (`stateInTx`) and the grant. Every event that ends
  a grant or the link is its own write transaction that also revokes hub sessions, so the two serialise.
- **Tests.** `TestADisconnectBetweenTheNonceAndTheSessionLeavesNoSession` uses `SetAfterNonce`.
  `TestADisconnectBetweenTheSessionAndTheCommitLeavesNoSession` uses `SetBeforeSessionCommit`, and the disconnect
  waits for the commit and then revokes what it made. `TestTheLinkGoingIdleBetweenTheNonceAndTheSessionLeavesNoSession`
  covers the link going idle.
- **Holds.**

### 5. Grants end in every case of 9.3, replacement included, each event revoking the account's hub sessions in its own transaction

- **Code.** `auth.endHubGrantTx` runs inside each account change's own transaction: a password changed or reset,
  an authenticator enrolled, reset or turned off, the account disabled or deleted, and all its sessions revoked
  (`service.go`, `totp.go`, `user_update.go`, `password_reset.go`). Replacing an authenticator is turning it off
  and enrolling again, and both end the grant. In hublink, `endGrantTx` handles a removal (`Revoke`) and a
  replacement (`Allow`). `endUnofferedTx` handles a reply that stops offering a hub user, in the heartbeat's own
  transaction. `onClear` handles the link ending, and `PrepareSignIn` handles the switch found off.
- **Tests.** `TestEveryEndOfAGrantEndsItsHubSessions` makes a hub session, fires one event, and checks that the
  next request is `401`. It does this for each of 16 events: removal by the account, removal by an administrator,
  replacement, a disconnect, the account disabled, the account deleted, the password changed, the password
  reset, the authenticator reset, the authenticator turned off, all sessions revoked, the hub user no longer
  offered, sign-in no longer offered, the switch found off at start, a grant row deleted with no revocation, and
  an idle flag written with no revocation. `TestEnrollingAnAuthenticatorEndsAGrantMadeWithoutOne` covers
  enrolment. `TestRemovalInTheHubEndsGrantsAndHubSessions` and `TestEndingTheLinkDeletesGrantsTicketsAndHubSessions`
  are in `backend/internal/hublink/signin_test.go`.
- **Holds.** The tests prove the session is refused after each event. The one-transaction part is held by the
  code above and by the race tests of item 4.

### 6. Going idle and revoking are one transaction

- **Code.** `goIdle` (`backend/internal/hublink/node.go`) hands `revokeHubSessionsTx` to `engine.SetIdle`, which
  runs it in the transaction that stores the idle flag.
- **Tests.** `TestTheLinkGoingIdleRevokesEveryHubSessionAndKeepsTheGrants`,
  `TestAnotherMachineAtTheHubsNameRevokesEveryHubSession`, and the API race test of item 4.
- **Holds.**

### 7. The switch-off clean-up commits before the first request is served

- **Code.** `api.Server.Start` calls `hublink.PrepareSignIn` first. With either switch off, it deletes every
  grant, ticket record and hub session in one transaction, and returns an error if it cannot. `cmd/server/main.go`
  calls `Start` and returns on that error before `ListenAndServe`.
- **Tests.** The "hub sign-in is found off at start" event in `TestEveryEndOfAGrantEndsItsHubSessions` and
  `TestAHubSessionsSocketsEndWithIt`.
- **Holds.** No test makes the clean-up fail. The refusal to start is held by `main.go`'s order.

### 8. A hub session is refused on its next request with no revocation run

- **Code.** `auth.Service.ResolveSession` asks `hublink.checkHubSession` on every use of a hub session. It
  checks both switches, a stored link that is not idle, and a grant for this account and link whose hub user is
  the session's and whose `created_at` is not later than the session's. Without the check installed, no hub
  session resolves.
- **Tests.** The events "a grant row deleted with no revocation" and "an idle flag written with no revocation"
  in `TestEveryEndOfAGrantEndsItsHubSessions`, and `TestAnIdleLinkRefusesHubSignInAfterEveryRestart`.
- **Holds.**

### 9. A hub session is refused on every route of 9.9, by the route-walk test, and ends after 2 hours

- **Code.** `httpx.RefuseHubSession` sits on every route in the table of 9.9: credentials, links, the opt-in,
  every second-factor route, and, since this review, the dashboard's own settings. `auth.HubSessionTTL` is
  2 hours and `HubSessionIdle` is 60 minutes, whatever the install's own limits are
  (`backend/internal/auth/hub_signin.go`).
- **Tests.** `TestEveryCredentialAndLinkRouteRefusesAHubSession` walks the router and fails unless exactly
  `hubSessionRefused` carries the middleware. `TestTheDocumentedRouteTablesAreHubSessionRefused` holds the
  tables in `invariants.md` and protocol 9.9 to that list. `TestEveryRefusedRouteAnswersAHubSessionWith403`
  checks that nothing answers before the middleware. Also: `TestAHubSessionCannotMakeCredentialsOrLinks`,
  `TestAHubSessionEndsAfterTwoHours`, and `TestAHubSessionIdlesOutAfterAnHourWhateverTheInstallAllows`
  (`backend/internal/auth/hub_signin_test.go`).
- **Holds.**

### 10. Everything a hub session does is audited as a hub session's (9.10)

- **Code.** Item 2 of the findings above.
- **Tests.** `TestWhatAHubSessionDoesIsAuditedApartFromALocalSession` runs through the router. It checks the
  same mutation from a hub session and from a password session, the refused settings change, and the grant
  removal written by hublink. `TestAHubSessionIsAuditedApartFromTheSameAccountsOwnSession` covers the middleware,
  `AuditRead`, `AuditEntry` and the assistant in either session. The frontend test is
  `frontend/src/components/audit/marks.test.js`.
- **Holds** since this review.

### 11. Every socket a hub session opened closes when the session ends, at its expiry included

- **Code.** `api/hub_session_sockets.go`. Every socket `wsx.Upgrader` opens goes through `wsx.Guard`, and the
  Remote Desktop stream bridge's proxied sockets go through `guardHubStream`. A hub session's socket closes at
  `expires_at`, at once when a revocation commits (`SessionsEnded`), and otherwise at the next 15-second check.
  That check, `SessionStands` through `auth.Service.CheckSession`, does not count as use of the session.
- **Tests.** `TestAHubSessionsSocketsEndWithIt` runs every event of item 5 against a real socket.
  `TestAHubSessionsSocketEndsAtItsExpiry`, `TestAProxiedStreamEndsWithTheHubSession`, and
  `TestASignedInSessionsSocketIsLeftAlone` (a password session's socket stays open) are in
  `backend/internal/api/hub_session_sockets_test.go`.
- **Holds.**

### 12. The redeem route is rate limited and audited

- **Code.** `POST /auth/hub/enter` is in the group with `POST /auth/login`: `loginLim` per address, then
  `AuditMutations` and `RequireCSRF`. The body is at most 4 KiB. The redeem writes its own `hub.signin` entry for
  every outcome, steps 1 and 2 included, with the status it answered (`recordRedeem`).
- **Tests.** `TestTheRedeemSharesThePasswordFormsBudgetPerAddress`, added by this review, checks that the budget
  runs out, that the password form is refused from the same address, and that every redeem let through has its
  entry. `TestATicketIsRefusedAtTheStepThatFailsAndAudited` and `TestRedeemingReplacesTheBrowsersSession`
  check the entries' statuses.
- **Holds.**

### 13. `/hub/enter` removes the fragment before anything else, sends the ticket only in the redeem's body, and loads nothing from another origin

- **Code.** `frontend/src/lib/hub-enter.ts` writes the document by hand, and `frontend/src/app/hub/enter/route.ts`
  serves it with `Cache-Control: no-store` and `Referrer-Policy: no-referrer`. Its only script is inline with the
  response's CSP nonce, and only a charset comes before it. The script reads `location.hash` once and calls
  `history.replaceState` before any `fetch`. It never writes the ticket to storage, the console or the DOM.
- **Tests.** `frontend/src/lib/hub-enter.test.js` has "its first script is the inline one with this response's
  nonce", "it loads nothing from anywhere", "the ticket leaves the address before the script does anything else",
  "the ticket travels only in the redeem's body", and "nothing else in the frontend reads ticket= from a
  fragment". In a browser, `frontend/tests/browser/hub-signin.spec.ts` has "the ticket leaves the address first,
  travels only in the redeem, and the page lands on next" and "no ticket, no request".
- **Holds.**

### 14. The page asks before replacing another account's session

- **Code.** The page reads `/auth/session` first and asks Continue or Stay when another account is signed in, or
  when it cannot tell. The backend answers `409 other_account_signed_in` unless the body says `"replace": true`
  (`otherAccountsSessionTx`).
- **Tests.** The `hub-enter.test.js` group "who the browser is signed in as (§9.5 step 2)". In
  `hub-signin.spec.ts`: "signed in as another account, it asks first and redeems only on Continue", "when it
  cannot tell who is signed in, it asks rather than redeems", and "Stay keeps the session the browser has".
  `TestRedeemingReplacesTheBrowsersSession` covers the backend.
- **Holds.**

### 15. `JD_HUB_SIGNIN` with a test origin refuses to start in a release build

- **Code.** `config.Load` returns an error when `JD_HUB_SIGNIN` and `JD_HUB_LINK_TEST_ORIGIN` are both set and
  `hubSignInTestOriginAllowed` is false. It is false in `backend/internal/config/hubsignin_release.go`
  (`//go:build !hublinktest`), and only the local harness builds with `-tags hublinktest`.
- **Tests.** `TestHubSignInIsOffByDefaultAndRefusesATestOriginInARelease`.
- **Holds.**

### 16. The opt-in screen says what the hub can then do, what a hub session cannot do, and that turning it off does not undo what was done

- **Code.** `Costs` in `frontend/src/components/hub-link/sign-in-panel.tsx` covers four things. First, anyone who
  takes over the hub could open a session as this account with everything it can do, the Files page included,
  which reads every file and, with permission to change files or the terminal, is as good as root. Second, the
  session lasts two hours, and the dashboard's own forms refuse it keys, accounts, passwords, authenticators,
  passkeys, Linux and database users, VPN peers, saved credentials, revealed secrets, links and the dashboard's
  own settings. Third, the audit log marks what it does. Fourth, turning it off ends the hub's sessions without
  undoing what was done in them. The page also says what the hub is told about the account.
- **Tests.** "opting in says what it costs, asks for the password and code, and can be undone" in
  `hub-signin.spec.ts`.
- **Holds.**

## Residual risks

Hub sign-in does not remove these. The operator who sets `JD_HUB_SIGNIN=true` accepts them.

- **The Files page reads as root, for every role.** Any hub session can read every file under `JD_FILE_ROOTS`,
  which is `/` by default. That includes `/etc/shadow`, SSH private keys, WireGuard configurations, a project's
  `.env`, and this dashboard's own `.env` with `JD_MASTER_KEY` and its database. The route list in 9.9 does not
  reach a file read.
- **`file.write` writes any file as root.** A hub session for the `admin` and `limited` roles can plant an SSH key
  in `authorized_keys`, add a sudoers rule, or edit this dashboard's `.env` (`files.WriteChecked` has no
  deny-list). For those roles a hub session is as good as root, and what it writes outlives the session.
- **An administrator's terminal.** An administrator's hub session can open the terminal, a real shell on the
  host. The terminal closes with the session, but nothing it changed is undone.
- **A taken-over hub can open a session for any account that opted in.** It signs the ticket, so it names its
  own tailnet login and device. The device check stops a stolen ticket, not a hostile hub. `mfaAt` is also the
  hub's word. The limits are the 2-hour session, the route list, and the kill switches: `JD_HUB_SIGNIN`, a
  disconnect, or each account's own choice.
- **A leaked ticket.** A browser extension or proxy that reads the fragment can use the ticket once, within
  120 seconds. It must do so from the same device and tailnet login, or over loopback, or while tailscaled cannot
  be asked. Both of those skip the device check, and the audit entry says so.
- **The trail can be erased by what it records.** The audit entries are in the dashboard's database and mirrored
  to the process log. An administrator's hub session can write any file as root, including the database and the
  container logs. The trail is evidence of what a hub session did unless that session erased it. Send the
  process log off the machine if that matters.
- **Ending hub sign-in does not undo anything.** It ends the sessions and closes their sockets. Whatever they
  read or wrote stays read or written.

## Sign-off

By signing, the owner confirms that they have read this review, accept the residual risks above, and agree that
`JD_HUB_SIGNIN=true` may be set on servers running the reviewed code. A change to section 9, to the route list in
9.9 or to the opt-in screen's text needs this review brought up to date and signed again.

- Owner: ______________________
- Date: ______________________
- JD commit reviewed: ______________________
