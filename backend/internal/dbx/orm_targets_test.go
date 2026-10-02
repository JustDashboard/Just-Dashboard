package dbx

import (
	"strings"
	"testing"
)

// What each target has to get right, said as lines.
//
// The golden files pin every byte, which catches an accident and explains
// nothing: a diff there says the output moved, not whether it should have.
// These tables are the intent — for each target, the lines that make it
// correct and the lines that would make it wrong — over the same fixtures.
type ormExpectation struct {
	name    string
	engine  string
	req     ORMRequest
	file    string // the generated file to look in; "" is the first
	must    []string
	mustNot []string
	warned  []string // substrings some warning must contain
}

func (e ormExpectation) run(t *testing.T) {
	t.Helper()
	res := ormGenerateCase(t, ormCase{name: e.name, engine: e.engine, req: e.req})
	content := res.Schema
	if e.file != "" {
		content = ""
		for _, f := range res.Files {
			if f.Filename == e.file {
				content = f.Content
			}
		}
		if content == "" {
			names := []string{}
			for _, f := range res.Files {
				names = append(names, f.Filename)
			}
			t.Fatalf("no generated file called %s (have %v)", e.file, names)
		}
	}
	ormMustContain(t, e.name, content, e.must...)
	ormMustNotContain(t, e.name, content, e.mustNot...)
	warnings := strings.Join(res.Warnings, "\n")
	for _, w := range e.warned {
		if !strings.Contains(warnings, w) {
			t.Errorf("%s: no warning mentions %q\n%s", e.name, w, warnings)
		}
	}
}

func TestORMPrisma(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "postgres", engine: "postgres", req: ORMRequest{Target: ORMPrisma},
			must: []string{
				// The original shape: a datasource with the URL from the environment.
				`provider = "postgresql"`, `url      = env("DATABASE_URL")`, `provider = "prisma-client-js"`,
				// Two schemas, so both are named and every model says which is its own.
				`schemas  = ["analytics", "public"]`, `@@schema("analytics")`,
				// The same table name twice: the default schema keeps it.
				"model events {", "model analytics_events {", `@@map("events")`,
				// Defaults of every kind.
				"id BigInt @id @default(autoincrement())",
				"tier customer_tier @default(free)",
				"marketing_opt_in Boolean @default(false)",
				"tags String[] @default([])",
				`profile Json @default("{}")`,
				`external_id String @default(dbgenerated("gen_random_uuid()")) @db.Uuid`,
				"lifetime_value Decimal @default(0) @db.Decimal(12, 2)",
				"created_at DateTime @default(now()) @db.Timestamptz(6)",
				`label String @default("it's")`,
				`stamp DateTime @default(dbgenerated("timezone('utc'::text, now())")) @db.Timestamp(3)`,
				// Native types where the column is not the scalar's default.
				"country String? @db.Char(2)", "line_no Int @db.SmallInt", "ratio Float? @db.Real",
				"sent_at DateTime @default(now()) @db.Timestamptz(3)", "meta Json? @db.Json", "addr String? @db.Inet",
				// A type Prisma has no scalar for stays in the schema, honestly.
				`search Unsupported("tsvector")?`, `span Unsupported("interval")?`,
				// Relations: both ends, named, with the database's own rules.
				`customer customers @relation("orders_customer_id", fields: [customer_id], references: [id], onDelete: Cascade, onUpdate: NoAction)`,
				`orders orders[] @relation("orders_customer_id")`,
				// A self-reference.
				`parent categories? @relation("categories_parent_id", fields: [parent_id], references: [id], onDelete: NoAction, onUpdate: NoAction)`,
				`categories categories[] @relation("categories_parent_id")`,
				// Two keys to one parent are told apart by their columns.
				`messages_sender_id messages[] @relation("messages_sender_id")`,
				`messages_recipient_id messages[] @relation("messages_recipient_id")`,
				// A key that is itself unique is one-to-one.
				`customer_profile customer_profiles? @relation("customer_profiles_customer_id")`,
				// A composite key, and a key to a unique column.
				`order_item order_items @relation("shipments_order_id_line_no", fields: [order_id, line_no], references: [order_id, line_no], onDelete: Cascade, onUpdate: NoAction)`,
				`category categories? @relation("shipments_category_slug", fields: [category_slug], references: [slug], onDelete: NoAction, onUpdate: NoAction)`,
				"@@id([order_id, line_no])",
				`@@unique([order_id, product_id], map: "order_items_order_product_key")`,
				`@@index([status, placed_at(sort: Desc)], map: "orders_status_placed_idx")`,
				`@@index([attributes], map: "products_attributes_gin", type: Gin)`,
				// Names Prisma cannot spell keep their real ones through a map.
				`model Mixed_Case_Table {`, `@@map("Mixed Case Table")`, `weird_column String? @map("weird column")`,
				`fa Boolean @default(false) @map("2fa")`,
				"enum order_status {", `add_to_cart @map("add-to-cart")`,
				"/// People who have an account in the shop",
				// No key, no client: Prisma is told to leave it alone.
				"@@ignore",
			},
			mustNot: []string{
				"model order_summary", "model top_products", "view ", "measurements_2024",
				// A unique index over an expression or part of a table is not a
				// fact about a column.
				"last_seen_at DateTime? @unique", "customers_lower_email_idx",
			},
			warned: []string{
				"One partition was left out", "public.measurements is a partitioned table",
				"public.audit_log has no primary key", "customers_lower_email_idx", "customers_active_idx",
				"public.type_samples.total is a generated column",
			},
		},
		{
			name: "camel", engine: "postgres", req: ORMRequest{Target: ORMPrisma, Naming: ORMNamingCamel},
			must: []string{
				"model Customer {", `@@map("customers")`, `fullName String @map("full_name")`,
				`createdAt DateTime @default(now()) @map("created_at") @db.Timestamptz(6)`,
				"tier CustomerTier @default(free)", "enum CustomerTier {", `@@map("customer_tier")`,
				`customer Customer @relation("orders_customer_id", fields: [customerId], references: [id]`,
				"orderItems OrderItem[]",
				`@@index([status, placedAt(sort: Desc)], map: "orders_status_placed_idx")`,
			},
		},
		{
			name: "views", engine: "postgres", req: ORMRequest{Target: ORMPrisma, Views: ormYes()},
			must: []string{
				`previewFeatures = ["multiSchema", "views"]`, "view order_summary {", "view top_products {",
				"status order_status?",
			},
		},
		{
			name: "prisma 7", engine: "postgres", req: ORMRequest{Target: ORMPrisma, PrismaVersion: "7"},
			must:    []string{`provider = "postgresql"`, `schemas  = ["analytics", "public"]`},
			mustNot: []string{"url ", "previewFeatures"},
		},
		{
			name: "prisma 7 config", engine: "postgres", req: ORMRequest{Target: ORMPrisma, PrismaVersion: "7"},
			file: "prisma.config.ts",
			must: []string{`import { defineConfig, env } from "prisma/config"`, `url: env("DATABASE_URL")`},
		},
		{
			name: "bare", engine: "postgres",
			req: ORMRequest{Target: ORMPrisma, Relations: ormNo(), Enums: ormNo(), Defaults: ormNo()},
			must: []string{
				"id BigInt @id\n", "tier String\n", "status String\n", "customer_id BigInt\n",
			},
			mustNot: []string{"@relation", "@default", "enum "},
		},
		{
			name: "selected", engine: "postgres",
			req: ORMRequest{Target: ORMPrisma, Tables: []string{"customers", "orders", "public.order_items"}},
			must: []string{
				"model customers {", "model orders {", "model order_items {",
				`@relation("orders_customer_id"`, `@relation("order_items_order_id"`,
			},
			// The relations that leave the selection go with it, on both sides.
			mustNot: []string{"products", "messages", "customer_profiles", "analytics", "@@schema"},
		},
		{
			name: "mysql", engine: "mysql", req: ORMRequest{Target: ORMPrisma, Schema: "blog"},
			must: []string{
				`provider = "mysql"`,
				"id Int @id @default(autoincrement()) @db.UnsignedInt",
				`handle String @unique(map: "handle") @db.VarChar(40)`,
				"is_staff Boolean @default(false)",
				"joined_at DateTime @default(now()) @db.DateTime(0)",
				"body String? @db.MediumText",
				// The enum lives in the column type; it is named after the column.
				"status posts_status @default(draft)", "enum posts_status {",
				`flags Unsupported("set('pinned','featured')")?`,
				"views Int @default(0) @db.UnsignedInt", "cover Bytes? @db.MediumBlob", "digest Bytes? @db.Binary(16)",
				"published_at DateTime? @db.DateTime(6)", "updated_at DateTime @default(now()) @db.Timestamp(0)",
				"held_in Int? @db.Year",
				`@relation("posts_author_id", fields: [author_id], references: [id], onDelete: Restrict, onUpdate: NoAction)`,
				`@@index([status, published_at], map: "idx_posts_status")`,
			},
			mustNot: []string{"schemas", "ft_posts_body", "published_posts"},
			warned:  []string{"ft_posts_body on posts is a full-text index"},
		},
		{
			name: "sqlite", engine: "sqlite", req: ORMRequest{Target: ORMPrisma},
			must: []string{
				`provider = "sqlite"`, "id Int @id @default(autoincrement())", "pinned Boolean @default(false)",
				"price Decimal?", "payload Json?", "created_at DateTime @default(now())",
				// REFERENCES users, with no column named: the parent's key.
				`user users @relation("notes_user_id", fields: [user_id], references: [id], onDelete: Cascade, onUpdate: NoAction)`,
				"@@id([user_id, tag])",
			},
			mustNot: []string{"@db."},
		},
		{
			name: "sqlserver", engine: "sqlserver", req: ORMRequest{Target: ORMPrisma},
			must: []string{
				`provider = "sqlserver"`, `schemas  = ["dbo", "sales"]`,
				"id Int @id @default(autoincrement())",
				`guid String @unique(map: "UQ_accounts_guid") @default(dbgenerated("newid()")) @db.UniqueIdentifier`,
				"name String @db.NVarChar(200)", "notes String? @db.NVarChar(Max)", "active Boolean @default(true)",
				"level Int @default(0) @db.TinyInt", "balance Float? @db.Money",
				"created_at DateTime @default(now())", "seen_at DateTime? @db.DateTimeOffset",
				"model accounts {", "model sales_accounts {",
				// Two keys to one parent with no _id to drop: named by their columns.
				`from_account_ref accounts @relation("transfers_from_account"`,
				`to_account_ref accounts? @relation("transfers_to_account"`,
			},
		},
		{
			name: "cockroachdb", engine: "cockroachdb", req: ORMRequest{Target: ORMPrisma},
			must: []string{
				`provider = "cockroachdb"`, "id BigInt @id @default(sequence())", "line_no Int @db.Int2",
				"ratio Float? @db.Float4", "sku String @unique @db.String(32)",
			},
			mustNot: []string{"@db.SmallInt", "@db.VarChar"},
		},
	} {
		t.Run(e.name, e.run)
	}
}

