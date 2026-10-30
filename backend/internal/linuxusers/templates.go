package linuxusers

import (
	"fmt"
	"regexp"
	"strings"
)

// Template is a shape for a new account: what it is for, how much of root it has
// and which groups it starts in. It is never a credential.
type Template struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Builtin bool     `json:"builtin"`
	Shell   string   `json:"shell"`
	Groups  []string `json:"groups"`
	// Sudo is the level the account starts with: none, password or nopassword.
	Sudo   string `json:"sudo"`
	System bool   `json:"system"`
	NoHome bool   `json:"noHome"`
}

// BuiltinTemplates are the accounts people actually make on one server. Only
// groups that already exist on the host take effect; the form says which do.
func BuiltinTemplates() []Template {
	return []Template{
		{ID: "person", Name: "Person", Builtin: true, Shell: "/bin/bash", Groups: []string{}, Sudo: SudoNone},
		{ID: "administrator", Name: "Administrator", Builtin: true, Shell: "/bin/bash", Groups: []string{}, Sudo: SudoPassword},
		{ID: "developer", Name: "Developer", Builtin: true, Shell: "/bin/bash", Groups: []string{"docker"}, Sudo: SudoNone},
		{ID: "deploy", Name: "Deploy bot", Builtin: true, Shell: "/bin/bash", Groups: []string{}, Sudo: SudoNone},
		{ID: "service", Name: "Service", Builtin: true, Shell: "/usr/sbin/nologin", Groups: []string{}, Sudo: SudoNone, System: true, NoHome: true},
	}
}

var templateIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// CleanTemplates checks the operator's own and marks them as theirs. An ID that
// collides with a built-in would make one of the two unreachable.
func CleanTemplates(in []Template) ([]Template, error) {
	builtin := map[string]bool{}
	for _, template := range BuiltinTemplates() {
		builtin[template.ID] = true
	}
	seen := map[string]bool{}
	out := make([]Template, 0, len(in))
	for _, template := range in {
		template.Name = strings.TrimSpace(template.Name)
		if template.Name == "" || len(template.Name) > 64 {
			return nil, fmt.Errorf("a template needs a name of up to 64 characters")
		}
		if !templateIDRe.MatchString(template.ID) || builtin[template.ID] || seen[template.ID] {
			return nil, fmt.Errorf("%q cannot be used as a template id", template.ID)
		}
		seen[template.ID] = true
		template.Builtin = false
		if template.Groups == nil {
			template.Groups = []string{}
		}
		for _, group := range template.Groups {
			if err := ValidateUsername(group); err != nil {
				return nil, fmt.Errorf("invalid group %q", group)
			}
		}
		switch template.Sudo {
		case "":
			template.Sudo = SudoNone
		case SudoNone, SudoPassword, SudoNoPassword:
		default:
			return nil, fmt.Errorf("sudo level must be none, password or nopassword")
		}
		out = append(out, template)
	}
	return out, nil
}
