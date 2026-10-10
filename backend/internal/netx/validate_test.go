package netx

import "testing"

func TestMappedIPv4PrefixesKeepTheirWidthAfterUnmapping(t *testing.T) {
	for input, want := range map[string]string{
		"::ffff:192.168.5.7/120": "192.168.5.7/24",
		"::ffff:192.168.5.7/128": "192.168.5.7/32",
		"::ffff:192.168.5.7/96":  "192.168.5.7/0",
		"::ffff:192.168.5.7":     "192.168.5.7/32",
	} {
		got, err := ParsePrefix(input)
		if err != nil || !got.IsValid() || got.String() != want {
			t.Errorf("ParsePrefix(%q) = %v, %v; want %s", input, got, err, want)
		}
	}
	if _, err := ParsePrefix("::ffff:192.168.5.7/64"); err == nil {
		t.Fatal("an IPv6 network wider than the mapped range became an invalid IPv4 prefix")
	}
}