// Prisma reads one MySQL database per connection; two of them in one schema
// file would be two models mapped to one table name.
func TestORMPrismaRefusesSeveralMySQLDatabases(t *testing.T) {
	opts, _ := ORMRequest{Target: ORMPrisma}.Options()
	_, err := GenerateORMFiles(ormMySQLFixture(), opts)
	if err == nil || !strings.Contains(err.Error(), "one database per connection") || !strings.Contains(err.Error(), "blog, stats") {
		t.Errorf("err = %v", err)
	}
}

func TestORMDrizzle(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "postgres", engine: "postgres", req: ORMRequest{Target: ORMDrizzle},
			must: []string{
				`import { relations, sql } from "drizzle-orm"`, `} from "drizzle-orm/pg-core"`,
				// The default schema is pgTable; any other schema is declared and used.
				`export const analytics = pgSchema("analytics")`,
				`export const customers = pgTable("customers", {`,
				`export const analytics_events = analytics.table(`,
				`export const event_kind = analytics.enum("event_kind", ["page_view", "click", "add-to-cart"])`,
				`export const customer_tier = pgEnum("customer_tier", ["free", "plus", "pro"])`,
				`id: bigserial("id", { mode: "number" }).primaryKey().notNull(),`,
				`email: text("email").notNull().unique(),`,
				`tier: customer_tier("tier").notNull().default("free"),`,
				`country: char("country", { length: 2 }),`,
				`tags: text("tags").array().notNull().default([]),`,
				`profile: jsonb("profile").notNull().default({}),`,
				`external_id: uuid("external_id").notNull().defaultRandom(),`,
				`lifetime_value: numeric("lifetime_value", { precision: 12, scale: 2 }).notNull().default("0"),`,
				`created_at: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),`,
				`sent_at: timestamp("sent_at", { precision: 3, withTimezone: true }).notNull().defaultNow(),`,
				`id: bigint("id", { mode: "number" }).primaryKey().notNull().generatedAlwaysAsIdentity(),`,
				`parent_id: integer("parent_id").references((): AnyPgColumn => categories.id),`,
				`customer_id: bigint("customer_id", { mode: "number" }).notNull().references(() => customers.id, { onDelete: "cascade" }),`,
				"primaryKey({ columns: [table.order_id, table.line_no] }),",
				`unique("order_items_order_product_key").on(table.order_id, table.product_id),`,
				`index("orders_status_placed_idx").on(table.status, table.placed_at.desc()),`,
				`index("products_attributes_gin").using("gin", table.attributes),`,
				`foreignKey({ columns: [table.order_id, table.line_no], foreignColumns: [order_items.order_id, order_items.line_no], name: "shipments_item_fkey" }).onDelete("cascade"),`,
				// A type with no builder is declared, not guessed at.
				"const bytea = customType<{ data: Buffer }>({", `avatar: bytea("avatar"),`, `search: tsvector("search"),`,
				"total: numeric(\"total\", { precision: 10, scale: 2 }).generatedAlwaysAs(sql`((small)::numeric * 1.5)`),",
				// Relations on both sides.
				"export const ordersRelations = relations(orders, ({ one, many }) => ({",
				"  customer: one(customers, {\n    fields: [orders.customer_id],\n    references: [customers.id],\n    relationName: \"orders_customer_id\",\n  }),",
				`  order_items: many(order_items, { relationName: "order_items_order_id" }),`,
				`  messages_sender_id: many(messages, { relationName: "messages_sender_id" }),`,
				// The one-to-one's far side is a one() too.
				"  customer_profile: one(customer_profiles, {\n    fields: [customers.id],\n    references: [customer_profiles.customer_id],",
				`_2fa: boolean("2fa").notNull().default(false),`, `weird_column: text("weird column"),`,
			},
			mustNot: []string{
				`pgSchema("public")`, "order_summary", "measurements_2024", `import { bytea`, ` bytea,`,
			},
			warned: []string{"public.customers.avatar has type bytea, which Drizzle ships no builder for"},
		},
		{
			name: "camel", engine: "postgres", req: ORMRequest{Target: ORMDrizzle, Naming: ORMNamingCamel},
			must: []string{
				`export const orderItems = pgTable(`, `orderId: bigint("order_id", { mode: "number" })`,
				`export const customerTier = pgEnum("customer_tier", ["free", "plus", "pro"])`,
				"primaryKey({ columns: [table.orderId, table.lineNo] }),",
				"export const orderItemsRelations = relations(orderItems, ({ one, many }) => ({",
			},
		},
		{
			name: "views", engine: "postgres", req: ORMRequest{Target: ORMDrizzle, Views: ormYes()},
			must: []string{
				`export const order_summary = pgView("order_summary", {`, "}).existing()",
				`export const top_products = analytics.materializedView("top_products", {`,
			},
		},
		{
			name: "mysql", engine: "mysql", req: ORMRequest{Target: ORMDrizzle},
			must: []string{
				`} from "drizzle-orm/mysql-core"`, `export const blog = mysqlSchema("blog")`,
				`export const authors = blog.table("authors", {`,
				// The same table in two databases.
				`export const events = blog.table("events", {`, `export const stats_events = stats.table(`,
				`id: int("id", { unsigned: true }).primaryKey().notNull().autoincrement(),`,
				`is_staff: boolean("is_staff").notNull().default(false),`,
				"joined_at: datetime(\"joined_at\").notNull().default(sql`CURRENT_TIMESTAMP`),",
				`status: mysqlEnum("status", ["draft", "published", "archived"]).notNull().default("draft"),`,
				`published_at: datetime("published_at", { fsp: 6 }),`,
				`updated_at: timestamp("updated_at").notNull().defaultNow().onUpdateNow(),`,
				`seen_at: timestamp("seen_at", { fsp: 3 }).notNull().defaultNow(),`,
				`digest: binary("digest", { length: 16 }),`, `held_in: year("held_in"),`,
			},
		},
		{
			name: "sqlite", engine: "sqlite", req: ORMRequest{Target: ORMDrizzle},
			must: []string{
				`} from "drizzle-orm/sqlite-core"`, `export const notes = sqliteTable(`,
				`id: integer("id").primaryKey({ autoIncrement: true }).notNull(),`,
				`id: integer("id").primaryKey().notNull(),`,
				`pinned: integer("pinned", { mode: "boolean" }).notNull().default(false),`,
				`payload: text("payload", { mode: "json" }),`, `attachment: blob("attachment"),`,
				"created_at: text(\"created_at\").notNull().default(sql`CURRENT_TIMESTAMP`),",
				`email: text("email").notNull().unique(),`,
				`user_id: integer("user_id").notNull().references(() => users.id, { onDelete: "cascade" }),`,
			},
		},
	} {
		t.Run(e.name, e.run)
	}
}

