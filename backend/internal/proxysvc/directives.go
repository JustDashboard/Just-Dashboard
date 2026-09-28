package proxysvc

import (
	"fmt"
	"slices"
	"strings"
)

// DirectiveEdit is one directive as an edit changed it: the line it starts
// on in the new file, and its text before and after. Before is empty for a
// directive the edit added.
type DirectiveEdit struct {
	Line   int    `json:"line"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// SetDirective gives the simple directive name the one value in content,
// where context names the blocks it sits in within this file, outermost first
// — ["http"] in nginx.conf, nil at the top of a file nginx includes inside
// http.
//
// Only the directive's words change: the text from the end of its name to its
// ";" is replaced, so a comment after the ";" stays where it was, and so does
// every other byte of the file. The file is read with nginx's own tokenizer,
// so a "#" inside quotes or a directive's name inside a comment is not taken
// for either. A comment written between the directive's words would be lost
// by that replacement, so such a directive is refused rather than rewritten.
//
// A directive the file does not set is added as the first line of the block
// context names, indented like the line after it, or at the end of the file
// when context is empty. A directive set twice in the same block is refused:
// nginx refuses most of these as duplicates, and picking one would be a guess.
func SetDirective(content string, context []string, name, value string) (string, DirectiveEdit, error) {
	if value == "" || strings.ContainsAny(value, ";{}\"'#\\\n\r") {
		return "", DirectiveEdit{}, fmt.Errorf("%s: the value may not be empty or hold ; { } # quotes or a line break", name)
	}
	tokens, err := tokenizeNginx("", content)
	if err != nil {
		return "", DirectiveEdit{}, err
	}
	var (
		stack   []string
		words   []nginxToken
		found   [][2]nginxToken
		opening = -1
	)
	for i, tok := range tokens {
		if tok.quoted || (tok.text != ";" && tok.text != "{" && tok.text != "}") {
			words = append(words, tok)
			continue
		}
		switch tok.text {
		case ";":
			if len(words) > 0 && words[0].text == name && !words[0].quoted && slices.Equal(stack, context) {
				found = append(found, [2]nginxToken{words[0], tok})
			}
		case "{":
			if len(words) > 0 {
				stack = append(stack, words[0].text)
				if opening < 0 && len(context) > 0 && slices.Equal(stack, context) {
					opening = i
				}
			}
		case "}":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
		words = nil
	}
	switch {
	case len(found) > 1:
		return "", DirectiveEdit{}, fmt.Errorf("%s is set %d times in the same block here; edit the file by hand", name, len(found))
	case len(found) == 1:
		nameTok, semi := found[0][0], found[0][1]
		if hasCommentBetween(content, tokens, nameTok.end, semi.start) {
			return "", DirectiveEdit{}, fmt.Errorf("%s has a comment between its words; edit the file by hand", name)
		}
		next := content[:nameTok.end] + " " + value + content[semi.start:]
		return next, DirectiveEdit{
			Line:   nameTok.line,
			Before: linesSpanning(content, nameTok.start, semi.end),
			After:  linesSpanning(next, nameTok.start, nameTok.end+1+len(value)+1),
		}, nil
	}
	statement := name + " " + value + ";"
	if len(context) == 0 {
		prefix := content
		if prefix != "" && !strings.HasSuffix(prefix, "\n") {
			prefix += "\n"
		}
		return prefix + statement + "\n", DirectiveEdit{Line: strings.Count(prefix, "\n") + 1, After: statement}, nil
	}
	if opening < 0 {
		return "", DirectiveEdit{}, fmt.Errorf("this file has no %s block to add %s to", strings.Join(context, " "), name)
	}
	brace := tokens[opening]
	at := brace.end
	if nl := strings.IndexByte(content[at:], '\n'); nl >= 0 {
		at += nl + 1
	} else {
		content += "\n"
		at = len(content)
	}
	line := indentOf(content[at:], len(context)) + statement
	next := content[:at] + line + "\n" + content[at:]
	return next, DirectiveEdit{Line: strings.Count(content[:at], "\n") + 1, After: line}, nil
}

// hasCommentBetween reports whether a "#" outside every token sits in
// content[from:to] — a comment nginx skips, which a replacement would drop.
func hasCommentBetween(content string, tokens []nginxToken, from, to int) bool {
	pos := from
	for _, tok := range tokens {
		if tok.end <= from || tok.start >= to {
			continue
		}
		if strings.Contains(content[pos:tok.start], "#") {
			return true
		}
		pos = tok.end
	}
	return strings.Contains(content[pos:to], "#")
}

// linesSpanning is the whole lines content[from:to] touches.
func linesSpanning(content string, from, to int) string {
	start := strings.LastIndexByte(content[:from], '\n') + 1
	end := len(content)
	if nl := strings.IndexByte(content[to:], '\n'); nl >= 0 {
		end = to + nl
	}
	return content[start:end]
}

// indentOf is the indentation of the first line of rest that holds anything,
// or four spaces a level when the block is empty.
func indentOf(rest string, depth int) string {
	for _, line := range strings.Split(rest, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && !strings.HasPrefix(trimmed, "}") {
			return line[:len(line)-len(trimmed)]
		}
	}
	return strings.Repeat("    ", depth)
}
