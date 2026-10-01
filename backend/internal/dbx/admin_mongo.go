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

// --- Accounts in any database, and the roles they hold --------------------
//
// The functions above map MongoDB onto the shape every engine shares, and
// that shape has no room for the two things that make its accounts its own:
// an account belongs to a database, and what it may do is a list of roles,
// each on a database. Everything below works in those terms. Nothing here
// ever asks the server for credentials, so none can be returned.

// MongoRoleRef is a role on a database: readWrite on shop, root on admin.
type MongoRoleRef struct {
	Role string `json:"role" bson:"role"`
	DB   string `json:"db" bson:"db"`
}

// MongoUser is one account.
type MongoUser struct {
	User string `json:"user"`
	// DB is the database the account authenticates against.
	DB    string         `json:"db"`
	Roles []MongoRoleRef `json:"roles"`
	// Mechanisms are the authentication mechanisms the account can use.
	Mechanisms []string `json:"mechanisms"`
	// Superuser is true when one of the roles is root.
	Superuser bool `json:"superuser"`
	// Self marks the account this dashboard is connected as.
	Self bool `json:"self"`
}

// MongoListUsers lists the accounts of one database, or of every database
// when dbName is empty.
func MongoListUsers(ctx context.Context, client *mongo.Client, dbName string) ([]MongoUser, error) {
	target, cmd := "admin", bson.D{{Key: "usersInfo", Value: bson.D{{Key: "forAllDBs", Value: true}}}}
	if dbName != "" {
		if err := mongoDatabaseName(dbName); err != nil {
			return nil, err
		}
		target, cmd = dbName, bson.D{{Key: "usersInfo", Value: 1}}
	}
	var res struct {
		Users []struct {
			User       string         `bson:"user"`
			DB         string         `bson:"db"`
			Roles      []MongoRoleRef `bson:"roles"`
			Mechanisms []string       `bson:"mechanisms"`
		} `bson:"users"`
	}
	if err := client.Database(target).RunCommand(ctx, cmd).Decode(&res); err != nil {
		if dbName != "" {
			return nil, err
		}
		// Listing every database's accounts takes a privilege an account
		// scoped to its own database lacks. What it can always see is admin.
		if client.Database("admin").RunCommand(ctx, bson.D{{Key: "usersInfo", Value: 1}}).Decode(&res) != nil {
			return nil, err
		}
	}
	self := mongoSelf(ctx, client)
	out := make([]MongoUser, 0, len(res.Users))
	for _, u := range res.Users {
		user := MongoUser{User: u.User, DB: u.DB, Roles: u.Roles, Mechanisms: u.Mechanisms}
		if user.Roles == nil {
			user.Roles = []MongoRoleRef{}
		}
		if user.Mechanisms == nil {
			user.Mechanisms = []string{}
		}
		for _, role := range u.Roles {
			if role.Role == "root" || role.Role == "__system" {
				user.Superuser = true
			}
		}
		user.Self = self[u.User+"@"+u.DB]
		out = append(out, user)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DB != out[j].DB {
			return out[i].DB < out[j].DB
		}
		return out[i].User < out[j].User
	})
	return out, nil
}

// mongoSelf names the accounts this connection is authenticated as, as
// "user@db". It is empty on a server that asks for no authentication.
func mongoSelf(ctx context.Context, client *mongo.Client) map[string]bool {
	out := map[string]bool{}
	raw, err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "connectionStatus", Value: 1}}).Raw()
	if err != nil {
		return out
	}
	users, ok := raw.Lookup("authInfo", "authenticatedUsers").ArrayOK()
	if !ok {
		return out
	}
	values, _ := users.Values()
	for _, v := range values {
		doc, ok := v.DocumentOK()
		if !ok {
			continue
		}
		user, _ := doc.Lookup("user").StringValueOK()
		db, _ := doc.Lookup("db").StringValueOK()
		out[user+"@"+db] = true
	}
	return out
}