func TestORMTypeTargets(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "typescript", engine: "postgres", req: ORMRequest{Target: ORMTypeScript},
			must: []string{
				"// Row types as the database returns them.",
				`export type OrderStatus = "pending" | "paid" | "shipped" | "delivered" | "cancelled" | "refunded"`,
				"export interface Customer {", "  id: string\n", "  tier: CustomerTier\n", "  country: string | null\n",
				"  tags: string[]\n", "  profile: unknown\n", "  created_at: string\n", "  lifetime_value: string\n",
				"  scores: number[] | null\n",
				// A key that is not an identifier is quoted, never renamed.
				`  "weird column": string | null`, `  "2fa": boolean`,
				// Views are row types too, and the same name in two schemas is two types.
				"export interface OrderSummary {", "export interface Event {", "export interface AnalyticsEvent {",
			},
			mustNot: []string{": any"},
		},
		{
			name: "typescript dates", engine: "postgres",
			req:  ORMRequest{Target: ORMTypeScript, Dates: "Date", Naming: ORMNamingCamel, Enums: ormNo()},
			must: []string{"  createdAt: Date\n", "  lastSeenAt: Date | null\n", "  tier: string\n", "  releasedOn: Date | null\n"},
			mustNot: []string{
				"export type ", "created_at",
				// A time of day is not a Date.
				"starts: Date",
			},
		},
		{
			name: "typescript mysql", engine: "mysql", req: ORMRequest{Target: ORMTypeScript},
			must: []string{
				`export type PostsStatus = "draft" | "published" | "archived"`,
				"  is_staff: boolean\n", "  views: number\n", "  id: string\n", "  rating: string | null\n",
				"  status: PostsStatus\n", "  held_in: number | null\n",
			},
		},
		{
			name: "typescript oracle", engine: "oracle", req: ORMRequest{Target: ORMTypeScript},
			must: []string{
				"export interface Customer {", "  ID: string\n", "  BALANCE: string\n", "  RATIO: number | null\n",
				"  CREATED: string\n", "  TOKEN: string | null\n",
			},
		},
		{
			name: "typescript clickhouse", engine: "clickhouse", req: ORMRequest{Target: ORMTypeScript},
			must: []string{
				`export type PageViewsDevice = "desktop" | "mobile"`,
				"  user_id: string\n", "  duration_ms: number\n", "  referrer: string | null\n", "  tags: string[]\n",
				"  props: unknown\n", "  big: string\n", "  revenue: string\n",
			},
		},
		{
			name: "zod", engine: "postgres", req: ORMRequest{Target: ORMZod},
			must: []string{
				`import { z } from "zod"`,
				`export const OrderStatusSchema = z.enum(["pending", "paid", "shipped", "delivered", "cancelled", "refunded"])`,
				"export const CustomerSchema = z.object({", "  id: z.string(),", "  tier: CustomerTierSchema,",
				"  tags: z.array(z.string()),", "  created_at: z.coerce.date(),", "  country: z.string().nullable(),",
				"export type Customer = z.infer<typeof CustomerSchema>",
				// What the database fills in is optional on the way in.
				`export const CustomerInsertSchema = CustomerSchema.partial({ "id": true, "tier": true, "marketing_opt_in": true, "tags": true, "profile": true, "external_id": true, "lifetime_value": true, "created_at": true })`,
				// A key with no default has to be supplied.
				"export const DailyRevenueInsertSchema = DailyRevenueSchema\n",
				"export const MeasurementInsertSchema = MeasurementSchema\n",
			},
			// A view has rows to validate and nothing to insert.
			mustNot: []string{"OrderSummaryInsertSchema"},
		},
		{
			name: "zod plain", engine: "postgres", req: ORMRequest{Target: ORMZod, InsertSchemas: ormNo(), Views: ormNo()},
			must:    []string{"export const CustomerSchema = z.object({"},
			mustNot: []string{"InsertSchema", "OrderSummary"},
		},
		{
			name: "kysely", engine: "postgres", req: ORMRequest{Target: ORMKysely},
			must: []string{
				`import type { ColumnType, Generated, GeneratedAlways, Insertable, Selectable, Updateable } from "kysely"`,
				"export type Int8 = ColumnType<string, string | number | bigint, string | number | bigint>",
				"export interface CustomerTable {", "  id: Generated<Int8>", "  tier: Generated<CustomerTier>",
				"  avatar: Buffer | null", "  created_at: Generated<Timestamp>", "  last_seen_at: Timestamp | null",
				"  id: GeneratedAlways<Int8>", "  total: GeneratedAlways<Numeric | null>",
				"export interface Database {", "  customers: CustomerTable", `  "analytics.events": AnalyticsEventTable`,
				`  "Mixed Case Table": MixedCaseTableTable`,
				"export type Customer = Selectable<CustomerTable>", "export type NewCustomer = Insertable<CustomerTable>",
				"export type CustomerUpdate = Updateable<CustomerTable>",
			},
			mustNot: []string{"NewOrderSummary"},
		},
		{
			name: "kysely mysql", engine: "mysql", req: ORMRequest{Target: ORMKysely},
			must: []string{
				// mysql2 hands back a TINYINT(1) as the number it is.
				"  is_staff: Generated<number>", "  id: Generated<number>", "  rating: Numeric | null",
				`  "blog.posts": PostTable`, `  "stats.events": StatsEventTable`,
			},
		},
		{
			name: "kysely sqlite", engine: "sqlite", req: ORMRequest{Target: ORMKysely},
			must: []string{"  pinned: Generated<number>", "  created_at: Generated<string>", "  attachment: Buffer | null", "  notes: NoteTable"},
		},
		{
			name: "go structs", engine: "postgres", req: ORMRequest{Target: ORMGoStructs},
			must: []string{
				"package models", `"database/sql"`, `"encoding/json"`,
				"type OrderStatus string", `OrderStatusPending   OrderStatus = "pending"`,
				"func (e *OrderStatus) Scan(src any) error {",
				// A nullable enum column gets the wrapper sqlc writes for one.
				"type NullOrderStatus struct {", "func (ns NullOrderStatus) Value() (driver.Value, error) {",
				"type Customer struct {", "`db:\"id\" json:\"id\"`", "sql.NullString", "sql.NullTime", "json.RawMessage",
				"Tags           []string", "Scores   []int32", "sql.NullInt32", "sql.Null[float32]",
			},
			mustNot: []string{"gorm", "TableName"},
		},
		{
			name: "go pointers", engine: "postgres", req: ORMRequest{Target: ORMGoStructs, Nulls: "pointer", Enums: ormNo()},
			must:    []string{"Country        *string", "LastSeenAt    *time.Time", "Tier           string"},
			mustNot: []string{"sql.Null", "type OrderStatus", `"database/sql"`},
		},
		{
			name: "go clickhouse", engine: "clickhouse", req: ORMRequest{Target: ORMGoStructs},
			must: []string{"UserID     uint64", "DurationMs uint32", "IsBot      uint8", "Tags       []string", "Big        string", "Referrer   sql.NullString"},
		},
		{
			name: "jsonschema", engine: "postgres", req: ORMRequest{Target: ORMJSONSchema},
			must: []string{
				`"$schema": "https://json-schema.org/draft/2020-12/schema"`,
				`"OrderStatus": {`, `"enum": ["pending", "paid", "shipped", "delivered", "cancelled", "refunded"]`,
				`"Customer": {`, `"title": "customers"`, `"description": "People who have an account in the shop"`,
				`"$ref": "#/$defs/CustomerTier"`, `"format": "int64"`, `"format": "date-time"`, `"format": "uuid"`,
				`"type": ["string", "null"]`, `"maxLength": 2`, `"contentEncoding": "base64"`,
				`"$comment": "References customers.id."`, `"readOnly": true`, `"default": "free"`,
				// What an insert has to supply: not nullable, not filled in.
				`"required": ["email", "full_name"]`, `"additionalProperties": false`,
				`"analytics.events": {`,
			},
			mustNot: []string{`"OrderSummary"`},
		},
		{
			name: "graphql", engine: "postgres", req: ORMRequest{Target: ORMGraphQL},
			must: []string{
				"scalar BigInt", "scalar DateTime", "scalar JSON", "scalar UUID",
				"enum OrderStatus {", "enum EventKind {", "  add_to_cart\n",
				`"People who have an account in the shop"`, "type Customer {", "  id: ID!", "  email: String!",
				"  country: String\n", "  tags: [String!]!", "  profile: JSON!", "  created_at: DateTime!",
				"  orders: [Order!]!", "  customer: Customer!", "  parent: Category\n", "  customer_profile: CustomerProfile\n",
				// A name that is not a GraphQL name is renamed, and says what it was.
				`  "Column \"weird column\"."`, "  weird_column: String",
			},
			mustNot: []string{"input ", "type OrderSummary"},
			warned:  []string{`Enum value "add-to-cart" of event_kind is not a GraphQL name`},
		},
		{
			name: "graphql inputs", engine: "postgres", req: ORMRequest{Target: ORMGraphQL, Inputs: ormYes(), Naming: ORMNamingCamel},
			must: []string{
				"input CustomerInput {", "  fullName: String!", "  createdAt: DateTime\n", "  id: ID\n", "  orderItems: [OrderItem!]!",
			},
		},
	} {
		t.Run(e.name, e.run)
	}
}

