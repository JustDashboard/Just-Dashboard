package dbx

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Redis has accounts but no SQL. Its vocabulary is kept — a user has an ACL —
// and mapped onto the same Role shape the page draws for the SQL engines.
//
// An ACL rule is a sentence of tokens: whether the user may connect, which
// passwords it has, which keys and channels it may touch, and which commands
// it may run, the last read left to right so that "+@all -@dangerous" and
// "-@dangerous +@all" mean different things. Everything here keeps that order
// and never keeps the passwords: ACL LIST prints their hashes, and a hash on
// a page any reader can open is a credential leaked to somebody with a GPU.

// RedisACLUser is one user's rule, taken apart.
type RedisACLUser struct {
	Name string `json:"name"`
	// Enabled is "on": a user that is off cannot authenticate at all.
	Enabled bool `json:"enabled"`
	// NoPassword means any password is accepted. Passwords is how many the
	// user has; what they are is never read out.
	NoPassword bool `json:"noPassword"`
	Passwords  int  `json:"passwords"`
	// Keys, Channels and Commands are the rule's tokens as written, in
	// order: "~app:*", "%R~cache:*"; "&events.*"; "+@all", "-@dangerous",
	// "+config|get".
	Keys     []string `json:"keys"`
	Channels []string `json:"channels"`
	Commands []string `json:"commands"`
	// Selectors are the parenthesised alternative rule sets Redis 7 added.
	Selectors []string `json:"selectors,omitempty"`
	// Flags are the tokens that are none of the above, such as
	// sanitize-payload.
	Flags []string `json:"flags,omitempty"`
	// Unrestricted is the common whole-server grant: every command on every
	// key.
	Unrestricted bool `json:"unrestricted"`
	// System marks the default user, which every server has and which cannot
	// be removed.
	System bool `json:"system"`
	// Self marks the user the dashboard itself connects as. Disabling or
	// removing it would cut the dashboard off, and both are refused.
	Self bool `json:"self"`
	// Rule is the whole rule as one line, without its passwords.
	Rule string `json:"rule"`
}

// parseRedisACL reads one line of ACL LIST:
//
//	user default on nopass sanitize-payload ~* &* +@all
//
// It returns false for a line that is not a user.
func parseRedisACL(line string) (RedisACLUser, bool) {
	tokens := redisACLTokens(line)
	if len(tokens) < 2 || tokens[0] != "user" {
		return RedisACLUser{}, false
	}
	u := RedisACLUser{
		Name: tokens[1], System: tokens[1] == "default",
		Keys: []string{}, Channels: []string{}, Commands: []string{},
	}
	kept := make([]string, 0, len(tokens))
	allCommands, allKeys := false, false
	for _, t := range tokens[2:] {
		switch {
		case t == "on":
			u.Enabled = true
		case t == "off":
			u.Enabled = false
		case t == "nopass":
			u.NoPassword = true
		case strings.HasPrefix(t, "#"), strings.HasPrefix(t, ">"), strings.HasPrefix(t, "<"), strings.HasPrefix(t, "!"):
			// A password, by hash or in clear. Counted and dropped: this is
			// the one token that must not reach the page.
			u.Passwords++
			continue
		case strings.HasPrefix(t, "("):
			u.Selectors = append(u.Selectors, t)
		case strings.HasPrefix(t, "~"), strings.HasPrefix(t, "%"), t == "allkeys", t == "resetkeys":
			u.Keys = append(u.Keys, t)
			if t == "~*" || t == "allkeys" {
				allKeys = true
			}
		case strings.HasPrefix(t, "&"), t == "allchannels", t == "resetchannels":
			u.Channels = append(u.Channels, t)
		case strings.HasPrefix(t, "+"), strings.HasPrefix(t, "-"), t == "allcommands", t == "nocommands":
			u.Commands = append(u.Commands, t)
			// The rule is read left to right, so whether every command is
			// allowed is whatever the last sweeping token said.
			switch t {
			case "+@all", "allcommands":
				allCommands = true
			case "-@all", "nocommands":
				allCommands = false
			default:
				if strings.HasPrefix(t, "-") {
					allCommands = false
				}
			}
		default:
			u.Flags = append(u.Flags, t)
		}
		kept = append(kept, t)
	}
	u.Unrestricted = allCommands && allKeys
	u.Rule = strings.Join(kept, " ")
	return u, true
}

