package auth

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Passkeys are WebAuthn discoverable credentials that stand in for the
// password at sign-in. They replace the password and nothing else: an account
// with an authenticator is still asked for its code after a passkey, a session
// owing that code still reaches only the 2FA routes, and JD_REQUIRE_2FA still
// decides only whether an unenrolled account may sign in. Every passkey sign-in
// goes through startSession, the same function a password sign-in does, so the
// two cannot drift apart on what a new session owes.
//
// A credential belongs to the name the dashboard was reached at when it was
// registered (its relying party ID): browsers bind it there and offer it
// nowhere else, so one made at dash.example.com does not sign in at the
// tailnet name, and none can be made at an IP address, which WebAuthn refuses.

var (
	// ErrPasskeyChallenge is a ceremony the server did not start, already
	// finished, started for another session or let expire.
	ErrPasskeyChallenge = errors.New("this passkey request has expired or was already used; start again")
	// ErrPasskeyRejected is a response that did not verify: a wrong origin or
	// relying party, a bad signature, no user verification.
	ErrPasskeyRejected = errors.New("the passkey could not be verified")
	// ErrPasskeyUnknown is a credential no account holds, a removed one
	// included.
	ErrPasskeyUnknown = errors.New("this passkey is not registered with any account here")
	// ErrPasskeyCloned is a signature counter that did not move forward: two
	// copies of the private key may exist.
	ErrPasskeyCloned = errors.New("this passkey's signature counter went backwards, so it may have been copied; it was refused")
	// ErrPasskeyOrigin is a request from somewhere a passkey cannot be used:
	// an IP address, plain http off this machine, or an origin the dashboard
	// is not served at.
	ErrPasskeyOrigin = errors.New("passkeys are not available here")
	// ErrReauthentication is a wrong password or code when registering one.
	ErrReauthentication = errors.New("password or code is incorrect")
)

// passkeyChallengeTTL bounds a ceremony: long enough for a person to find
// their phone, short enough that an abandoned one is gone.
const (
	passkeyChallengeTTL     = 5 * time.Minute
	maxPasskeysPerAccount   = 25
	maxPasskeyNameRunes     = 64
	passkeyRPDisplayName    = "Just Dashboard"
	passkeyHandleDerivation = "webauthn-user-handle-v1"
)

// RelyingParty is where a ceremony happens: the dashboard's host name (the
// RP ID) and the exact origin the browser is on.
type RelyingParty struct {
	ID     string
	Origin string
}

// PasskeyParty checks the origin a request came from and returns the relying
// party it means. The origin must be the host the request was sent to, or one
// configured as an allowed origin, and it must be one WebAuthn accepts: a name
// rather than an IP address, over https, or plain http on localhost. The
// reason is said in words the sign-in and security pages show.
func PasskeyParty(origin, requestHost string, allowed []string) (RelyingParty, error) {
	if origin == "" {
		return RelyingParty{}, fmt.Errorf("%w: the browser sent no origin", ErrPasskeyOrigin)
	}
	scheme, host, ok := strings.Cut(origin, "://")
	if !ok || strings.ContainsAny(host, "/?#@") || host == "" {
		return RelyingParty{}, fmt.Errorf("%w: %q is not an origin", ErrPasskeyOrigin, origin)
	}
	hostname := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		hostname = h
	}
	hostname = strings.ToLower(strings.Trim(hostname, "[]"))
	if net.ParseIP(hostname) != nil {
		return RelyingParty{}, fmt.Errorf("%w: the dashboard was opened at the IP address %s, and passkeys need its name; open it by its host name to use one", ErrPasskeyOrigin, hostname)
	}
	local := hostname == "localhost" || strings.HasSuffix(hostname, ".localhost")
	if scheme != "https" && !(scheme == "http" && local) {
		return RelyingParty{}, fmt.Errorf("%w: passkeys need https (or http on localhost)", ErrPasskeyOrigin)
	}
	served := strings.EqualFold(host, requestHost)
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimRight(strings.TrimSpace(candidate), "/"), origin) {
			served = true
		}
	}
	if !served {
		return RelyingParty{}, fmt.Errorf("%w: %s is not where this dashboard is served", ErrPasskeyOrigin, origin)
	}
	return RelyingParty{ID: hostname, Origin: scheme + "://" + strings.ToLower(host)}, nil
}