func TestORMClassTargets(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "typeorm", engine: "postgres", req: ORMRequest{Target: ORMTypeORM},
			must: []string{
				`} from "typeorm"`, "export enum OrderStatus {", `  Pending = "pending",`, `  AddToCart = "add-to-cart",`,
				`@Index("customers_email_key", ["email"], { unique: true })`,
				`@Entity("customers", { schema: "public" })`, "export class Customer {",
				`  @PrimaryGeneratedColumn({ type: "bigint" })`, "  id!: string",
				`  @Column({ type: "enum", enum: CustomerTier, enumName: "customer_tier", default: CustomerTier.Free })`,
				`  @Column({ type: "char", length: 2, nullable: true })`,
				`  @Column({ type: "text", array: true, default: () => "'{}'::text[]" })`,
				`  @Column({ type: "timestamp with time zone", default: () => "now()" })`,
				`  @PrimaryGeneratedColumn("identity", { type: "bigint", generatedIdentity: "ALWAYS" })`,
				`  @PrimaryGeneratedColumn("uuid")`,
				`  @ManyToOne(() => Customer, (other) => other.orders, { onDelete: "CASCADE" })`,
				`  @JoinColumn({ name: "customer_id", referencedColumnName: "id", foreignKeyConstraintName: "orders_customer_id_fkey" })`,
				"  customer!: Relation<Customer>", "  @OneToMany(() => Order, (other) => other.customer)", "  orders!: Relation<Order>[]",
				`  @OneToOne(() => Customer, (other) => other.customer_profile, { onDelete: "CASCADE" })`,
				"  customer_profile!: Relation<CustomerProfile> | null",
				`  @JoinColumn([{ name: "order_id", referencedColumnName: "order_id", foreignKeyConstraintName: "shipments_item_fkey" }, { name: "line_no", referencedColumnName: "line_no" }])`,
				`  @Column({ type: "numeric", precision: 10, scale: 2, nullable: true, generatedType: "STORED", asExpression: "((small)::numeric * 1.5)" })`,
			},
			mustNot: []string{"ViewEntity", "measurements_2024"},
			warned:  []string{"public.audit_log has no primary key. TypeORM needs one", "products_attributes_gin on public.products uses the gin access method"},
		},
		{
			name: "typeorm split", engine: "postgres", file: "Order.ts",
			req: ORMRequest{Target: ORMTypeORM, Split: ormYes(), Naming: ORMNamingCamel, Views: ormYes()},
			must: []string{
				`import { Customer } from "./Customer"`, `import { OrderItem } from "./OrderItem"`,
				`import { OrderStatus } from "./enums"`, "export class Order {",
				`  @Column({ type: "bigint", name: "customer_id" })`, "  customerId!: string",
				`@Index("orders_status_placed_idx", ["status", "placedAt"])`,
			},
		},
		{
			name: "typeorm split index", engine: "postgres", file: "index.ts",
			req:  ORMRequest{Target: ORMTypeORM, Split: ormYes(), Views: ormYes()},
			must: []string{`export * from "./enums"`, `export * from "./Customer"`, `export * from "./OrderSummary"`},
		},
		{
			name: "typeorm views", engine: "postgres", file: "OrderSummary.ts",
			req:  ORMRequest{Target: ORMTypeORM, Split: ormYes(), Views: ormYes()},
			must: []string{`@ViewEntity({ name: "order_summary", schema: "public", synchronize: false })`, "  @ViewColumn()"},
		},
		{
			name: "typeorm mysql", engine: "mysql", req: ORMRequest{Target: ORMTypeORM},
			must: []string{
				`@Entity("authors", { database: "blog" })`,
				`  @PrimaryGeneratedColumn({ type: "int", unsigned: true })`,
				`  @Column({ type: "boolean", default: false })`,
				`  @Column({ type: "enum", enum: PostsStatus, default: PostsStatus.Draft })`,
				`  @Column({ type: "set", enum: ["pinned", "featured"], nullable: true })`,
				`  @Column({ type: "timestamp", default: () => "CURRENT_TIMESTAMP", onUpdate: "CURRENT_TIMESTAMP" })`,
				`  @Column({ type: "datetime", precision: 6, nullable: true })`,
			},
		},
		{
			name: "typeorm oracle", engine: "oracle", req: ORMRequest{Target: ORMTypeORM},
			must: []string{
				`@Entity("CUSTOMERS")`, "export class Customer {", `  @PrimaryGeneratedColumn({ type: "number" })`,
				`  @Column({ type: "varchar2", length: 255 })`, `  @Column({ type: "number", precision: 18, scale: 2, default: 0 })`,
				`  @Column({ type: "date", default: () => "SYSDATE" })`,
				`  @Column({ type: "timestamp with time zone", precision: 6, nullable: true })`,
			},
			// The primary key's own index is not a second unique constraint.
			mustNot: []string{`"CUSTOMERS_PK"`},
		},
		{
			name: "mikroorm", engine: "postgres", req: ORMRequest{Target: ORMMikroORM},
			must: []string{
				`from "@mikro-orm/core"`, `} from "@mikro-orm/decorators/legacy"`,
				`@Entity({ tableName: "customers", schema: "public" })`,
				`@Unique({ name: "customers_email_key", properties: ["email"] })`,
				`  @PrimaryKey({ type: "bigint", columnType: "bigint" })`, "  id!: bigint & Opt",
				`  @Enum({ items: () => CustomerTier, nativeEnumName: "customer_tier", default: "free" })`,
				`  @Property({ type: "datetime", columnType: "timestamp with time zone", defaultRaw: "now()" })`,
				"  created_at!: Date & Opt",
				// The foreign-key column is the relation, not a second property.
				`  @ManyToOne({ entity: () => Customer, fieldName: "customer_id", deleteRule: "cascade" })`,
				"  customer!: Rel<Customer>",
				`  @OneToMany({ entity: () => Order, mappedBy: "customer" })`, "  orders = new Collection<Order>(this)",
				// A key that is a relation is named as one in the key's type.
				`  [PrimaryKeyProp]?: ["order", "line_no"]`,
				`  @ManyToOne({ entity: () => Order, fieldName: "order_id", primary: true, deleteRule: "cascade" })`,
				`  @OneToOne({ entity: () => Customer, fieldName: "customer_id", primary: true, deleteRule: "cascade" })`,
				`  @ManyToOne({ entity: () => OrderItem, fieldNames: ["order_id", "line_no"], deleteRule: "cascade" })`,
				`  @ManyToOne({ entity: () => Category, fieldName: "category_slug", referencedColumnNames: ["slug"], nullable: true })`,
				`@Index({ name: "orders_status_placed_idx", properties: ["status", "placed_at"] })`,
			},
			mustNot: []string{"  customer_id!: bigint\n"},
			warned:  []string{"public.audit_log has no primary key. MikroORM needs one"},
		},
		{
			name: "sequelize", engine: "postgres", req: ORMRequest{Target: ORMSequelize},
			must: []string{
				`} from "sequelize"`,
				"export class Customer extends Model<InferAttributes<Customer>, InferCreationAttributes<Customer>> {",
				"  declare id: CreationOptional<string>", "  declare email: string",
				"  declare country: CreationOptional<string | null>", "  declare orders?: NonAttribute<Order[]>",
				"      id: { type: DataTypes.BIGINT, primaryKey: true, autoIncrement: true, allowNull: false },",
				`      tier: { type: DataTypes.ENUM("free", "plus", "pro"), allowNull: false, defaultValue: "free" },`,
				"      created_at: { type: DataTypes.DATE, allowNull: false, defaultValue: DataTypes.NOW },",
				`      external_id: { type: DataTypes.UUID, allowNull: false, defaultValue: literal("gen_random_uuid()") },`,
				"      tags: { type: DataTypes.ARRAY(DataTypes.TEXT), allowNull: false, defaultValue: literal(\"'{}'::text[]\") },",
				`      tableName: "customers",`, `      schema: "public",`, "      timestamps: false,",
				`        { name: "customers_email_key", unique: true, fields: ["email"] },`,
				"export function initModels(sequelize: Sequelize) {",
				`  Order.belongsTo(Customer, { as: "customer", foreignKey: "customer_id", targetKey: "id", onDelete: "CASCADE", onUpdate: "NO ACTION" })`,
				`  Customer.hasMany(Order, { as: "orders", foreignKey: "customer_id", sourceKey: "id" })`,
				`  Customer.hasOne(CustomerProfile, { as: "customer_profile", foreignKey: "customer_id", sourceKey: "id" })`,
				`  Shipment.belongsTo(Category, { as: "category", foreignKey: "category_slug", targetKey: "slug"`,
				// No primary key: the id Sequelize would invent is taken back out.
				`  AuditLog.removeAttribute("id")`,
			},
			warned: []string{"public.shipments.order_id, line_no is a composite foreign key, which Sequelize associations cannot express"},
		},
		{
			name: "sqlalchemy", engine: "postgres", req: ORMRequest{Target: ORMSQLAlchemy},
			must: []string{
				"from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column, relationship",
				"class Base(DeclarativeBase):\n    pass\n",
				"class Customer(Base):\n    __tablename__ = \"customers\"\n",
				`        UniqueConstraint("email", name="customers_email_key"),`,
				`        {"schema": "public", "comment": "People who have an account in the shop"},`,
				"    id: Mapped[int] = mapped_column(BigInteger, primary_key=True)",
				`    tier: Mapped[str] = mapped_column(Enum("free", "plus", "pro", name="customer_tier", schema="public"), server_default=text("'free'::customer_tier"))`,
				"    country: Mapped[Optional[str]] = mapped_column(CHAR(2))",
				`    tags: Mapped[list[str]] = mapped_column(ARRAY(Text), server_default=text("'{}'::text[]"))`,
				`    created_at: Mapped[datetime.datetime] = mapped_column(DateTime(timezone=True), server_default=text("now()"))`,
				`    customer_id: Mapped[int] = mapped_column(BigInteger, ForeignKey("public.customers.id", ondelete="CASCADE", name="orders_customer_id_fkey"))`,
				`    customer: Mapped["Customer"] = relationship(back_populates="orders", foreign_keys=[customer_id])`,
				`    orders: Mapped[list["Order"]] = relationship(back_populates="customer", foreign_keys="Order.customer_id")`,
				`    parent: Mapped[Optional["Category"]] = relationship(back_populates="categories", foreign_keys=[parent_id], remote_side=[id])`,
				`    customer_profile: Mapped[Optional["CustomerProfile"]] = relationship(back_populates="customer", foreign_keys="CustomerProfile.customer_id", uselist=False)`,
				`        ForeignKeyConstraint(["order_id", "line_no"], ["public.order_items.order_id", "public.order_items.line_no"], ondelete="CASCADE", name="shipments_item_fkey"),`,
				`    order_item: Mapped["OrderItem"] = relationship(back_populates="shipments", foreign_keys=[order_id, line_no])`,
				"    id: Mapped[int] = mapped_column(BigInteger, Identity(always=True), primary_key=True)",
				`    Id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=False)`,
				`    weird_column: Mapped[Optional[str]] = mapped_column("weird column", Text)`,
				`        Index("products_attributes_gin", "attributes", postgresql_using="gin"),`,
				// No key, no mapped class: a Table on the same metadata.
				"t_audit_log = Table(\n    \"audit_log\",\n    Base.metadata,\n",
			},
			mustNot: []string{"class AuditLog", "order_summary"},
			warned:  []string{"public.audit_log has no primary key, so it is declared as a Table"},
		},
		{
			name: "sqlalchemy mysql", engine: "mysql", req: ORMRequest{Target: ORMSQLAlchemy},
			must: []string{
				"from sqlalchemy.dialects.mysql import", `__table_args__ = (`, `{"schema": "blog"`,
				"    id: Mapped[int] = mapped_column(INTEGER(unsigned=True), primary_key=True)",
				`    status: Mapped[str] = mapped_column(Enum("draft", "published", "archived"), server_default=text("'draft'"))`,
				`    flags: Mapped[Optional[set[str]]] = mapped_column(SET("pinned", "featured"))`,
				"    published_at: Mapped[Optional[datetime.datetime]] = mapped_column(DATETIME(fsp=6))",
			},
		},
		{
			name: "django", engine: "postgres", req: ORMRequest{Target: ORMDjango},
			must: []string{
				"from django.db import models", "from django.contrib.postgres.fields import ArrayField",
				"class OrderStatus(models.TextChoices):", `    PENDING = "pending", "Pending"`,
				`    ADD_TO_CART = "add-to-cart", "Add to cart"`,
				"class Customer(models.Model):", "    id = models.BigAutoField(primary_key=True)",
				"    email = models.TextField(unique=True)",
				`    tier = models.CharField(max_length=4, choices=CustomerTier.choices, db_default="free")`,
				"    country = models.CharField(max_length=2, blank=True, null=True)",
				"    tags = ArrayField(models.TextField())", "    external_id = models.UUIDField(db_default=RandomUUID())",
				`    lifetime_value = models.DecimalField(max_digits=12, decimal_places=2, db_default=Decimal("0"))`,
				"    created_at = models.DateTimeField(db_default=Now())",
				// The column is the relation, with the database's own rule.
				`    customer = models.ForeignKey("Customer", models.CASCADE, related_name="orders")`,
				`    parent = models.ForeignKey("self", models.DO_NOTHING, related_name="categories", blank=True, null=True)`,
				`    customer = models.OneToOneField("Customer", models.CASCADE, related_name="customer_profile", primary_key=True)`,
				`    category = models.ForeignKey("Category", models.DO_NOTHING, db_column="category_slug", to_field="slug", related_name="shipments", blank=True, null=True)`,
				`    pk = models.CompositePrimaryKey("order_id", "line_no")`,
				"        managed = False", `        db_table = "public\".\"customers"`,
				`            models.UniqueConstraint(fields=["order", "product"], name="order_items_order_product_key"),`,
				`            models.Index(fields=["status", "-placed_at"], name="orders_status_placed_idx"),`,
				`            GinIndex(fields=["attributes"], name="products_attributes_gin"),`,
				`    search = models.TextField(blank=True, null=True)  # This field type is a guess.`,
			},
			warned: []string{
				"public.audit_log has no primary key. Django will assume an id column",
				"public.shipments.order_id, line_no is a composite foreign key, which Django has no field for",
			},
		},
		{
			name: "django managed", engine: "postgres", req: ORMRequest{Target: ORMDjango, Managed: ormYes()},
			must:    []string{"        managed = True", "These models are managed"},
			mustNot: []string{"managed = False"},
		},
		{
			name: "gorm", engine: "postgres", req: ORMRequest{Target: ORMGorm},
			must: []string{
				"package models", `"gorm.io/datatypes"`, `"github.com/lib/pq"`,
				"type Customer struct {",
				"`gorm:\"column:id;type:bigint;primaryKey;autoIncrement\" json:\"id\"`",
				"`gorm:\"column:email;type:text;not null;uniqueIndex:customers_email_key\" json:\"email\"`",
				"`gorm:\"column:tier;type:customer_tier;not null;default:free\" json:\"tier\"`",
				"`gorm:\"column:created_at;type:timestamp with time zone;not null;default:now()\" json:\"created_at\"`",
				"pq.StringArray", "datatypes.JSON", "*time.Time",
				"`gorm:\"foreignKey:CustomerID;references:ID;constraint:OnDelete:CASCADE\" json:\"customer,omitempty\"`",
				"Orders              []Order",
				"`gorm:\"foreignKey:OrderID,LineNo;references:OrderID,LineNo;constraint:OnDelete:CASCADE\" json:\"order_item,omitempty\"`",
				"`gorm:\"column:order_id;type:bigint;primaryKey;uniqueIndex:order_items_order_product_key,priority:1\" json:\"order_id\"`",
				"`gorm:\"column:Id;type:integer;primaryKey;autoIncrement:false\" json:\"Id\"`",
				`func (Customer) TableName() string { return "public.customers" }`,
			},
			warned: []string{"products_attributes_gin on public.products uses the gin access method"},
		},
		{
			name: "gorm plain", engine: "postgres",
			req:  ORMRequest{Target: ORMGorm, Relations: ormNo(), JSONTags: ormNo(), Package: "store"},
			must: []string{"package store", "`gorm:\"column:email;type:text;not null;uniqueIndex:customers_email_key\"`"},
			mustNot: []string{
				"json:", "foreignKey:", "[]Order",
			},
		},
		{
			name: "diesel schema", engine: "postgres", req: ORMRequest{Target: ORMDiesel},
			must: []string{
				"pub mod sql_types {", `    #[diesel(postgres_type(name = "order_status"))]`, "    pub struct OrderStatus;",
				"diesel::table! {\n    use diesel::sql_types::*;\n    use super::sql_types::CustomerTier;\n\n    customers (id) {",
				"        id -> Int8,", "        tier -> CustomerTier,", "        #[max_length = 2]\n        country -> Nullable<Bpchar>,",
				"        tags -> Array<Nullable<Text>>,", "        created_at -> Timestamptz,",
				"    order_items (order_id, line_no) {",
				"    #[sql_name = \"Mixed Case Table\"]\n    mixed_case_table (id) {",
				"        #[sql_name = \"Id\"]\n        id -> Int4,",
				"diesel::joinable!(orders -> customers (customer_id));",
				"diesel::allow_tables_to_appear_in_same_query!(",
				// A schema other than the default is a module, as print-schema has it.
				"pub mod analytics {", `        #[diesel(postgres_type(name = "event_kind", schema = "analytics"))]`,
				"        analytics.events (id) {",
			},
			mustNot: []string{
				"audit_log", "measurements_2024",
				// Two keys to one parent give Diesel no single join to assume.
				"diesel::joinable!(messages -> customers",
				// Nor does a table's key to itself.
				"diesel::joinable!(categories -> categories",
			},
			warned: []string{"public.audit_log has no primary key. Diesel cannot describe a table without one"},
		},
		{
			name: "diesel models", engine: "postgres", req: ORMRequest{Target: ORMDiesel}, file: "models.rs",
			must: []string{
				"use diesel::prelude::*;",
				"#[diesel(sql_type = crate::schema::sql_types::OrderStatus)]\npub enum OrderStatus {\n    Pending,",
				"impl diesel::serialize::ToSql<crate::schema::sql_types::OrderStatus, diesel::pg::Pg> for OrderStatus {",
				`            OrderStatus::Pending => out.write_all(b"pending")?,`,
				`            b"add-to-cart" => Ok(EventKind::AddToCart),`,
				"#[derive(Queryable, Selectable, Identifiable, Associations, Debug)]\n#[diesel(table_name = crate::schema::orders)]\n#[diesel(belongs_to(Customer, foreign_key = customer_id))]\n#[diesel(check_for_backend(diesel::pg::Pg))]\npub struct Order {",
				"    pub id: i64,", "    pub status: OrderStatus,", "    pub shipped_at: Option<chrono::DateTime<chrono::Utc>>,",
				"#[diesel(primary_key(order_id, line_no))]", "    pub tags: Vec<Option<String>>,",
				"#[diesel(table_name = crate::schema::analytics::events)]",
				"    // search (tsvector) has no Rust type here and is not selected.",
			},
		},
		{
			name: "eloquent", engine: "postgres", req: ORMRequest{Target: ORMEloquent},
			must: []string{
				"<?php\n", "namespace App\\Models;\n", "use Illuminate\\Database\\Eloquent\\Model;",
				"enum OrderStatus: string\n{\n    case Pending = 'pending';", "    case AddToCart = 'add-to-cart';",
				" * People who have an account in the shop", " * @property string|null $country",
				" * @property-read \\Illuminate\\Database\\Eloquent\\Collection<int, Order> $orders",
				"class Customer extends Model\n{\n    protected $table = 'public.customers';",
				"    public $timestamps = false;",
				"    protected $fillable = [\n        'email',\n        'full_name',",
				"        'marketing_opt_in' => 'boolean',", "        'tier' => CustomerTier::class,", "        'profile' => 'array',",
				"        'lifetime_value' => 'decimal:2',", "        'created_at' => 'datetime',",
				"    public function orders(): HasMany\n    {\n        return $this->hasMany(Order::class, 'customer_id', 'id');\n    }",
				"    public function customer(): BelongsTo\n    {\n        return $this->belongsTo(Customer::class, 'customer_id', 'id');\n    }",
				"    public function customerProfile(): HasOne",
				"        return $this->belongsTo(Category::class, 'category_slug', 'slug');",
				// A key that is not an auto-numbered integer called id has to be said.
				"    protected $primaryKey = 'customer_id';\n\n    public $incrementing = false;",
				"    protected $keyType = 'string';",
				"    protected $primaryKey = null;",
			},
			warned: []string{
				"public.order_items has a composite primary key, which Eloquent does not support",
				"public.shipments.order_id, line_no is a composite foreign key, which Eloquent relations cannot express",
			},
		},
		{
			name: "eloquent split", engine: "postgres", req: ORMRequest{Target: ORMEloquent, Split: ormYes()}, file: "Order.php",
			must:    []string{"<?php\n", "namespace App\\Models;\n", "class Order extends Model", "use Illuminate\\Database\\Eloquent\\Relations\\BelongsTo;"},
			mustNot: []string{"class Customer ", "enum "},
		},
	} {
		t.Run(e.name, e.run)
	}
}