// redisACLTokens splits a rule on spaces, keeping a parenthesised selector —
// which has spaces inside it — as one token.
func redisACLTokens(line string) []string {
	var (
		out   []string
		depth int
		start = -1
	)
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '(':
			if start < 0 {
				start = i
			}
			depth++
		case c == ')':
			if depth > 0 {
				depth--
			}
		case c == ' ' && depth == 0:
			if start >= 0 {
				out = append(out, line[start:i])
				start = -1
			}
		default:
			if start < 0 {
				start = i
			}
		}
	}
	if start >= 0 {
		out = append(out, line[start:])
	}
	return out
}

// RedisACLUsers reads every user and its rule. Redis 6 and later only; an
// older server answers with an unknown-command error, which the caller
// reports as unsupported.
func RedisACLUsers(ctx context.Context, client *redis.Client) ([]RedisACLUser, error) {
	pipe := client.Pipeline()
	list := pipe.Do(ctx, "ACL", "LIST")
	whoami := pipe.Do(ctx, "ACL", "WHOAMI")
	_, _ = pipe.Exec(ctx)
	lines, err := list.StringSlice()
	if err != nil {
		return nil, err
	}
	self := redisACLSelf(whoami)
	out := make([]RedisACLUser, 0, len(lines))
	for _, line := range lines {
		u, ok := parseRedisACL(line)
		if !ok {
			continue
		}
		u.Self = self != "" && u.Name == self
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// redisACLSelf reads ACL WHOAMI, which Dragonfly answers as a sentence
// ("User is default") rather than a name.
func redisACLSelf(cmd *redis.Cmd) string {
	name, err := cmd.Text()
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(name, "User is ")
}

// RedisUsers reads the ACL as the Role shape the accounts page draws for
// every engine. The rule rides in MemberOf, without the password hashes ACL
// LIST prints beside it.
func RedisUsers(ctx context.Context, client *redis.Client) ([]Role, error) {
	users, err := RedisACLUsers(ctx, client)
	if err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(users))
	for _, u := range users {
		r := Role{
			Name: u.Name, ConnLimit: -1, System: u.System,
			Login: u.Enabled, Locked: !u.Enabled, Superuser: u.Unrestricted,
		}
		if u.Rule != "" {
			r.MemberOf = []string{u.Rule}
		}
		out = append(out, r)
	}
	return out, nil
}

// redisACLName bounds a user name the way ACL SETUSER would otherwise not: a
// space or a newline in it becomes a second rule.
func redisACLName(name string) error {
	if name == "" || len(name) > 128 {
		return fmt.Errorf("a user name of up to 128 characters is required")
	}
	for _, r := range name {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("user name may not contain spaces or control characters")
		}
	}
	return nil
}

// RedisACLSpec is a change to one user. A nil field is left as it is, which
// is what lets a page change a user's key patterns without knowing — or
// resetting — its password.
type RedisACLSpec struct {
	Name string
	// Create refuses the change when the user already exists.
	Create  bool
	Enabled *bool
	// Password replaces every password the user has. NoPassword removes them
	// and accepts anything, which is only ever right for a user that is also
	// limited to nothing harmful.
	Password   *string
	NoPassword bool
	// Keys, Channels and Commands each replace that part of the rule whole.
	// A key pattern may be written bare ("app:*") and is given its ~; a
	// channel likewise its &.
	Keys     *[]string
	Channels *[]string
	Commands *[]string
}

var (
	// A command rule is +name, -name, +name|sub, +@category or -@category.
	redisACLCommandRe = regexp.MustCompile(`^[+-](@[A-Za-z][A-Za-z0-9_-]*|[A-Za-z][A-Za-z0-9._-]*(\|[A-Za-z0-9._-]+)?)$`)
	// A key rule may say which way it applies: %R~, %W~ or %RW~.
	redisACLKeyRe = regexp.MustCompile(`^(~|%(R|W|RW)~)`)
)

