package dbx

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Redis has accounts but no SQL. Its vocabulary is kept — a user has an ACL —
// and mapped onto the same Role shape the page draws for the SQL engines.

// RedisUsers reads the ACL. Redis 6 and later only; an older server answers
// with an unknown-command error, which the caller reports as unsupported.
func RedisUsers(ctx context.Context, client *redis.Client) ([]Role, error) {
	lines, err := client.Do(ctx, "ACL", "LIST").StringSlice()
	if err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(lines))
	for _, line := range lines {
		// "user default on nopass ~* &* +@all"
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "user" {
			continue
		}
		r := Role{Name: fields[1], ConnLimit: -1, System: fields[1] == "default"}
		for _, f := range fields[2:] {
			switch {
			case f == "on":
				r.Login = true
			case f == "off":
				r.Locked = true
			case f == "+@all" || f == "allcommands":
				r.Superuser = true
			}
		}
		if len(fields) > 2 {
			r.MemberOf = []string{strings.Join(fields[2:], " ")}
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

// RedisCreateUser adds an ACL user. A superuser may run every command on
// every key; anyone else starts able to read and write all keys, which is
// what an application account is — narrower rules are Redis's own ACL
// language and belong in the query console.
func RedisCreateUser(ctx context.Context, client *redis.Client, spec RoleSpec) error {
	if err := redisACLName(spec.Name); err != nil {
		return err
	}
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	args := []any{"ACL", "SETUSER", spec.Name, "on", ">" + spec.Password, "~*", "&*"}
	if spec.Superuser {
		args = append(args, "+@all")
	} else {
		args = append(args, "+@all", "-@dangerous")
	}
	if err := client.Do(ctx, args...).Err(); err != nil {
		return err
	}
	return redisACLSave(ctx, client)
}

func RedisAlterUser(ctx context.Context, client *redis.Client, spec RoleSpec) error {
	if err := redisACLName(spec.Name); err != nil {
		return err
	}
	args := []any{"ACL", "SETUSER", spec.Name}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		args = append(args, "resetpass", ">"+spec.Password)
	}
	if spec.Superuser {
		args = append(args, "+@all")
	}
	if len(args) == 3 {
		return nil
	}
	if err := client.Do(ctx, args...).Err(); err != nil {
		return err
	}
	return redisACLSave(ctx, client)
}

func RedisDropUser(ctx context.Context, client *redis.Client, name string) error {
	if err := redisACLName(name); err != nil {
		return err
	}
	if name == "default" {
		return fmt.Errorf("the default user cannot be removed")
	}
	if err := client.Do(ctx, "ACL", "DELUSER", name).Err(); err != nil {
		return err
	}
	return redisACLSave(ctx, client)
}

// redisACLSave persists the ACL where the server has a file for it. A server
// with none answers with an error that means "nothing to save", which is not
// a failure of the change that was just made.
func redisACLSave(ctx context.Context, client *redis.Client) error {
	if err := client.Do(ctx, "ACL", "SAVE").Err(); err != nil && !strings.Contains(err.Error(), "aclfile") {
		return err
	}
	return nil
}
