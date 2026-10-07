package netx

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Headscale is read, never changed: it is somebody's control server, and the
// people on it are not this dashboard's to add or remove. It runs either as a
// binary on this host or, more often, in a container; both are asked the same
// two questions with `-o json`.

// HeadscaleView is the Headscale half of the VPN page.
type HeadscaleView struct {
	// Installed is a `headscale` binary on this host.
	Installed bool `json:"installed"`
	// Container is the name of a running container whose image is Headscale,
	// when there is no binary. Its nodes are listed only if the docker command
	// is here to run `headscale` inside it.
	Container string          `json:"container"`
	Nodes     []HeadscaleNode `json:"nodes"`
	Users     []HeadscaleUser `json:"users"`
	Error     string          `json:"error,omitempty"`
}

// HeadscaleNode is a device registered with the control server.
type HeadscaleNode struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	GivenName   string   `json:"givenName"`
	IPAddresses []string `json:"ipAddresses"`
	Online      bool     `json:"online"`
	// LastSeen is unix seconds, zero for never.
	LastSeen   int64    `json:"lastSeen"`
	User       string   `json:"user"`
	ForcedTags []string `json:"forcedTags"`
}

// HeadscaleUser is an owner of devices.
type HeadscaleUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Nodes int    `json:"nodes"`
}

// headscaleFlexString reads a JSON string or number, because Headscale prints 64-bit
// ids as strings through protobuf's JSON and older builds print numbers.
type headscaleFlexString string

func (f *headscaleFlexString) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = headscaleFlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*f = headscaleFlexString(n.String())
	return nil
}

// headscaleFlexTime reads the three shapes a timestamp arrives in: protobuf's
// {"seconds":…,"nanos":…}, an RFC 3339 string, and null.
type headscaleFlexTime int64

func (f *headscaleFlexTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var obj struct {
		Seconds headscaleFlexString `json:"seconds"`
	}
	if json.Unmarshal(b, &obj) == nil && obj.Seconds != "" {
		n, _ := strconv.ParseInt(string(obj.Seconds), 10, 64)
		*f = headscaleFlexTime(n)
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil && t.Unix() > 0 {
			*f = headscaleFlexTime(t.Unix())
		}
	}
	return nil
}

type headscaleNodeJSON struct {
	ID          headscaleFlexString `json:"id"`
	Name        string              `json:"name"`
	GivenName   string              `json:"givenName"`
	IPAddresses []string            `json:"ipAddresses"`
	Online      bool                `json:"online"`
	LastSeen    headscaleFlexTime   `json:"lastSeen"`
	User        struct {
		Name string `json:"name"`
	} `json:"user"`
	ForcedTags []string `json:"forcedTags"`
}

type headscaleUserJSON struct {
	ID   headscaleFlexString `json:"id"`
	Name string              `json:"name"`
}

// Headscale reads a Headscale binary on this host. Without one, it is a view
// that says so.
func (s *Service) Headscale(ctx context.Context) *HeadscaleView {
	v := &HeadscaleView{Nodes: []HeadscaleNode{}, Users: []HeadscaleUser{}}
	if !has("headscale") {
		return v
	}
	v.Installed = true
	fillHeadscale(ctx, v, "headscale", nil)
	return v
}

var headscaleContainerIDRe = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// HeadscaleContainer reads a Headscale that runs in a container, through
// `docker exec`. The container is found by the caller (the API layer owns the
// Docker client); this only runs the two read commands, and only with an id
// that is a container id, so nothing a request says becomes an option.
func (s *Service) HeadscaleContainer(ctx context.Context, id, name string) *HeadscaleView {
	v := &HeadscaleView{Container: name, Nodes: []HeadscaleNode{}, Users: []HeadscaleUser{}}
	if !has("docker") {
		v.Error = "The docker command is not available here, so the nodes cannot be listed."
		return v
	}
	if !headscaleContainerIDRe.MatchString(id) {
		v.Error = "The container id is not one Docker issues."
		return v
	}
	fillHeadscale(ctx, v, "docker", []string{"exec", id, "headscale"})
	return v
}

// fillHeadscale runs `nodes list -o json` and `users list -o json` through
// name and the arguments that lead up to the headscale command (none on the
// host, `exec <id> headscale` in a container) into v. A failure of either is
// carried in v.Error; the other is still shown.
func fillHeadscale(ctx context.Context, v *HeadscaleView, name string, lead []string) {
	argv := func(rest ...string) []string { return append(append([]string{}, lead...), rest...) }
	var problems []string

	out, err := run(ctx, name, argv("nodes", "list", "-o", "json")...)
	if err != nil {
		problems = append(problems, "nodes: "+err.Error())
	} else {
		var nodes []headscaleNodeJSON
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &nodes); err != nil {
			problems = append(problems, "nodes: headscale printed something unreadable")
		}
		for _, n := range nodes {
			v.Nodes = append(v.Nodes, HeadscaleNode{
				ID: string(n.ID), Name: n.Name, GivenName: n.GivenName,
				IPAddresses: vpnNonNil(n.IPAddresses), Online: n.Online, LastSeen: int64(n.LastSeen),
				User: n.User.Name, ForcedTags: vpnNonNil(n.ForcedTags),
			})
		}
	}
	sort.Slice(v.Nodes, func(i, j int) bool {
		a, b := v.Nodes[i], v.Nodes[j]
		if a.Online != b.Online {
			return a.Online
		}
		return a.Name < b.Name
	})

	out, err = run(ctx, name, argv("users", "list", "-o", "json")...)
	if err != nil {
		problems = append(problems, "users: "+err.Error())
	} else {
		var users []headscaleUserJSON
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &users); err != nil {
			problems = append(problems, "users: headscale printed something unreadable")
		}
		for _, u := range users {
			hu := HeadscaleUser{ID: string(u.ID), Name: u.Name}
			for _, n := range v.Nodes {
				if n.User == u.Name {
					hu.Nodes++
				}
			}
			v.Users = append(v.Users, hu)
		}
	}
	if len(problems) > 0 {
		v.Error = strings.Join(problems, "; ")
	}
}
