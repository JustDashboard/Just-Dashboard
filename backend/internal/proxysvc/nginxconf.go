package proxysvc

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Directive is one statement of an nginx configuration.
//
// ParseSiteSpec reads the files the site form writes, line by line; this
// reads any file the way nginx does, token by token, so a question about the
// whole configuration — which server blocks listen where, what an upstream
// is called, whether a zone name is taken — is answered from what nginx will
// actually see rather than from what a regular expression happened to match.
type Directive struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	File string   `json:"file"`
	Line int      `json:"line"`
	// Context names the blocks this directive sits in, outermost first:
	// ["http", "server", "location"]. Empty at the top of the main file.
	Context []string `json:"context"`
	// Block is nil for a simple directive and non-nil, possibly empty, for
	// one that opens a block.
	Block []Directive `json:"block"`
}

type nginxToken struct {
	text string
	line int
	// quoted marks a token written in quotes, where ";" and braces are text.
	quoted bool
}

// tokenizeNginx splits a file the way ngx_conf_read_token does: words break
// at whitespace, ";" and "{"; "#" opens a comment only where a word could
// start; quotes group, and inside them `\"`, `\'` and `\\` are the character
// and `\t`, `\r`, `\n` are control characters. A "{" straight after "$" is
// part of a ${variable} rather than a block, and "}" ends a word only where a
// new one could start, which is what nginx does with both.
func tokenizeNginx(file, content string) ([]nginxToken, error) {
	var tokens []nginxToken
	line := 1
	for i := 0; i < len(content); {
		c := content[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < len(content) && content[i] != '\n' {
				i++
			}
		case c == ';' || c == '{' || c == '}':
			tokens = append(tokens, nginxToken{text: string(c), line: line})
			i++
		case c == '"' || c == '\'':
			start := line
			j := i + 1
			for j < len(content) && content[j] != c {
				if content[j] == '\\' && j+1 < len(content) {
					j++
				}
				if content[j] == '\n' {
					line++
				}
				j++
			}
			if j >= len(content) {
				return nil, fmt.Errorf("unexpected end of file, expecting %q in %s:%d", c, file, start)
			}
			tokens = append(tokens, nginxToken{text: unescapeNginx(content[i+1 : j]), line: start, quoted: true})
			i = j + 1
		default:
			j := i
			variable := false
			for j < len(content) {
				ch := content[j]
				if ch == '{' && variable {
					j++
					continue
				}
				variable = false
				if ch == '\\' && j+1 < len(content) {
					if content[j+1] == '\n' {
						line++
					}
					j += 2
					continue
				}
				if ch == '$' {
					variable = true
				} else if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == ';' || ch == '{' {
					break
				}
				j++
			}
			tokens = append(tokens, nginxToken{text: unescapeNginx(content[i:j]), line: line})
			i = j
		}
	}
	return tokens, nil
}

// unescapeNginx applies nginx's escapes to a token's text. Any other
// backslash is kept, which is what keeps `\.php$` a regular expression.
func unescapeNginx(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) {
			switch raw[i+1] {
			case '"', '\'', '\\':
				b.WriteByte(raw[i+1])
				i++
				continue
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				i++
				continue
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			}
		}
		b.WriteByte(raw[i])
	}
	return b.String()
}

// ParseNginxFile reads one file into directives, as if it sat inside the
// blocks named by context. The errors say where, the way nginx does.
func ParseNginxFile(path, content string, context []string) ([]Directive, error) {
	tokens, err := tokenizeNginx(path, content)
	if err != nil {
		return nil, err
	}
	pos := 0
	directives, err := parseNginxBlock(path, tokens, &pos, context, false)
	if err != nil {
		return nil, err
	}
	return directives, nil
}

