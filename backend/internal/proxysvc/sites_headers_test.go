package proxysvc

import (
	"strings"
	"testing"
)

func TestHeaderValueKeepsItsLimitWithoutARegExpRepeatOverThousand(t *testing.T) {
	if !validHeaderValue(strings.Repeat("a", 1024)) {
		t.Fatal("a value at the documented limit was refused")
	}
	if !validHeaderValue(strings.Repeat("é", 1024)) {
		t.Fatal("a Unicode value at the character limit was refused")
	}
	if validHeaderValue(strings.Repeat("a", 1025)) {
		t.Fatal("a value past the documented limit was accepted")
	}
	for _, value := range []string{"a\nb", `a"b`, `a$b`, `a;b`, `a\\b`} {
		if validHeaderValue(value) {
			t.Errorf("an unsafe header value was accepted: %q", value)
		}
	}
}
