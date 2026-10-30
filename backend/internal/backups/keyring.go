package backups

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

// An install seals what it writes to one key of its own (encryption.go). A
// server that imports archives another one wrote needs that server's key to
// open them, so a key can be imported: the file Export key… saves. The first
// one imported on a server that holds no key yet becomes its own — a server
// rebuilt or moved keeps sealing to the key whose copy is already off the
// machine, and there is still one file to keep. Any key imported after that
// only opens archives; nothing new is sealed to it.

// importedKeysSetting holds the keys imported beside this server's own.
const importedKeysSetting = "backups.archive-keys.imported"

// importedArchiveKey is one imported key: sealed with the master key, beside
// its public half and when it arrived.
type importedArchiveKey struct {
	Identity   string    `json:"identity"`
	Recipient  string    `json:"recipient"`
	ImportedAt time.Time `json:"importedAt"`
}

// ImportedKey is what the API says about an imported key.
type ImportedKey struct {
	Recipient  string    `json:"recipient"`
	ImportedAt time.Time `json:"importedAt"`
}

// KeyError is an encrypted archive this server cannot open, naming the key
// the manifest says it was sealed to, so the operator knows which exported
// file to bring. It is ErrNoArchiveKey or ErrArchiveKey to errors.Is.
type KeyError struct {
	Recipient string
	err       error
}

func (e *KeyError) Error() string {
	if errors.Is(e.err, ErrNoArchiveKey) {
		return fmt.Sprintf("this archive was sealed to backup key %s, and this server holds no backup key; import that key under Backups → Backup key, or decrypt the archive with the exported file", e.Recipient)
	}
	return fmt.Sprintf("this archive was sealed to backup key %s, which this server does not hold; import that key under Backups → Backup key, or decrypt the archive with the exported file", e.Recipient)
}

func (e *KeyError) Unwrap() error { return e.err }

// keyringMu serialises imports, which read the list, add to it and write it.
var keyringMu sync.Mutex

func (s *Store) importedKeys(ctx context.Context) ([]importedArchiveKey, error) {
	var raw string
	err := s.st.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, importedKeysSetting).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []importedArchiveKey
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return nil, errors.New("the imported backup keys are malformed")
	}
	return keys, nil
}

// identityFor opens the key an archive was sealed to: this server's own, or
// one imported. A read never makes a key.
func (s *Store) identityFor(ctx context.Context, recipient string) (*age.X25519Identity, error) {
	recipient = strings.TrimSpace(recipient)
	own, err := s.storedArchiveKey(ctx)
	if err != nil {
		return nil, err
	}
	if own != nil && own.Recipient == recipient {
		identity, _, err := s.archiveIdentity(ctx, false)
		if err != nil {
			return nil, err
		}
		if identity.Recipient().String() == recipient {
			return identity, nil
		}
	}
	imported, err := s.importedKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range imported {
		if key.Recipient != recipient {
			continue
		}
		plain, err := s.sealer.Open(key.Identity)
		if err != nil {
			return nil, errors.New("an imported backup key cannot be unsealed with this server's master key")
		}
		identity, err := age.ParseX25519Identity(plain)
		if err != nil || identity.Recipient().String() != recipient {
			return nil, errors.New("an imported backup key is malformed")
		}
		return identity, nil
	}
	if own == nil && len(imported) == 0 {
		return nil, &KeyError{Recipient: recipient, err: ErrNoArchiveKey}
	}
	return nil, &KeyError{Recipient: recipient, err: ErrArchiveKey}
}

// HoldsKey says whether this server can open an archive sealed to recipient.
func (s *Store) HoldsKey(ctx context.Context, recipient string) bool {
	_, err := s.identityFor(ctx, recipient)
	return err == nil
}

// ImportedKeyResult is what an import did with the key it was given.
type ImportedKeyResult struct {
	Recipient string `json:"recipient"`
	// Became is "own" when the key became this server's backup key, "opens"
	// when it was added to open archives sealed to it, and "held" when this
	// server already held it.
	Became string `json:"became"`
}

var createdLineRe = regexp.MustCompile(`(?m)^#\s*created:\s*(\S+)\s*$`)

// ImportArchiveKey takes an exported key file (or the AGE-SECRET-KEY line on
// its own) and keeps the one key it holds, sealed with the master key.
func (s *Store) ImportArchiveKey(ctx context.Context, text string) (*ImportedKeyResult, error) {
	if len(text) > 16<<10 {
		return nil, errors.New("a backup key file is a few lines; this is longer than 16 KiB")
	}
	identities, err := age.ParseIdentities(strings.NewReader(text))
	if err != nil {
		return nil, errors.New("this is not a backup key: paste the file Export key… saved, or its AGE-SECRET-KEY-1 line")
	}
	var keys []*age.X25519Identity
	for _, identity := range identities {
		if x, ok := identity.(*age.X25519Identity); ok {
			keys = append(keys, x)
		}
	}
	if len(keys) != 1 {
		return nil, errors.New("paste one backup key at a time")
	}
	identity := keys[0]
	recipient := identity.Recipient().String()

	keyringMu.Lock()
	defer keyringMu.Unlock()
	own, err := s.storedArchiveKey(ctx)
	if err != nil {
		return nil, err
	}
	if own != nil && own.Recipient == recipient {
		return &ImportedKeyResult{Recipient: recipient, Became: "held"}, nil
	}
	sealed, err := s.sealer.Seal(identity.String())
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if own == nil {
		created := now
		if match := createdLineRe.FindStringSubmatch(text); match != nil {
			if at, err := time.Parse(time.RFC3339, match[1]); err == nil {
				created = at.UTC()
			}
		}
		// It came from an exported file, so a copy is already off this
		// server: it is recorded as exported, and the page does not warn
		// that the only copy is here.
		value := encodeJSON(storedArchiveKey{Identity: sealed, Recipient: recipient, CreatedAt: created, ExportedAt: &now})
		result, err := s.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO NOTHING`, archiveKeySetting, value)
		if err != nil {
			return nil, err
		}
		if affected, _ := result.RowsAffected(); affected == 1 {
			return &ImportedKeyResult{Recipient: recipient, Became: "own"}, nil
		}
		// A job made the server's own key in the meantime; this one opens.
	}
	imported, err := s.importedKeys(ctx)
	if err != nil {
		return nil, err
	}
	for _, key := range imported {
		if key.Recipient == recipient {
			return &ImportedKeyResult{Recipient: recipient, Became: "held"}, nil
		}
	}
	if len(imported) >= 64 {
		return nil, errors.New("this server already holds 64 imported backup keys")
	}
	imported = append(imported, importedArchiveKey{Identity: sealed, Recipient: recipient, ImportedAt: now})
	if _, err := s.st.DB.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		importedKeysSetting, encodeJSON(imported)); err != nil {
		return nil, err
	}
	return &ImportedKeyResult{Recipient: recipient, Became: "opens"}, nil
}