func (rp RelyingParty) webAuthn() (*webauthn.WebAuthn, error) {
	return webauthn.New(&webauthn.Config{
		RPID:          rp.ID,
		RPDisplayName: passkeyRPDisplayName,
		RPOrigins:     []string{rp.Origin},
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyChallengeTTL, TimeoutUVD: passkeyChallengeTTL},
			Registration: webauthn.TimeoutConfig{Enforce: true, Timeout: passkeyChallengeTTL, TimeoutUVD: passkeyChallengeTTL},
		},
	})
}

// Passkey is a credential as the security page reads it.
type Passkey struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	RPID       string    `json:"rpId"`
	CreatedAt  time.Time `json:"createdAt"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	// Synced says the authenticator backs the credential up (a password
	// manager or a phone's keychain) rather than keeping it on one device.
	Synced     bool     `json:"synced"`
	Transports []string `json:"transports"`
}

// passkeyUser is an account as the WebAuthn library sees it.
type passkeyUser struct {
	user        *User
	handle      []byte
	credentials []webauthn.Credential
}

func (u *passkeyUser) WebAuthnID() []byte                         { return u.handle }
func (u *passkeyUser) WebAuthnName() string                       { return u.user.Username }
func (u *passkeyUser) WebAuthnDisplayName() string                { return u.user.DisplayName }
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

// passkeyHandle is the opaque user handle an authenticator stores with a
// credential and returns at a discoverable sign-in. It is derived from the
// account id under the install's key, so it names nobody to anyone who reads
// it off an authenticator, needs no column, and is stable for the account's
// life.
func (s *Service) passkeyHandle(userID int64) []byte {
	return s.sealer.DeriveHMAC(passkeyHandleDerivation, []byte(strconv.FormatInt(userID, 10)))
}

func (s *Service) passkeyAccount(ctx context.Context, userID int64, rpID string) (*passkeyUser, error) {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	rows, err := s.st.DB.QueryContext(ctx, `SELECT credential_json FROM webauthn_credentials WHERE user_id = ? AND rp_id = ? ORDER BY id`, userID, rpID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	account := &passkeyUser{user: u, handle: s.passkeyHandle(userID), credentials: []webauthn.Credential{}}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var credential webauthn.Credential
		if err := json.Unmarshal([]byte(raw), &credential); err != nil {
			return nil, err
		}
		account.credentials = append(account.credentials, credential)
	}
	return account, rows.Err()
}

// challengeEnvelope is a ceremony between its begin and its finish. It is
// not kept on the server: it is sealed with the install's key and handed to
// the browser as the challenge id, so beginning a sign-in writes nothing
// (and a sign-in page's autofill can ask for one on every load), while the
// seal means the browser can neither read nor alter what it carries.
type challengeEnvelope struct {
	ID        string               `json:"id"`
	Kind      string               `json:"kind"`
	UserID    int64                `json:"userId"`
	SessionID string               `json:"sessionId"`
	RPID      string               `json:"rpId"`
	Origin    string               `json:"origin"`
	Expires   int64                `json:"expires"`
	Data      webauthn.SessionData `json:"data"`
}

func (s *Service) sealChallenge(kind string, userID int64, sessionID string, rp RelyingParty, data *webauthn.SessionData) (string, error) {
	encoded, err := json.Marshal(challengeEnvelope{
		ID: RandomToken(16), Kind: kind, UserID: userID, SessionID: sessionID, RPID: rp.ID, Origin: rp.Origin,
		Expires: time.Now().Add(passkeyChallengeTTL).Unix(), Data: *data,
	})
	if err != nil {
		return "", err
	}
	return s.sealer.Seal(string(encoded))
}

// takeChallenge opens a ceremony and spends it, if it is one the caller may
// finish: the same kind, account, session and relying party, not expired,
// and not finished before. Spending is a row keyed by the ceremony's own id,
// so a response is tried against a challenge once, however often it is sent.
func (s *Service) takeChallenge(ctx context.Context, token, kind string, userID int64, sessionID string, rp RelyingParty) (*webauthn.SessionData, error) {
	if token == "" || len(token) > 8192 {
		return nil, ErrPasskeyChallenge
	}
	plain, err := s.sealer.Open(token)
	if err != nil {
		return nil, ErrPasskeyChallenge
	}
	var envelope challengeEnvelope
	if err := json.Unmarshal([]byte(plain), &envelope); err != nil || envelope.ID == "" {
		return nil, ErrPasskeyChallenge
	}
	now := time.Now().Unix()
	if envelope.Kind != kind || envelope.UserID != userID || envelope.SessionID != sessionID || envelope.Expires <= now {
		return nil, ErrPasskeyChallenge
	}
	if envelope.RPID != rp.ID || envelope.Origin != rp.Origin {
		return nil, fmt.Errorf("%w: it was started at %s and finished at %s", ErrPasskeyChallenge, envelope.Origin, rp.Origin)
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM webauthn_spent_challenges WHERE expires_at <= ?`, now); err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO webauthn_spent_challenges(id, expires_at) VALUES(?, ?)`, envelope.ID, envelope.Expires); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "constraint") {
			return nil, ErrPasskeyChallenge
		}
		return nil, err
	}
	return &envelope.Data, nil
}

// VerifyReauthentication is the fresh proof registering a passkey needs from
// a session that is already complete: the password, and the authenticator's
// code where one is enrolled. An accepted code is consumed as it would be at
// sign-in, so it cannot be used twice, but the session is not changed.
func (s *Service) VerifyReauthentication(ctx context.Context, userID int64, password, code string) error {
	if !s.VerifyUserPassword(ctx, userID, password) {
		return ErrReauthentication
	}
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.TOTPEnabled {
		return nil
	}
	var sealed string
	if err := s.st.DB.QueryRowContext(ctx, `SELECT totp_secret FROM users WHERE id = ? AND totp_enabled = 1`, userID).Scan(&sealed); err != nil {
		return ErrReauthentication
	}
	step, ok := s.validTOTPStep(sealed, code)
	if !ok {
		return ErrReauthentication
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_enabled = 1 AND totp_secret = ? AND totp_last_step < ?`,
		step, userID, sealed, step)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return ErrReauthentication
	}
	return nil
}

