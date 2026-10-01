package dbx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// MongoDB has accounts but no SQL. Its vocabulary is kept — a user has roles
// on databases — and mapped onto the same Role shape the page draws for the
// SQL engines, so an operator managing five servers reads one list, not five.

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
