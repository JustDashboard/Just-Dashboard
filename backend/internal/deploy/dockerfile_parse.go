package deploy

import (
	"encoding/json"
	"regexp"
	"strings"
)

// A Dockerfile is read here the way BuildKit reads it — parser directives,
// line continuations, heredoc bodies — and never executed. Every check that
// used to split the file on newlines judged the first physical line of an
// instruction, so a `\` continuation or a heredoc could carry what the
// checker was looking for straight past it, while a harmless line of the
// Rails template was refused.

type dockerfileInstruction struct {
	// Line is the 1-based physical line the instruction starts on.
	Line    int
	Keyword string
	// Flags are the leading `--name[=value]` options, verbatim.
	Flags []string
	// Args is the instruction's text after the flags, continuations joined.
	Args string
	// JSON holds the exec form's elements when Args is a JSON string array.
	JSON     []string
	IsJSON   bool
	Heredocs []dockerfileHeredoc
}

type dockerfileHeredoc struct {
	Name string
	Body string
	Line int
}

type parsedDockerfile struct {
	Escape       byte
	Instructions []dockerfileInstruction
}

var (
	dockerfileDirectiveRE = regexp.MustCompile(`^#\s*([A-Za-z]+)\s*=\s*(\S+)\s*$`)
	dockerfileHeredocRE   = regexp.MustCompile(`<<(-?)(["']?)([A-Za-z_][A-Za-z0-9_]*)(["']?)`)
)