func parseNginxBlock(path string, tokens []nginxToken, pos *int, context []string, inBlock bool) ([]Directive, error) {
	out := []Directive{}
	var words []nginxToken
	for *pos < len(tokens) {
		tok := tokens[*pos]
		*pos++
		if tok.quoted || (tok.text != ";" && tok.text != "{" && tok.text != "}") {
			words = append(words, tok)
			continue
		}
		switch tok.text {
		case ";":
			if len(words) == 0 {
				return nil, fmt.Errorf("unexpected \";\" in %s:%d", path, tok.line)
			}
			out = append(out, newDirective(path, words, context))
		case "{":
			if len(words) == 0 {
				return nil, fmt.Errorf("unexpected \"{\" in %s:%d", path, tok.line)
			}
			d := newDirective(path, words, context)
			inner := append(append([]string{}, context...), d.Name)
			block, err := parseNginxBlock(path, tokens, pos, inner, true)
			if err != nil {
				return nil, err
			}
			d.Block = block
			out = append(out, d)
		case "}":
			if len(words) > 0 || !inBlock {
				return nil, fmt.Errorf("unexpected \"}\" in %s:%d", path, tok.line)
			}
			return out, nil
		}
		words = nil
	}
	if len(words) > 0 {
		return nil, fmt.Errorf("unexpected end of file, expecting \";\" or \"}\" in %s:%d", path, words[len(words)-1].line)
	}
	if inBlock {
		return nil, fmt.Errorf("unexpected end of file, expecting \"}\" in %s", path)
	}
	return out, nil
}

func newDirective(path string, words []nginxToken, context []string) Directive {
	d := Directive{
		Name: words[0].text, Args: []string{}, File: path, Line: words[0].line,
		Context: append([]string{}, context...),
	}
	for _, w := range words[1:] {
		d.Args = append(d.Args, w.text)
	}
	return d
}

// NginxTree reads the files `nginx -T` printed into one tree, rooted at the
// first of them — the main configuration — with every include replaced by the
// directives of the files it matched, in the order nginx reads them, each
// keeping its own file and line.
//
// A relative include is resolved against the main file's directory, which is
// where nginx resolves it. A glob is matched the way glob(3) matches for
// nginx: sorted, and without leading-dot names unless the pattern asks for
// them — the reason a `conf.d/.site.conf` is not read. Only files nginx
// printed can match, since those are the ones it read.
func NginxTree(files []ConfigFile) ([]Directive, error) {
	if len(files) == 0 {
		return []Directive{}, nil
	}
	w := includeWalker{files: files, prefix: filepath.Dir(files[0].Path), open: map[string]bool{}}
	return w.file(files[0], nil)
}

type includeWalker struct {
	files  []ConfigFile
	prefix string
	// open is the chain of files being expanded, so a file that includes
	// itself is read once rather than for ever.
	open map[string]bool
}

func (w *includeWalker) file(f ConfigFile, context []string) ([]Directive, error) {
	directives, err := ParseNginxFile(f.Path, f.Content, context)
	if err != nil {
		return nil, err
	}
	w.open[f.Path] = true
	defer delete(w.open, f.Path)
	return w.expand(directives)
}

func (w *includeWalker) expand(directives []Directive) ([]Directive, error) {
	out := make([]Directive, 0, len(directives))
	for _, d := range directives {
		if d.Name == "include" && d.Block == nil && len(d.Args) == 1 {
			for _, f := range w.match(d.Args[0]) {
				if w.open[f.Path] {
					continue
				}
				included, err := w.file(f, d.Context)
				if err != nil {
					return nil, err
				}
				out = append(out, included...)
			}
			continue
		}
		if d.Block != nil {
			block, err := w.expand(d.Block)
			if err != nil {
				return nil, err
			}
			d.Block = block
		}
		out = append(out, d)
	}
	return out, nil
}

func (w *includeWalker) match(pattern string) []ConfigFile {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(w.prefix, pattern)
	}
	literal := !strings.ContainsAny(pattern, "*?[")
	hidden := strings.HasPrefix(filepath.Base(pattern), ".")
	seen := map[string]bool{}
	var out []ConfigFile
	for _, f := range w.files {
		if seen[f.Path] {
			continue
		}
		if literal {
			if f.Path != pattern {
				continue
			}
		} else if ok, _ := filepath.Match(pattern, f.Path); !ok ||
			(!hidden && strings.HasPrefix(filepath.Base(f.Path), ".")) {
			continue
		}
		seen[f.Path] = true
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
