package proxysvc

import "testing"

// The version a proxy binary reports is written for a terminal, not for a tile
// with a label already on it. Storing the line verbatim put "nginx nginx
// version: nginx/1.26.3" under a "Reverse proxy" heading.
func TestParseNginxVersion(t *testing.T) {
	cases := map[string]string{
		"nginx version: nginx/1.26.3 (Ubuntu)\n": "nginx/1.26.3",
		"nginx version: nginx/1.24.0":            "nginx/1.24.0",
		// openresty and tengine answer -v in the same shape, and which one is
		// serving matters when a module is missing.
		"nginx version: openresty/1.21.4.1\n": "openresty/1.21.4.1",
		"nginx version: tengine/2.3.3\n":      "tengine/2.3.3",
		// Unrecognised output is still worth showing.
		"something else entirely\nsecond line": "something else entirely",
		"":                                     "",
	}
	for in, want := range cases {
		if got := parseNginxVersion(in); got != want {
			t.Errorf("parseNginxVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseCaddyVersion(t *testing.T) {
	cases := map[string]string{
		"v2.7.6 h1:w1dLC0KkQvWWyBmDqF9cdRqXY8bGVmtVdKVBFEgFvxs=\n": "v2.7.6",
		"v2.8.4\n":   "v2.8.4",
		"v2.6.2 h1:": "v2.6.2",
		"":           "",
	}
	for in, want := range cases {
		if got := parseCaddyVersion(in); got != want {
			t.Errorf("parseCaddyVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
