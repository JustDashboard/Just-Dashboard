package api

import (
	"context"
	"math"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/Wayy01/Just-Dashboard/backend/internal/deploy"
)

// What the assistant reads is masked before it is sent: the model belongs to a
// provider, and a secret's value is the one thing the provider is kept from. It
// is not the person being kept from anything — they can read every value on its
// page — and it is the one rule full access does not lift.
//
// Three layers, in order, over every string of every answer:
//
//   - the values: every deployment's secret values (PlanningStore.DeploymentSecrets,
//     including ones since rotated, what references point at, shared variables,
//     saved credentials and trigger secrets), read once per press when first needed;
//   - the shapes deploy.DiagnosisMasker knows: a private key, a URL's password,
//     an Authorization header, NAME=value where the name says secret, and the
//     tokens issuers shape;
//   - here: a field whose name says it holds a secret — password, token, apiKey,
//     privateKey, credential, dsn, connectionString, cookie, session and their
//     kin — whatever its value, and a value shaped like key material: a PEM block
//     or a long random token.

// agentSecretSuffixes are what a field's name ends with, lower-cased and with
// its separators gone, when the field holds a secret.
var agentSecretSuffixes = []string{
	"password", "passwd", "passphrase", "secret", "token", "apikey", "privatekey", "accesskey", "secretkey",
	"credential", "credentials", "dsn", "connectionstring", "connstring", "authorization", "cookie", "cookies",
	"session", "plaintext", "bearer", "signingkey", "encryptionkey", "masterkey", "recoverycodes", "totp",
}

// agentSecretField reports whether a field's name says its value is a secret.
func agentSecretField(key string) bool {
	normalized := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, key)
	for _, suffix := range agentSecretSuffixes {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

var (
	// agentKeyBlock is a PEM block of anything private or secret, whole.
	agentKeyBlock = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*(?:PRIVATE|SECRET|ENCRYPTED)[A-Z0-9 ]*-----.*?(?:-----END [A-Z0-9 ]*-----|$)`)
	// agentJWT is a signed token's three parts.
	agentJWT = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	// agentLongToken is a run of what a random token is made of, long enough to
	// be one; agentRandom decides whether it is.
	agentLongToken = regexp.MustCompile(`[A-Za-z0-9_\-+/=]{32,}`)
)

// agentRandom reports whether a run of characters is random enough to be key
// material: both cases and digits, which a hash, an id or a word is not, and the
// spread of characters a random draw has.
func agentRandom(token string) bool {
	if strings.Count(token, "/") > 2 {
		// A path.
		return false
	}
	var upper, lower, digits int
	counts := map[rune]int{}
	for _, r := range token {
		counts[r]++
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		case unicode.IsDigit(r):
			digits++
		}
	}
	if upper == 0 || lower == 0 || digits < 2 {
		return false
	}
	entropy := 0.0
	n := float64(len(token))
	for _, c := range counts {
		p := float64(c) / n
		entropy -= p * math.Log2(p)
	}
	return entropy >= 4.2
}

// agentMaskShapes masks what looks like key material in a string.
func agentMaskShapes(text string) string {
	text = agentKeyBlock.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	text = agentJWT.ReplaceAllString(text, deploy.DiagnosisMasked)
	return agentLongToken.ReplaceAllStringFunc(text, func(token string) string {
		if agentRandom(token) {
			return deploy.DiagnosisMasked
		}
		return token
	})
}

// agentMaskText masks one string: a key block whole first, so no line of it is
// left behind by a mask of its first line alone, then the values, then the shapes.
func agentMaskText(text string, masker *deploy.DiagnosisMasker) string {
	text = agentKeyBlock.ReplaceAllString(text, "[REDACTED PRIVATE KEY]")
	if masker != nil {
		text, _ = masker.Mask(text)
	}
	return agentMaskShapes(text)
}

// agentMaskValue masks a JSON value read whole: every string in it, and every
// string under a field whose name says secret.
func agentMaskValue(value any, masker *deploy.DiagnosisMasker) any {
	return agentMask(value, masker, false)
}

func agentMask(value any, masker *deploy.DiagnosisMasker, secret bool) any {
	switch v := value.(type) {
	case string:
		if secret && v != "" {
			return deploy.DiagnosisMasked
		}
		return agentMaskText(v, masker)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = agentMask(item, masker, secret)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			// A field named for a secret holds one whatever it is: its strings,
			// and a list of them. An object under it names its own fields.
			out[key] = agentMask(item, masker, agentSecretField(key))
		}
		return out
	}
	return value
}

// agentSecrets is a press's mask of every deployment's secret values, read
// once, the first time an answer needs it.
type agentSecrets struct {
	once   sync.Once
	masker *deploy.DiagnosisMasker
	err    error
}

// masker reads every deployment's secret values. A value that cannot be opened
// to mask is an error, and nothing is read or changed without it.
func (a *agentSecrets) get(ctx context.Context, s *Server) (*deploy.DiagnosisMasker, error) {
	a.once.Do(func() {
		values, err := s.everySecret(ctx)
		if err != nil {
			a.err = err
			return
		}
		a.masker = deploy.NewDiagnosisMasker(values)
	})
	return a.masker, a.err
}

// everySecret is every deployment's secret values, as each one's own masker
// reads them.
func (s *Server) everySecret(ctx context.Context) ([]string, error) {
	rows, err := s.Store.DB.QueryContext(ctx, `SELECT id FROM deploy_projects`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	values := []string{}
	if s.modules.deployPlanning == nil {
		return values, nil
	}
	for _, id := range ids {
		more, err := s.modules.deployPlanning.DeploymentSecrets(ctx, id)
		if err != nil {
			return nil, err
		}
		values = append(values, more...)
	}
	// With no deployment, the saved credentials and shared variables are still
	// the server's; a project id no row has reads only those.
	if len(ids) == 0 {
		more, err := s.modules.deployPlanning.DeploymentSecrets(ctx, 0)
		if err != nil {
			return nil, err
		}
		values = append(values, more...)
	}
	return values, nil
}