// BeginPasskeyRegistration starts registering a passkey for an account, from
// one of its complete sessions. The caller has already re-authenticated.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, userID int64, sessionID string, rp RelyingParty) (*protocol.CredentialCreation, string, error) {
	account, err := s.passkeyAccount(ctx, userID, rp.ID)
	if err != nil {
		return nil, "", err
	}
	var count int
	if err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return nil, "", err
	}
	if count >= maxPasskeysPerAccount {
		return nil, "", fmt.Errorf("%w: an account holds at most %d passkeys; remove one first", ErrPasskeyRejected, maxPasskeysPerAccount)
	}
	web, err := rp.webAuthn()
	if err != nil {
		return nil, "", err
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(account.credentials))
	for _, credential := range account.credentials {
		exclude = append(exclude, credential.Descriptor())
	}
	// A passkey is discoverable (it signs in with no username) and always
	// verifies its user — a PIN, a fingerprint, a face — because it stands in
	// for the password, not beside it.
	creation, data, err := web.BeginRegistration(account,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(exclude),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
	)
	if err != nil {
		return nil, "", err
	}
	id, err := s.sealChallenge("register", userID, sessionID, rp, data)
	if err != nil {
		return nil, "", err
	}
	return creation, id, nil
}

// FinishPasskeyRegistration verifies the browser's answer to a registration
// and keeps the credential under the name the account holder gave it.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, userID int64, sessionID, challengeID, name string, rp RelyingParty, response []byte) (*Passkey, error) {
	name, err := passkeyName(name)
	if err != nil {
		return nil, err
	}
	data, err := s.takeChallenge(ctx, challengeID, "register", userID, sessionID, rp)
	if err != nil {
		return nil, err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPasskeyRejected, protocolDetail(err))
	}
	account, err := s.passkeyAccount(ctx, userID, rp.ID)
	if err != nil {
		return nil, err
	}
	web, err := rp.webAuthn()
	if err != nil {
		return nil, err
	}
	credential, err := web.CreateCredential(account, *data, parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrPasskeyRejected, protocolDetail(err))
	}
	if !credential.Flags.UserVerified {
		return nil, fmt.Errorf("%w: the authenticator did not verify its user", ErrPasskeyRejected)
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	res, err := s.st.DB.ExecContext(ctx, `INSERT INTO webauthn_credentials(user_id, credential_id, credential_json, rp_id, name, created_at)
		VALUES(?,?,?,?,?,?)`, userID, base64.RawURLEncoding.EncodeToString(credential.ID), string(encoded), rp.ID, name, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, fmt.Errorf("%w: this passkey is already registered", ErrPasskeyRejected)
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.passkey(ctx, userID, id)
}

// BeginPasskeyLogin starts a sign-in with no username: the browser offers
// whichever passkeys it holds for this relying party, and the one chosen says
// whose it is.
func (s *Service) BeginPasskeyLogin(_ context.Context, rp RelyingParty) (*protocol.CredentialAssertion, string, error) {
	web, err := rp.webAuthn()
	if err != nil {
		return nil, "", err
	}
	assertion, data, err := web.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", err
	}
	id, err := s.sealChallenge("login", 0, "", rp, data)
	if err != nil {
		return nil, "", err
	}
	return assertion, id, nil
}

