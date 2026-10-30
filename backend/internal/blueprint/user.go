package blueprint

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// UserPrefix is the namespace of every template an operator adds. A user
// template's id must carry it and a built-in's must not (loadCatalog refuses
// one that does), so the two can never name the same definition: a template
// somebody saved cannot stand in for a reviewed one, and a built-in shipped
// later cannot take the place of a template somebody saved.
const UserPrefix = "user-"

// Where a definition in a listing comes from: shipped with the dashboard, or
// added on it.
const (
	OriginBuiltIn = "builtin"
	OriginUser    = "user"
)

// MaxUserTemplateBytes bounds a stored definition. The largest built-in is a
// few kilobytes; a definition this size is a payload, not a template.
const MaxUserTemplateBytes = 256 << 10

// IsUserID says whether an id names a template added on this dashboard.
func IsUserID(id string) bool { return strings.HasPrefix(id, UserPrefix) }

var slugRunRE = regexp.MustCompile(`[^a-z0-9]+`)

// UserID is the id a template the operator names is stored under: the name as
// a slug, in the user namespace. "user-" typed by the operator is not doubled.
func UserID(name string) (string, error) {
	slug := strings.Trim(slugRunRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	slug = strings.TrimPrefix(slug, UserPrefix)
	if limit := 64 - len(UserPrefix); len(slug) > limit {
		slug = strings.TrimRight(slug[:limit], "-")
	}
	id := UserPrefix + slug
	if slug == "" || !identifierRE.MatchString(id) {
		return "", fmt.Errorf("%w: %q makes no template id; use letters or digits", ErrInvalidBlueprint, name)
	}
	return id, nil
}

// ValidateUserTemplate holds a definition the operator added to every rule a
// built-in is held to — Validate, which a built-in meets when the catalogue
// loads, and the ones the built-in test suite adds: every fixture renders to
// the digest it records, and no input ships a default for a credential — and
// to the few only a definition that did not ship with the dashboard needs.
func ValidateUserTemplate(definition *Blueprint) error {
	if err := Validate(definition); err != nil {
		return err
	}
	return validateUserRules(definition, true)
}

// validateUserRules is what a user template answers beyond Validate. The
// fixture renders are left to the writes (fixtures true): a template is read
// on every source save and run, and what it rendered to was checked when it
// was stored.
func validateUserRules(definition *Blueprint, fixtures bool) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrInvalidBlueprint, definition.ID, fmt.Sprintf(format, args...))
	}
	if !IsUserID(definition.ID) {
		return fail("a template added here is named %s<name>, which is how it can never stand in for a built-in", UserPrefix)
	}
	if definition.Retired != "" {
		return fail("a template added here is deleted rather than retired")
	}
	if definition.Profile == ProfileGame || definition.Category == CategoryGame {
		// The console, players and server settings pages are written for the
		// two reviewed Minecraft definitions and find them by id.
		return fail("game servers are the reviewed Minecraft templates; a template added here cannot be one")
	}
	// Only a reviewed built-in may reach the host. A template added here can
	// come from a file somebody downloaded, and importing it must not be how
	// a deployment gets a privileged container or the Docker socket: one that
	// needs either sets it under its own Runtime settings, where it is shown.
	security := definition.Security
	if security.Privileged || security.HostNetwork || len(security.Capabilities) > 0 || len(security.Devices) > 0 {
		return fail("a template added here cannot take privileges, the host's network, capabilities or devices; only a reviewed built-in may")
	}
	for _, volume := range definition.Volumes {
		if strings.HasPrefix(volume.Target, "/var/run/docker") || volume.Target == "/run/docker.sock" {
			return fail("a template added here cannot mount the Docker socket; only a reviewed built-in may")
		}
	}
	// The built-in suite's rule, which reads the input's name as well as its
	// variable: a default for either is a password every deployment shares.
	for _, input := range definition.Inputs {
		if input.Default != "" && looksLikeSecret(input.Variable+" "+strings.ReplaceAll(input.Name, "-", "_")) {
			return fail("input %q ships a default for a credential", input.Name)
		}
	}
	if !fixtures {
		return nil
	}
	for _, fixture := range definition.Fixtures {
		plan, err := Render(definition, fixture.Inputs)
		if err != nil {
			return fail("fixture %q does not render: %v", fixture.Name, err)
		}
		if fixture.Digest != plan.Digest {
			return fail("fixture %q renders to %s, and the definition records %q", fixture.Name, plan.Digest, fixture.Digest)
		}
	}
	return nil
}

// PrepareUserTemplate records what each fixture renders to and returns the
// definition as it is stored. A built-in's digests are written by its
// reviewer and checked by CI; a template added here is checked the moment it
// is saved instead, so its digests are the server's to write. A fixture that
// does not render refuses the whole definition, as it fails CI for a built-in.
func PrepareUserTemplate(definition *Blueprint) ([]byte, error) {
	if err := Validate(definition); err != nil {
		return nil, err
	}
	if err := validateUserRules(definition, false); err != nil {
		return nil, err
	}
	for index := range definition.Fixtures {
		fixture := &definition.Fixtures[index]
		plan, err := Render(definition, fixture.Inputs)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: fixture %q does not render: %v", ErrInvalidBlueprint, definition.ID, fixture.Name, err)
		}
		fixture.Digest = plan.Digest
	}
	if err := ValidateUserTemplate(definition); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(definition, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxUserTemplateBytes {
		return nil, fmt.Errorf("%w: %s: the definition is %d KiB; a template is at most %d KiB", ErrInvalidBlueprint, definition.ID, len(encoded)>>10, MaxUserTemplateBytes>>10)
	}
	// What is stored is read back by ParseUserTemplate on every source save
	// and run; a definition that does not survive the trip is refused now.
	if _, err := ParseUserTemplate(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

// DecodeUserTemplate reads a definition being imported or edited, with
// unknown fields refused, and puts its id in the user namespace: an exported
// built-in imported as it is becomes a template of this dashboard's own named
// after it, never a second definition under the built-in's id. The rules are
// PrepareUserTemplate's to apply once the caller has settled the id.
func DecodeUserTemplate(raw []byte) (*Blueprint, error) {
	if len(raw) > MaxUserTemplateBytes {
		return nil, fmt.Errorf("%w: a template is at most %d KiB", ErrInvalidBlueprint, MaxUserTemplateBytes>>10)
	}
	decoded, err := decodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	if decoded.ID != "" && !IsUserID(decoded.ID) {
		id, err := UserID(decoded.ID)
		if err != nil {
			return nil, err
		}
		decoded.ID = id
	}
	return decoded, nil
}

// ParseUserTemplate reads a definition added on this dashboard: unknown fields
// refused, Validate applied, and the user namespace and its rules held.
func ParseUserTemplate(raw []byte) (*Blueprint, error) {
	if len(raw) > MaxUserTemplateBytes {
		return nil, fmt.Errorf("%w: a template is at most %d KiB", ErrInvalidBlueprint, MaxUserTemplateBytes>>10)
	}
	parsed, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := validateUserRules(parsed, false); err != nil {
		return nil, err
	}
	return parsed, nil
}