func redisACLPattern(what, token string) error {
	if token == "" || len(token) > 512 {
		return fmt.Errorf("a %s pattern of up to 512 characters is required", what)
	}
	for _, r := range token {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("a %s pattern may not contain spaces or control characters", what)
		}
	}
	return nil
}

// rules turns the spec into ACL SETUSER arguments. Each is passed to the
// server as its own argument, so a pattern cannot smuggle a second rule in;
// the checks are there to refuse a malformed rule with a sentence instead of
// the server's "Syntax error".
func (s RedisACLSpec) rules() ([]string, error) {
	var out []string
	if s.Enabled != nil {
		if *s.Enabled {
			out = append(out, "on")
		} else {
			out = append(out, "off")
		}
	}
	switch {
	case s.NoPassword && s.Password != nil:
		return nil, fmt.Errorf("give the user a password or none, not both")
	case s.NoPassword:
		out = append(out, "resetpass", "nopass")
	case s.Password != nil:
		if err := validatePassword(*s.Password); err != nil {
			return nil, err
		}
		out = append(out, "resetpass", ">"+*s.Password)
	}
	if s.Keys != nil {
		out = append(out, "resetkeys")
		for _, k := range *s.Keys {
			if k == "allkeys" {
				k = "~*"
			}
			if !redisACLKeyRe.MatchString(k) {
				k = "~" + k
			}
			// The pattern itself, after whatever marks it as one: "~" alone
			// would match only the key with no name.
			if err := redisACLPattern("key", redisACLKeyRe.ReplaceAllString(k, "")); err != nil {
				return nil, err
			}
			out = append(out, k)
		}
	}
	if s.Channels != nil {
		out = append(out, "resetchannels")
		for _, c := range *s.Channels {
			if c == "allchannels" {
				c = "&*"
			}
			if !strings.HasPrefix(c, "&") {
				c = "&" + c
			}
			if err := redisACLPattern("channel", strings.TrimPrefix(c, "&")); err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	if s.Commands != nil {
		// Start from nothing, so the list given is the whole of what the
		// user may run rather than an addition to what it could before.
		out = append(out, "-@all")
		for _, c := range *s.Commands {
			switch c {
			case "allcommands":
				c = "+@all"
			case "nocommands":
				c = "-@all"
			}
			if !redisACLCommandRe.MatchString(c) {
				// The offending rule is not quoted back. What ends up in the
				// wrong box of a form about accounts is, sooner or later, a
				// password, and this sentence is written to the audit log.
				return nil, fmt.Errorf("a command rule is written +name, -name, +@category or -@category")
			}
			out = append(out, strings.ToLower(c))
		}
	}
	return out, nil
}

// Validate checks a change without making it, so a malformed one can be
// refused before anything about it is recorded.
func (s RedisACLSpec) Validate() error {
	if err := redisACLName(s.Name); err != nil {
		return err
	}
	_, err := s.rules()
	return err
}

// RedisACLResult is what a change to a user left behind.
type RedisACLResult struct {
	User RedisACLUser `json:"user"`
	// Persisted is whether the change was written to the server's ACL file.
	// Without one the user exists until the server restarts, and Notice says
	// so.
	Persisted bool   `json:"persisted"`
	Notice    string `json:"notice,omitempty"`
}

// RedisACLSetUser creates a user or changes one.
func RedisACLSetUser(ctx context.Context, client *redis.Client, spec RedisACLSpec) (*RedisACLResult, error) {
	if err := redisACLName(spec.Name); err != nil {
		return nil, err
	}
	rules, err := spec.rules()
	if err != nil {
		return nil, err
	}
	users, err := RedisACLUsers(ctx, client)
	if err != nil {
		return nil, err
	}
	var existing *RedisACLUser
	for i := range users {
		if users[i].Name == spec.Name {
			existing = &users[i]
		}
	}
	switch {
	case spec.Create && existing != nil:
		return nil, fmt.Errorf("a user named %q already exists", spec.Name)
	case !spec.Create && existing == nil:
		return nil, fmt.Errorf("there is no user named %q", spec.Name)
	case existing != nil && existing.Self && spec.Enabled != nil && !*spec.Enabled:
		return nil, fmt.Errorf("the dashboard connects as %q; switching it off would cut the dashboard off from this server", spec.Name)
	case len(rules) == 0 && !spec.Create:
		return nil, fmt.Errorf("nothing to change")
	}
	args := make([]any, 0, len(rules)+3)
	args = append(args, "ACL", "SETUSER", spec.Name)
	for _, r := range rules {
		args = append(args, r)
	}
	if err := client.Do(ctx, args...).Err(); err != nil {
		return nil, redisACLError(err, spec.Password)
	}
	out := &RedisACLResult{}
	out.Persisted, out.Notice = redisACLPersist(ctx, client)
	users, err = RedisACLUsers(ctx, client)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u.Name == spec.Name {
			out.User = u
		}
	}
	return out, nil
}

// redisACLError makes sure a refused rule does not echo a password back: the
// server quotes the offending token, and that token may be the password.
func redisACLError(err error, password *string) error {
	if password != nil && *password != "" && strings.Contains(err.Error(), *password) {
		return fmt.Errorf("the server refused the rule")
	}
	return err
}

// RedisACLDeleteUser removes a user and disconnects whoever was using it.
func RedisACLDeleteUser(ctx context.Context, client *redis.Client, name string) (*RedisACLResult, error) {
	if err := redisACLName(name); err != nil {
		return nil, err
	}
	if name == "default" {
		return nil, fmt.Errorf("the default user cannot be removed")
	}
	whoami := client.Do(ctx, "ACL", "WHOAMI")
	if self := redisACLSelf(whoami); self == name {
		return nil, fmt.Errorf("the dashboard connects as %q; removing it would cut the dashboard off from this server", name)
	}
	removed, err := client.Do(ctx, "ACL", "DELUSER", name).Int64()
	if err != nil {
		return nil, err
	}
	if removed == 0 {
		return nil, fmt.Errorf("there is no user named %q", name)
	}
	out := &RedisACLResult{User: RedisACLUser{Name: name}}
	out.Persisted, out.Notice = redisACLPersist(ctx, client)
	return out, nil
}

// RedisCreateUser adds an ACL user. A superuser may run every command on
// every key; anyone else starts able to read and write all keys but not to
// run the commands Redis itself marks dangerous. Narrower rules are what
// RedisACLSetUser is for.
func RedisCreateUser(ctx context.Context, client *redis.Client, spec RoleSpec) error {
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	on := true
	commands := []string{"+@all", "-@dangerous"}
	if spec.Superuser {
		commands = []string{"+@all"}
	}
	_, err := RedisACLSetUser(ctx, client, RedisACLSpec{
		Name: spec.Name, Create: true, Enabled: &on, Password: &spec.Password,
		Keys: &[]string{"~*"}, Channels: &[]string{"&*"}, Commands: &commands,
	})
	return err
}

func RedisAlterUser(ctx context.Context, client *redis.Client, spec RoleSpec) error {
	if err := redisACLName(spec.Name); err != nil {
		return err
	}
	change := RedisACLSpec{Name: spec.Name}
	if spec.SetPassword {
		change.Password = &spec.Password
	}
	if spec.Superuser {
		change.Commands = &[]string{"+@all"}
	}
	if change.Password == nil && change.Commands == nil {
		return nil
	}
	_, err := RedisACLSetUser(ctx, client, change)
	return err
}

func RedisDropUser(ctx context.Context, client *redis.Client, name string) error {
	_, err := RedisACLDeleteUser(ctx, client, name)
	return err
}

// redisACLPersist writes the ACL to its file where the server has one. A
// server with none answers with an error that means "nothing to save", which
// is not a failure of the change that was just made — but it is something the
// operator should be told, because the change will not survive a restart.
func redisACLPersist(ctx context.Context, client *redis.Client) (bool, string) {
	err := client.Do(ctx, "ACL", "SAVE").Err()
	if err == nil {
		return true, ""
	}
	// "not configured to use an ACL file" from Redis and Valkey, "not running
	// with aclfile" from Dragonfly.
	if msg := strings.ToLower(err.Error()); strings.Contains(msg, "aclfile") || strings.Contains(msg, "acl file") {
		return false, "This server keeps its users in memory rather than in an ACL file, so the change lasts until the server restarts."
	}
	return false, "The change is in effect, but the server could not write it to its ACL file: " + err.Error()
}