func TestORMSQL(t *testing.T) {
	for _, e := range []ormExpectation{
		{
			name: "postgres", engine: "postgres", req: ORMRequest{Target: ORMSQL},
			must: []string{
				`CREATE SCHEMA "analytics";`,
				`CREATE TYPE "public"."order_status" AS ENUM ('pending', 'paid', 'shipped', 'delivered', 'cancelled', 'refunded');`,
				`CREATE TABLE "public"."customers" (`, `  "id" bigserial NOT NULL,`,
				`  "tier" customer_tier DEFAULT 'free'::customer_tier NOT NULL,`,
				`  "tags" text[] DEFAULT '{}'::text[] NOT NULL,`,
				`  CONSTRAINT "customers_pkey" PRIMARY KEY ("id")`,
				`  "id" bigint GENERATED ALWAYS AS IDENTITY NOT NULL,`,
				`  "total" numeric(10,2) GENERATED ALWAYS AS (((small)::numeric * 1.5)) STORED,`,
				`  CONSTRAINT "order_items_quantity_check" CHECK ((quantity > 0))`,
				`) PARTITION BY RANGE (taken_at);`,
				`COMMENT ON TABLE "public"."customers" IS 'People who have an account in the shop';`,
				// PostgreSQL's own definitions, expression and predicate included.
				"CREATE UNIQUE INDEX customers_lower_email_idx ON public.customers USING btree (lower(email));",
				"CREATE INDEX customers_active_idx ON public.customers USING btree (last_seen_at) WHERE (last_seen_at IS NOT NULL);",
				"CREATE INDEX products_attributes_gin ON public.products USING gin (attributes);",
				// Foreign keys last, so the order of the tables does not matter.
				`ALTER TABLE "public"."orders" ADD CONSTRAINT "orders_customer_id_fkey" FOREIGN KEY ("customer_id") REFERENCES "public"."customers" ("id") ON DELETE CASCADE;`,
				`ALTER TABLE "public"."shipments" ADD CONSTRAINT "shipments_item_fkey" FOREIGN KEY ("order_id", "line_no") REFERENCES "public"."order_items" ("order_id", "line_no") ON DELETE CASCADE;`,
			},
			mustNot: []string{"CREATE VIEW", "measurements_2024", "IF NOT EXISTS", "nextval("},
			warned:  []string{"public.measurements is partitioned: its partitions are not in this script"},
		},
		{
			name: "postgres guarded", engine: "postgres", req: ORMRequest{Target: ORMSQL, IfNotExists: ormYes(), Views: ormYes()},
			must: []string{
				`CREATE SCHEMA IF NOT EXISTS "analytics";`, `CREATE TABLE IF NOT EXISTS "public"."customers" (`,
				"DO $$ BEGIN\n  CREATE TYPE \"public\".\"order_status\" AS ENUM",
				"EXCEPTION WHEN duplicate_object THEN NULL;\nEND $$;",
				"CREATE UNIQUE INDEX IF NOT EXISTS customers_lower_email_idx ON public.customers",
				`CREATE OR REPLACE VIEW "public"."order_summary" AS`,
				`CREATE MATERIALIZED VIEW IF NOT EXISTS "analytics"."top_products" AS`,
			},
		},
		{
			name: "mysql", engine: "mysql", req: ORMRequest{Target: ORMSQL},
			must: []string{
				"SET FOREIGN_KEY_CHECKS = 0;", "CREATE DATABASE IF NOT EXISTS `blog`;", "USE `blog`;", "USE `stats`;",
				// The engine's own statement, word for word.
				"CREATE TABLE `posts` (\n  `id` bigint NOT NULL AUTO_INCREMENT,",
				"  FULLTEXT KEY `ft_posts_body` (`body`),",
				") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Everything that has been written';",
				"SET FOREIGN_KEY_CHECKS = 1;",
			},
		},
		{
			name: "sqlite", engine: "sqlite", req: ORMRequest{Target: ORMSQL},
			must: []string{
				"CREATE TABLE notes (\n  id INTEGER PRIMARY KEY AUTOINCREMENT,",
				`CREATE INDEX "idx_notes_user" ON "notes" ("user_id");`,
			},
			mustNot: []string{"sqlite_autoindex"},
		},
		{
			name: "sqlserver", engine: "sqlserver", req: ORMRequest{Target: ORMSQL},
			must: []string{
				"IF SCHEMA_ID(N'sales') IS NULL EXEC(N'CREATE SCHEMA [sales]');",
				"CREATE TABLE [dbo].[accounts] (", "  [id] int IDENTITY(1,1) NOT NULL,",
				"  [guid] uniqueidentifier DEFAULT (newid()) NOT NULL,", "  [notes] nvarchar(MAX) NULL,",
				"  [version] rowversion NOT NULL,", "  CONSTRAINT [PK_accounts] PRIMARY KEY ([id])",
				"CREATE UNIQUE INDEX [UQ_accounts_guid] ON [dbo].[accounts] ([guid]);",
				"ALTER TABLE [dbo].[transfers] ADD CONSTRAINT [FK_transfers_from] FOREIGN KEY ([from_account]) REFERENCES [dbo].[accounts] ([id]) ON DELETE CASCADE;",
			},
		},
		{
			// Each guard asks about the object where it will be made: the
			// constraint in its table's schema, like the table and the index.
			name: "sqlserver guarded", engine: "sqlserver", req: ORMRequest{Target: ORMSQL, IfNotExists: ormYes()},
			must: []string{
				"IF OBJECT_ID(N'[dbo].[accounts]', N'U') IS NULL\nCREATE TABLE [dbo].[accounts] (",
				"IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'UQ_accounts_guid' AND object_id = OBJECT_ID(N'[dbo].[accounts]'))\nCREATE UNIQUE INDEX [UQ_accounts_guid]",
				"IF OBJECT_ID(N'[dbo].[FK_transfers_from]', N'F') IS NULL\nALTER TABLE [dbo].[transfers] ADD CONSTRAINT [FK_transfers_from]",
			},
			mustNot: []string{"OBJECT_ID(N'[FK_transfers_from]'"},
		},
		{
			name: "clickhouse", engine: "clickhouse", req: ORMRequest{Target: ORMSQL},
			must: []string{"CREATE TABLE analytics.page_views\n(", "ENGINE = MergeTree", "ORDER BY (ts, user_id)", "SETTINGS index_granularity = 8192;"},
		},
		{
			name: "oracle", engine: "oracle", req: ORMRequest{Target: ORMSQL},
			// No statement was read for the fixture, so one is assembled — and says so.
			must:   []string{`CREATE TABLE "CUSTOMERS" (`, `  "EMAIL" VARCHAR2(255) NOT NULL,`, `  CONSTRAINT "CUSTOMERS_PK" PRIMARY KEY ("ID")`},
			warned: []string{"CUSTOMERS: the engine returned no CREATE statement"},
		},
	} {
		t.Run(e.name, e.run)
	}
}
