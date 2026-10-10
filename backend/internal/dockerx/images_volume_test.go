package dockerx

import "testing"

// The list says whether a volume has driver options — which is what keeps it
// out of a prune — without carrying the options, whose `o=` can hold a CIFS
// share's password.
func TestOptionMountNamesOptionsWithoutCarryingThem(t *testing.T) {
	cases := []struct {
		options map[string]string
		want    string
	}{
		{nil, ""},
		{map[string]string{}, ""},
		{map[string]string{"type": "nfs", "o": "addr=10.0.0.2,rw", "device": ":/export"}, "nfs"},
		{map[string]string{"type": "cifs", "o": "username=u,password=p", "device": "//nas/share"}, "cifs"},
		{map[string]string{"type": "none", "o": "bind", "device": "/srv/data"}, "bind"},
		{map[string]string{"o": "rw,bind,noexec", "device": "/srv/data"}, "bind"},
		{map[string]string{"size": "10G"}, "custom"},
	}
	for _, c := range cases {
		if got := optionMount(c.options); got != c.want {
			t.Errorf("optionMount(%v) = %q, want %q", c.options, got, c.want)
		}
	}
}