// FinishPasskeyLogin verifies a passkey sign-in and starts a session exactly
// as a password sign-in would: complete only for an account with nothing left
// to prove, and otherwise owing the code or the enrolment the account owes.
// The passkey that signed in is returned for the audit entry.
func (s *Service) FinishPasskeyLogin(ctx context.Context, challengeID string, rp RelyingParty, response []byte, ip, userAgent string) (*LoginResult, *Passkey, error) {
	data, err := s.takeChallenge(ctx, challengeID, "login", 0, "", rp)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrPasskeyRejected, protocolDetail(err))
	}
	credentialID := base64.RawURLEncoding.EncodeToString(parsed.RawID)
	var rowID, userID int64
	var storedRP string
	err = s.st.DB.QueryRowContext(ctx, `SELECT id, user_id, rp_id FROM webauthn_credentials WHERE credential_id = ?`, credentialID).Scan(&rowID, &userID, &storedRP)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrPasskeyUnknown
	}
	if err != nil {
		return nil, nil, err
	}
	if storedRP != rp.ID {
		return nil, nil, fmt.Errorf("%w: it was registered at %s", ErrPasskeyUnknown, storedRP)
	}
	account, err := s.passkeyAccount(ctx, userID, rp.ID)
	if err != nil {
		return nil, nil, err
	}
	web, err := rp.webAuthn()
	if err != nil {
		return nil, nil, err
	}
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		// The handle the authenticator returns must be the account's own;
		// a credential copied onto another account's handle is refused.
		if string(userHandle) != string(account.handle) {
			return nil, ErrPasskeyUnknown
		}
		return account, nil
	}
	credential, err := web.ValidateDiscoverableLogin(handler, *data, parsed)
	if err != nil {
		var unknown *protocol.ErrorUnknownCredential
		if errors.As(err, &unknown) || errors.Is(err, ErrPasskeyUnknown) {
			return nil, nil, ErrPasskeyUnknown
		}
		return nil, nil, fmt.Errorf("%w: %s", ErrPasskeyRejected, protocolDetail(err))
	}
	if credential.Authenticator.CloneWarning {
		return nil, nil, ErrPasskeyCloned
	}
	if !credential.Flags.UserVerified {
		return nil, nil, fmt.Errorf("%w: the authenticator did not verify its user", ErrPasskeyRejected)
	}
	// Disabled is the account's state, not the passkey's, and refuses here as
	// it does a password. A password lockout does not: it is a brake on
	// guessing a password, and a passkey is not guessed.
	if account.user.Disabled {
		return nil, nil, ErrAccountDisabled
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx, `UPDATE webauthn_credentials SET credential_json = ?, last_used_at = ? WHERE id = ?`,
		string(encoded), time.Now().Unix(), rowID); err != nil {
		return nil, nil, err
	}
	result, err := s.startSession(ctx, account.user, ip, userAgent, "passkey")
	if err != nil {
		return nil, nil, err
	}
	key, err := s.passkey(ctx, userID, rowID)
	if err != nil {
		return nil, nil, err
	}
	return result, key, nil
}