// MongoPrivilege is what a role may do on one resource.
type MongoPrivilege struct {
	// Resource is the resource document as relaxed Extended JSON:
	// {"db": "shop", "collection": ""} is every collection of shop.
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

// MongoRole is one role, built in or made by an administrator.
type MongoRole struct {
	Role    string `json:"role"`
	DB      string `json:"db"`
	Builtin bool   `json:"builtin"`
	// Roles are the roles this one inherits from.
	Roles []MongoRoleRef `json:"roles"`
	// Privileges are listed for custom roles. A built-in role's are fixed
	// and documented, and are not repeated here.
	Privileges []MongoPrivilege `json:"privileges"`
}

// MongoListRoles lists the roles a database offers: the built-in ones and
// any made in it.
func MongoListRoles(ctx context.Context, client *mongo.Client, dbName string) ([]MongoRole, error) {
	if err := mongoDatabaseName(dbName); err != nil {
		return nil, err
	}
	cmd := bson.D{
		{Key: "rolesInfo", Value: 1},
		{Key: "showBuiltinRoles", Value: true},
		{Key: "showPrivileges", Value: true},
	}
	raw, err := client.Database(dbName).RunCommand(ctx, cmd).Raw()
	if err != nil {
		return nil, err
	}
	roles, _ := raw.Lookup("roles").ArrayOK()
	values, _ := roles.Values()
	out := make([]MongoRole, 0, len(values))
	for _, v := range values {
		doc, ok := v.DocumentOK()
		if !ok {
			continue
		}
		role := MongoRole{Roles: []MongoRoleRef{}, Privileges: []MongoPrivilege{}}
		role.Role, _ = doc.Lookup("role").StringValueOK()
		role.DB, _ = doc.Lookup("db").StringValueOK()
		role.Builtin, _ = doc.Lookup("isBuiltin").BooleanOK()
		if inherited, ok := doc.Lookup("roles").ArrayOK(); ok {
			items, _ := inherited.Values()
			for _, item := range items {
				var ref MongoRoleRef
				if d, ok := item.DocumentOK(); ok && bson.Unmarshal(d, &ref) == nil {
					role.Roles = append(role.Roles, ref)
				}
			}
		}
		if privileges, ok := doc.Lookup("privileges").ArrayOK(); ok && !role.Builtin {
			items, _ := privileges.Values()
			for _, item := range items {
				d, ok := item.DocumentOK()
				if !ok {
					continue
				}
				p := MongoPrivilege{Actions: []string{}}
				if resource, ok := d.Lookup("resource").DocumentOK(); ok {
					p.Resource = mongoRelaxedJSON(resource)
				}
				if actions, ok := d.Lookup("actions").ArrayOK(); ok {
					names, _ := actions.Values()
					for _, n := range names {
						if s, ok := n.StringValueOK(); ok {
							p.Actions = append(p.Actions, s)
						}
					}
				}
				role.Privileges = append(role.Privileges, p)
			}
		}
		out = append(out, role)
	}
	sort.Slice(out, func(i, j int) bool {
		// Custom roles first: they are the ones somebody here made.
		if out[i].Builtin != out[j].Builtin {
			return !out[i].Builtin
		}
		return out[i].Role < out[j].Role
	})
	return out, nil
}

// mongoAccount checks the two halves of an account's identity.
func mongoAccount(dbName, user string) error {
	if err := mongoDatabaseName(dbName); err != nil {
		return err
	}
	if err := validateIdent(user); err != nil {
		return fmt.Errorf("user name: %w", err)
	}
	return nil
}

func mongoRoleList(roles []MongoRoleRef) (bson.A, error) {
	out := bson.A{}
	for _, r := range roles {
		if err := validateIdent(r.Role); err != nil {
			return nil, fmt.Errorf("role name: %w", err)
		}
		if err := mongoDatabaseName(r.DB); err != nil {
			return nil, fmt.Errorf("role %s: %w", r.Role, err)
		}
		out = append(out, bson.D{{Key: "role", Value: r.Role}, {Key: "db", Value: r.DB}})
	}
	return out, nil
}

// MongoCreateUserIn makes an account in a database with the given roles.
func MongoCreateUserIn(ctx context.Context, client *mongo.Client, dbName, user, password string, roles []MongoRoleRef) error {
	if err := mongoAccount(dbName, user); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	list, err := mongoRoleList(roles)
	if err != nil {
		return err
	}
	cmd := bson.D{{Key: "createUser", Value: user}, {Key: "pwd", Value: password}, {Key: "roles", Value: list}}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}

// MongoSetUserPassword changes an account's password and nothing else.
func MongoSetUserPassword(ctx context.Context, client *mongo.Client, dbName, user, password string) error {
	if err := mongoAccount(dbName, user); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	cmd := bson.D{{Key: "updateUser", Value: user}, {Key: "pwd", Value: password}}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}

// MongoGrantRoles adds roles to an account. The roles it already holds stay.
func MongoGrantRoles(ctx context.Context, client *mongo.Client, dbName, user string, roles []MongoRoleRef) error {
	return mongoChangeRoles(ctx, client, "grantRolesToUser", dbName, user, roles)
}

// MongoRevokeRoles takes roles from an account. The others stay.
func MongoRevokeRoles(ctx context.Context, client *mongo.Client, dbName, user string, roles []MongoRoleRef) error {
	return mongoChangeRoles(ctx, client, "revokeRolesFromUser", dbName, user, roles)
}

func mongoChangeRoles(ctx context.Context, client *mongo.Client, command, dbName, user string, roles []MongoRoleRef) error {
	if err := mongoAccount(dbName, user); err != nil {
		return err
	}
	if len(roles) == 0 {
		return fmt.Errorf("at least one role is required")
	}
	list, err := mongoRoleList(roles)
	if err != nil {
		return err
	}
	cmd := bson.D{{Key: command, Value: user}, {Key: "roles", Value: list}}
	return client.Database(dbName).RunCommand(ctx, cmd).Err()
}

// MongoDropUserIn removes an account from a database. The account this
// dashboard is connected as is refused: dropping it would cut the branch
// every later request sits on.
func MongoDropUserIn(ctx context.Context, client *mongo.Client, dbName, user string) error {
	if err := mongoAccount(dbName, user); err != nil {
		return err
	}
	if mongoSelf(ctx, client)[user+"@"+dbName] {
		return fmt.Errorf("%s is the account this dashboard connects as; change the saved connection to another account first", user)
	}
	return client.Database(dbName).RunCommand(ctx, bson.D{{Key: "dropUser", Value: user}}).Err()
}
