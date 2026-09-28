package deploy

import (
	"fmt"
	"regexp"
	"strings"
)

// dockerfileCredential is where a Dockerfile would bake a literal credential
// into an image layer or a process's argv. It names the line, the instruction
// and the variable or flag — never the value, and never as `NAME=`, because
// this text travels through detection evidence that refuses assignments.
type dockerfileCredential struct {
	Line    int
	Keyword string
	Name    string
	Reason  string
}

func (c dockerfileCredential) String() string {
	if c.Name == "" {
		return fmt.Sprintf("line %d (%s) %s", c.Line, c.Keyword, c.Reason)
	}
	return fmt.Sprintf("line %d (%s) %s %s", c.Line, c.Keyword, c.Reason, c.Name)
}

// validateCustomDockerfile refuses a Dockerfile only for what would really
// put a credential in an image or argv: a private key block, a URL carrying
// credentials, or a secret-named variable or flag given a literal value.
//
// The old rule refused any secret-shaped name followed by `=`, which is how
// every Rails 7.1+ application's generated Dockerfile precompiles assets
// (`SECRET_KEY_BASE_DUMMY=1`), how Django Dockerfiles run collectstatic, and
// how ML images set TOKENIZERS_PARALLELISM — and it said so only after the
// operator pressed Deploy.
func validateCustomDockerfile(content []byte) error {
	if issue, found := dockerfileCredentialIssue(content); found {
		return fmt.Errorf("%w: custom Dockerfile %s; move the value into a runtime variable", ErrUnsupportedBuilder, issue)
	}
	return nil
}

func dockerfileCredentialIssue(content []byte) (dockerfileCredential, bool) {
	parsed := parseDockerfile(content)
	for _, instruction := range parsed.Instructions {
		texts := []string{instruction.Args}
		for _, heredoc := range instruction.Heredocs {
			texts = append(texts, heredoc.Body)
		}
		for _, text := range texts {
			lower := strings.ToLower(text)
			if strings.Contains(lower, "-----begin") && strings.Contains(lower, "private key-----") {
				return dockerfileCredential{Line: instruction.Line, Keyword: instruction.Keyword, Reason: "embeds a private key"}, true
			}
			if containsURLCredentials(text) {
				return dockerfileCredential{Line: instruction.Line, Keyword: instruction.Keyword, Reason: "embeds a URL with credentials"}, true
			}
		}
		switch instruction.Keyword {
		case "ENV", "ARG":
			for _, pair := range dockerfileKeyValues(instruction, parsed.Escape) {
				if pair.HasValue && dockerfileLiteralCredential(pair.Name, pair.Value) {
					return dockerfileCredential{Line: instruction.Line, Keyword: instruction.Keyword, Name: pair.Name, Reason: "sets a literal value for"}, true
				}
			}
		case "RUN", "CMD", "ENTRYPOINT", "HEALTHCHECK":
			if name, found := commandLiteralCredential(instruction.commandWords(parsed.Escape)); found {
				return dockerfileCredential{Line: instruction.Line, Keyword: instruction.Keyword, Name: name, Reason: "passes a literal value for"}, true
			}
		}
		// A heredoc body is a script BuildKit runs, or a file it writes into
		// the image; either way an assignment in it is the same assignment.
		for _, heredoc := range instruction.Heredocs {
			for _, line := range strings.Split(heredoc.Body, "\n") {
				if name, found := commandLiteralCredential(shellWords(line, parsed.Escape)); found {
					return dockerfileCredential{Line: heredoc.Line, Keyword: instruction.Keyword, Name: name, Reason: "writes a literal value for"}, true
				}
			}
		}
	}
	return dockerfileCredential{}, false
}

var shellAssignmentRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// commandLiteralCredential finds a secret-named shell assignment or flag
// given a literal: `API_KEY=sk_live_… ./x`, `--token=abc`, `--password abc`.
func commandLiteralCredential(words []string) (string, bool) {
	for index, word := range words {
		candidate := strings.TrimLeft(word, "({")
		if strings.HasPrefix(candidate, "-") {
			flag, value, hasValue := strings.Cut(candidate, "=")
			name := strings.TrimLeft(flag, "-")
			if dockerfileSecretName(strings.ReplaceAll(name, "-", "_")) {
				if !hasValue {
					if index+1 >= len(words) || strings.HasPrefix(words[index+1], "-") {
						continue
					}
					value = words[index+1]
				}
				if dockerfileLiteralValue(name, value) {
					return flag, true
				}
				continue
			}
			// `--build-arg=NPM_TOKEN=abc` hides an assignment behind a flag.
			if hasValue {
				candidate = value
			} else {
				continue
			}
		}
		name, value, hasValue := strings.Cut(candidate, "=")
		if !hasValue || !shellAssignmentRE.MatchString(name) {
			continue
		}
		if dockerfileLiteralCredential(name, strings.TrimRight(value, ";&|)}")) {
			return name, true
		}
	}
	return "", false
}

func dockerfileLiteralCredential(name, value string) bool {
	return dockerfileSecretName(name) && dockerfileLiteralValue(name, value)
}

// dockerfileSecretName is the same name set the rest of planning treats as
// credential-shaped, minus the configuration names that merely contain one
// of its words: a dummy switch, a tokenizer thread count, a token budget.
func dockerfileSecretName(name string) bool {
	upper := strings.ToUpper(name)
	lower := strings.ToLower(name)
	if !secretShapedKey(name) && !strings.Contains(lower, "apikey") {
		return false
	}
	for _, suffix := range []string{"_DUMMY", "_PARALLELISM", "_TOKENS", "_TOKEN_LIMIT", "_TTL", "_LENGTH"} {
		if strings.HasSuffix(upper, suffix) {
			return false
		}
	}
	return upper != "MAX_TOKENS"
}

// dockerfilePlaceholders are the words Dockerfiles give a secret they need
// only to satisfy a boot-time check during a build step.
var dockerfilePlaceholders = map[string]bool{
	"dummy": true, "placeholder": true, "precompile": true, "changeme": true, "build": true,
	"build-only": true, "unused": true, "insecure": true, "not-a-secret": true, "none": true,
	"test": true, "x": true,
}

var dockerfileFlagValues = map[string]bool{
	"0": true, "1": true, "true": true, "false": true, "yes": true, "no": true, "on": true, "off": true,
}

var (
	dockerfileReferenceRE = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*\})$`)
	dockerfileDefaultRE   = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*:?[-=]([^}]*)\}$`)
	dockerfileNumberRE    = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)
)

// dockerfileLiteralValue says whether a value is real credential material
// rather than an empty value, a reference to a build argument (custom
// Dockerfiles receive none, and the argument's own default is checked on its
// own line), a command substitution, a switch, a number, a closed set of
// placeholders, or a secret file's path.
func dockerfileLiteralValue(name, value string) bool {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		value = value[1 : len(value)-1]
	}
	lower := strings.ToLower(value)
	switch {
	case value == "":
		return false
	case dockerfileReferenceRE.MatchString(value):
		return false
	case strings.HasPrefix(value, "$(") || strings.HasPrefix(value, "`"):
		return false
	case dockerfileFlagValues[lower] || dockerfilePlaceholders[lower]:
		return false
	case strings.HasSuffix(strings.ToUpper(name), "_FILE") && strings.HasPrefix(value, "/"):
		return false
	}
	if dockerfileNumberRE.MatchString(value) {
		return false
	}
	if match := dockerfileDefaultRE.FindStringSubmatch(value); match != nil {
		return dockerfileLiteralValue(name, match[1])
	}
	return true
}
