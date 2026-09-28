package proxysvc

import (
	"context"
	"regexp"
	"strings"
)

// DirectiveUse is one place a directive is set in the configuration nginx
// loads, with the server it sits in when it sits in one.
type DirectiveUse struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Value   string `json:"value"`
	Context string `json:"context"`
	// Server is the enclosing server block's server_name, so a reader can
	// tell which of several ssl_protocols lines is the scanned site's.
	Server []string `json:"server,omitempty"`
}

var directiveNameRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,63}$`)

// IsDirectiveName reports whether name could be an nginx directive, which is
// all a lookup needs to refuse anything else before running nginx -T.
func IsDirectiveName(name string) bool {
	return directiveNameRe.MatchString(name)
}

// FindDirective answers "where is this set": every place name appears in the
// loaded configuration, in the order nginx reads them. A finding such as
// "TLS 1.0 is still offered" is caused by a line somewhere, and on a host
// with an http-level default, a site override and a snippet the page cannot
// guess which one wins without showing all three.
//
// Only files EffectiveConfig returns are searched, so a directive in a file
// outside the proxy's directory — certbot's options-ssl-nginx.conf is the
// usual one — is not found, and the caller says so.
func (s *Service) FindDirective(ctx context.Context, name string) ([]DirectiveUse, error) {
	files, err := s.EffectiveConfig(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := NginxTree(files)
	if err != nil {
		return nil, err
	}
	return directiveUses(tree, name, nil), nil
}

func directiveUses(directives []Directive, name string, server []string) []DirectiveUse {
	out := []DirectiveUse{}
	for _, d := range directives {
		if d.Name == name {
			out = append(out, DirectiveUse{
				Path: d.File, Line: d.Line, Value: strings.Join(d.Args, " "),
				Context: strings.Join(d.Context, " › "), Server: server,
			})
		}
		if d.Block == nil {
			continue
		}
		inner := server
		if d.Name == "server" {
			inner = nil
			for _, c := range d.Block {
				if c.Name == "server_name" {
					inner = append(inner, c.Args...)
				}
			}
		}
		out = append(out, directiveUses(d.Block, name, inner)...)
	}
	return out
}
