package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// The two engines with accounts but no SQL. Their vocabulary is kept — a
// Mongo user has roles on databases, a Redis user has an ACL — and mapped onto
// the same Role shape the page draws for the SQL engines, so an operator
// managing five servers reads one list, not five.

// MongoUsers lists the accounts in the admin database, which is where every
// account made by the official image and by this dashboard lives.
func MongoUsers(ctx context.Context, client *mongo.Client) ([]Role, error) {
	var res struct {
		Users []struct {
			User  string `bson:"user"`
			DB    string `bson:"db"`
			Roles []struct {
				Role string `bson:"role"`
				DB   string `bson:"db"`
			} `bson:"roles"`
		} `bson:"users"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: 1}}).Decode(&res); err != nil {
		return nil, err
	}
	out := make([]Role, 0, len(res.Users))
	for _, u := range res.Users {
		r := Role{Name: u.User, Host: u.DB, Login: true, ConnLimit: -1}
		for _, role := range u.Roles {
			r.MemberOf = append(r.MemberOf, role.Role+"@"+role.DB)
			switch role.Role {
			case "root", "__system":
				r.Superuser = true
			case "userAdminAnyDatabase", "userAdmin":
				r.CreateRole = true
			case "dbAdminAnyDatabase", "readWriteAnyDatabase", "dbOwner":
				r.CreateDB = true
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// MongoCreateUser makes an account in the admin database. A superuser gets
// root; anyone else starts with no roles and is granted per database.
func MongoCreateUser(ctx context.Context, client *mongo.Client, spec RoleSpec) error {
	if err := validatePassword(spec.Password); err != nil {
		return err
	}
	if err := validateIdent(spec.Name); err != nil {
		return err
	}
	roles := bson.A{}
	if spec.Superuser {
		roles = append(roles, bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}})
	}
	cmd := bson.D{{Key: "createUser", Value: spec.Name}, {Key: "pwd", Value: spec.Password}, {Key: "roles", Value: roles}}
	return client.Database("admin").RunCommand(ctx, cmd).Err()
}

func MongoAlterUser(ctx context.Context, client *mongo.Client, spec RoleSpec) error {
	if err := validateIdent(spec.Name); err != nil {
		return err
	}
	if spec.SetPassword {
		if err := validatePassword(spec.Password); err != nil {
			return err
		}
		cmd := bson.D{{Key: "updateUser", Value: spec.Name}, {Key: "pwd", Value: spec.Password}}
		if err := client.Database("admin").RunCommand(ctx, cmd).Err(); err != nil {
			return err
		}
	}
	if spec.Superuser {
		cmd := bson.D{{Key: "grantRolesToUser", Value: spec.Name},
			{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: "root"}, {Key: "db", Value: "admin"}}}}}
		return client.Database("admin").RunCommand(ctx, cmd).Err()
	}
	return nil
}

func MongoDropUser(ctx context.Context, client *mongo.Client, name string) error {
	if err := validateIdent(name); err != nil {
		return err
	}
	return client.Database("admin").RunCommand(ctx, bson.D{{Key: "dropUser", Value: name}}).Err()
}

func MongoGrant(ctx context.Context, client *mongo.Client, user, database string, level GrantLevel) error {
	if err := validateIdent(user); err != nil {
		return err
	}
	if err := validateIdent(database); err != nil {
		return err
	}
	var role string
	switch level {
	case GrantAll:
		role = "dbOwner"
	case GrantWrite:
		role = "readWrite"
	case GrantRead:
		role = "read"
	default:
		return fmt.Errorf("unknown grant level %q", level)
	}
	cmd := bson.D{{Key: "grantRolesToUser", Value: user},
		{Key: "roles", Value: bson.A{bson.D{{Key: "role", Value: role}, {Key: "db", Value: database}}}}}
	return client.Database("admin").RunCommand(ctx, cmd).Err()
}

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

// MongoClientAddresses lists the client addresses of every open connection
// the server reports, one entry per connection, this one excluded.
func MongoClientAddresses(ctx context.Context, client *mongo.Client) []string {
	var res struct {
		Inprog []struct {
			Client string `bson:"client"`
			Desc   string `bson:"desc"`
		} `bson:"inprog"`
	}
	cmd := bson.D{{Key: "currentOp", Value: 1}, {Key: "$all", Value: true}}
	if err := client.Database("admin").RunCommand(ctx, cmd).Decode(&res); err != nil {
		return nil
	}
	out := []string{}
	for _, op := range res.Inprog {
		if op.Client == "" || !strings.HasPrefix(op.Desc, "conn") {
			continue
		}
		host := op.Client
		if i := strings.LastIndex(host, ":"); i > 0 {
			host = host[:i]
		}
		out = append(out, host)
	}
	// Drop one loopback entry for the connection asking.
	for i, h := range out {
		if h == "127.0.0.1" || h == "::1" {
			out = append(out[:i], out[i+1:]...)
			break
		}
	}
	return out
}