func parseDockerfile(content []byte) parsedDockerfile {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	parsed := parsedDockerfile{Escape: '\\'}
	index := 0
	// Parser directives only count before the first comment, blank line or
	// instruction, exactly as BuildKit stops looking for them.
	for ; index < len(lines); index++ {
		match := dockerfileDirectiveRE.FindStringSubmatch(strings.TrimSpace(lines[index]))
		if match == nil {
			break
		}
		if strings.EqualFold(match[1], "escape") && (match[2] == "`" || match[2] == `\`) {
			parsed.Escape = match[2][0]
		}
	}
	for index < len(lines) {
		raw := strings.TrimSpace(lines[index])
		if raw == "" || strings.HasPrefix(raw, "#") {
			index++
			continue
		}
		start := index + 1
		logical := strings.TrimRight(strings.TrimLeft(lines[index], " \t"), " \t")
		index++
		for dockerfileContinues(logical, parsed.Escape) && index < len(lines) {
			logical = strings.TrimRight(logical[:len(logical)-1], " \t")
			for index < len(lines) {
				next := strings.TrimSpace(lines[index])
				// Comment and empty lines inside a continuation are dropped by
				// BuildKit, so they must not end the instruction here either.
				if next == "" || strings.HasPrefix(next, "#") {
					index++
					continue
				}
				break
			}
			if index >= len(lines) {
				break
			}
			logical += " " + strings.TrimRight(strings.TrimLeft(lines[index], " \t"), " \t")
			index++
		}
		keyword, rest, _ := strings.Cut(logical, " ")
		if tab := strings.IndexByte(keyword, '\t'); tab >= 0 {
			keyword, rest = keyword[:tab], keyword[tab+1:]+" "+rest
		}
		instruction := dockerfileInstruction{Line: start, Keyword: strings.ToUpper(keyword)}
		rest = strings.TrimSpace(rest)
		for strings.HasPrefix(rest, "--") {
			flag, remainder, _ := strings.Cut(rest, " ")
			instruction.Flags = append(instruction.Flags, flag)
			rest = strings.TrimSpace(remainder)
		}
		instruction.Args = rest
		if strings.HasPrefix(rest, "[") {
			var elements []string
			if json.Unmarshal([]byte(rest), &elements) == nil {
				instruction.JSON, instruction.IsJSON = elements, true
			}
		}
		switch instruction.Keyword {
		case "RUN", "COPY", "ADD":
			if instruction.IsJSON {
				break
			}
			for _, position := range dockerfileHeredocRE.FindAllStringSubmatchIndex(rest, -1) {
				match := make([]string, 5)
				for group := range match {
					if position[2*group] >= 0 {
						match[group] = rest[position[2*group]:position[2*group+1]]
					}
				}
				// `<<<` is a here-string, and mismatched quotes are not a marker.
				if (position[0] > 0 && rest[position[0]-1] == '<') || match[2] != match[4] {
					continue
				}
				heredoc := dockerfileHeredoc{Name: match[3], Line: index + 1}
				body := []string{}
				for index < len(lines) {
					line := lines[index]
					index++
					candidate := line
					if match[1] == "-" {
						candidate = strings.TrimLeft(line, "\t")
					}
					if strings.TrimRight(candidate, " \t") == match[3] {
						break
					}
					body = append(body, line)
				}
				heredoc.Body = strings.Join(body, "\n")
				instruction.Heredocs = append(instruction.Heredocs, heredoc)
			}
		}
		parsed.Instructions = append(parsed.Instructions, instruction)
	}
	return parsed
}

func dockerfileContinues(line string, escape byte) bool {
	return line != "" && line[len(line)-1] == escape
}

// flag returns an instruction's `--name=value` option, if it carries one.
func (i dockerfileInstruction) flag(name string) (string, bool) {
	for _, flag := range i.Flags {
		key, value, hasValue := strings.Cut(strings.TrimPrefix(flag, "--"), "=")
		if strings.EqualFold(key, name) {
			if !hasValue {
				return "", true
			}
			return value, true
		}
	}
	return "", false
}

// shellWords splits text the way a POSIX shell splits words, closely enough
// to read assignments and arguments as data: quotes group, the escape
// character protects the next byte, and nothing is expanded.
func shellWords(text string, escape byte) []string {
	words := []string{}
	var current strings.Builder
	inWord := false
	quote := byte(0)
	for index := 0; index < len(text); index++ {
		char := text[index]
		switch {
		case quote == '\'':
			if char == '\'' {
				quote = 0
			} else {
				current.WriteByte(char)
			}
		case quote == '"':
			switch {
			case char == '"':
				quote = 0
			case char == escape && index+1 < len(text) && strings.IndexByte("\"$`\\", text[index+1]) >= 0:
				index++
				current.WriteByte(text[index])
			default:
				current.WriteByte(char)
			}
		case char == '\'' || char == '"':
			quote, inWord = char, true
		case char == escape && index+1 < len(text):
			index++
			current.WriteByte(text[index])
			inWord = true
		case char == ' ' || char == '\t' || char == '\n':
			if inWord {
				words = append(words, current.String())
				current.Reset()
				inWord = false
			}
		default:
			current.WriteByte(char)
			inWord = true
		}
	}
	if inWord {
		words = append(words, current.String())
	}
	return words
}

// dockerfileKeyValues reads ENV and ARG pairs: `NAME=value NAME2="v 2"`, and
// ENV's legacy `NAME value with spaces`. A name without `=` is an ARG with no
// default.
func dockerfileKeyValues(instruction dockerfileInstruction, escape byte) []dockerfileKeyValue {
	words := shellWords(instruction.Args, escape)
	if len(words) == 0 {
		return nil
	}
	if instruction.Keyword == "ENV" && !strings.Contains(words[0], "=") {
		name := words[0]
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(instruction.Args), name))
		values := shellWords(value, escape)
		return []dockerfileKeyValue{{Name: name, Value: strings.Join(values, " "), HasValue: true}}
	}
	pairs := make([]dockerfileKeyValue, 0, len(words))
	for _, word := range words {
		name, value, hasValue := strings.Cut(word, "=")
		pairs = append(pairs, dockerfileKeyValue{Name: name, Value: value, HasValue: hasValue})
	}
	return pairs
}

type dockerfileKeyValue struct {
	Name     string
	Value    string
	HasValue bool
}

// commandWords is an instruction's command as words: the exec form's
// elements, or the shell form split on whitespace and quotes.
func (i dockerfileInstruction) commandWords(escape byte) []string {
	if i.IsJSON {
		return i.JSON
	}
	return shellWords(i.Args, escape)
}