// protocolDetail is the library's reason for refusing a response, which
// says what was wrong (an origin, a relying party, a flag) and never holds a
// secret.
func protocolDetail(err error) string {
	var perr *protocol.Error
	if errors.As(err, &perr) && perr.Details != "" {
		return perr.Details
	}
	return err.Error()
}

func passkeyName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", fmt.Errorf("%w: give the passkey a name", ErrPasskeyRejected)
	}
	if utf8.RuneCountInString(name) > maxPasskeyNameRunes {
		return "", fmt.Errorf("%w: a passkey's name is at most %d characters", ErrPasskeyRejected, maxPasskeyNameRunes)
	}
	return name, nil
}

func (s *Service) scanPasskey(row interface{ Scan(...any) error }) (*Passkey, error) {
	var key Passkey
	var raw string
	var created, used int64
	if err := row.Scan(&key.ID, &key.Name, &key.RPID, &created, &used, &raw); err != nil {
		return nil, err
	}
	key.CreatedAt = time.Unix(created, 0).UTC()
	if used > 0 {
		key.LastUsedAt = time.Unix(used, 0).UTC()
	}
	var credential webauthn.Credential
	key.Transports = []string{}
	if json.Unmarshal([]byte(raw), &credential) == nil {
		key.Synced = credential.Flags.BackupEligible
		for _, transport := range credential.Transport {
			key.Transports = append(key.Transports, string(transport))
		}
	}
	return &key, nil
}

const passkeyColumns = `id, name, rp_id, created_at, last_used_at, credential_json`

func (s *Service) passkey(ctx context.Context, userID, id int64) (*Passkey, error) {
	key, err := s.scanPasskey(s.st.DB.QueryRowContext(ctx, `SELECT `+passkeyColumns+` FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return key, err
}

// ListPasskeys is an account's passkeys, newest first.
func (s *Service) ListPasskeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.st.DB.QueryContext(ctx, `SELECT `+passkeyColumns+` FROM webauthn_credentials WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Passkey{}
	for rows.Next() {
		key, err := s.scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *key)
	}
	return out, rows.Err()
}

// RenamePasskey changes the name a passkey is listed under.
func (s *Service) RenamePasskey(ctx context.Context, userID, id int64, name string) (*Passkey, error) {
	name, err := passkeyName(name)
	if err != nil {
		return nil, err
	}
	res, err := s.st.DB.ExecContext(ctx, `UPDATE webauthn_credentials SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.passkey(ctx, userID, id)
}

// RemovePasskey deletes one of an account's passkeys; it signs in nowhere
// from then on.
func (s *Service) RemovePasskey(ctx context.Context, userID, id int64) (*Passkey, error) {
	key, err := s.passkey(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if _, err := s.st.DB.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID); err != nil {
		return nil, err
	}
	return key, nil
}

// CountPasskeys is how many passkeys an account holds.
func (s *Service) CountPasskeys(ctx context.Context, userID int64) (int, error) {
	var count int
	err := s.st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?`, userID).Scan(&count)
	return count, err
}
