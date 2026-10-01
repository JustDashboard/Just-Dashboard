package dbx

// The schema every generator is tested against.
//
// It is written out as the structure a live catalogue fills in, not read from
// a database, so the generator tests run anywhere. It is meant to be awkward
// in every way a real schema is: a serial key and an identity key, a composite
// key, a table with no key at all, a self-reference, two keys to one parent, a
// one-to-one, a key that crosses schemas, enums in two schemas (one with a
// label that is not an identifier), every common column type, defaults of each
// kind, a partial and an expression index, a reserved word and a name with a
// space in it, a view, a materialized view, a partitioned table with one of
// its partitions, and the same table name in two schemas.

func ormColumn(name, typ string, opts ...func(*ORMColumn)) ORMColumn {
	c := ORMColumn{Name: name, Type: typ}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func ormNull(c *ORMColumn)                 { c.Nullable = true }
func ormAuto(c *ORMColumn)                 { c.AutoIncrement = true }
func ormDef(expr string) func(*ORMColumn)  { return func(c *ORMColumn) { c.Default = expr } }
func ormNote(text string) func(*ORMColumn) { return func(c *ORMColumn) { c.Comment = text } }
func ormEnumOf(schema, name string) func(*ORMColumn) {
	return func(c *ORMColumn) { c.EnumSchema, c.EnumName = schema, name }
}
func ormIdentity(kind string) func(*ORMColumn) {
	return func(c *ORMColumn) { c.Identity, c.AutoIncrement = kind, true }
}
func ormGenerated(expr string) func(*ORMColumn) {
	return func(c *ORMColumn) { c.Generated, c.GeneratedExpr = true, expr }
}

func ormPK(name string, cols ...string) ORMIndex {
	return ORMIndex{Name: name, Columns: cols, Unique: true, Primary: true}
}

func ormUnique(name string, cols ...string) ORMIndex {
	return ORMIndex{Name: name, Columns: cols, Unique: true}
}

func ormIndex(name string, cols ...string) ORMIndex {
	return ORMIndex{Name: name, Columns: cols}
}

func ormFK(name string, cols []string, refSchema, refTable string, refCols []string, onDelete string) ForeignKey {
	return ForeignKey{
		Name: name, Columns: cols, RefSchema: refSchema, RefTable: refTable, RefColumns: refCols,
		OnUpdate: "NO ACTION", OnDelete: onDelete,
	}
}

// ormPostgresFixture is the shop: two schemas, three enums, and a table for
// every awkward shape named above.
func ormPostgresFixture() *ORMSchema {
	return &ORMSchema{
		Driver:   DriverPostgres,
		Detailed: true,
		Enums: []ORMEnum{
			{Schema: "analytics", Name: "event_kind", Values: []string{"page_view", "click", "add-to-cart"}},
			{Schema: "public", Name: "customer_tier", Values: []string{"free", "plus", "pro"}},
			{Schema: "public", Name: "order_status", Values: []string{"pending", "paid", "shipped", "delivered", "cancelled", "refunded"}},
		},
		Tables: []ORMTable{
			{
				Schema: "analytics", Name: "daily_revenue", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("day", "date"),
					ormColumn("orders", "integer"),
					ormColumn("revenue_cents", "bigint"),
				},
				PrimaryKey: []string{"day"},
				Indexes:    []ORMIndex{ormPK("daily_revenue_pkey", "day")},
			},
			{
				Schema: "analytics", Name: "events", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormIdentity("always")),
					ormColumn("customer_id", "bigint", ormNull),
					ormColumn("kind", "analytics.event_kind", ormEnumOf("analytics", "event_kind")),
					ormColumn("path", "text", ormNull),
					ormColumn("duration_ms", "integer", ormNull),
					ormColumn("props", "jsonb", ormNull),
					ormColumn("occurred_at", "timestamp with time zone"),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("events_pkey", "id"),
					ormIndex("events_occurred_idx", "occurred_at"),
				},
				ForeignKeys: []ForeignKey{
					ormFK("events_customer_id_fkey", []string{"customer_id"}, "public", "customers", []string{"id"}, "SET NULL"),
				},
			},
			{
				Schema: "analytics", Name: "top_products", Kind: ORMKindMatView,
				Columns: []ORMColumn{
					ormColumn("product_id", "bigint", ormNull),
					ormColumn("units", "bigint", ormNull),
				},
				CreateSQL: " SELECT product_id,\n    sum(quantity) AS units\n   FROM order_items\n  GROUP BY product_id;",
			},
			{
				Schema: "public", Name: "Mixed Case Table", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("Id", "integer"),
					ormColumn("select", "text", ormNull),
					ormColumn("weird column", "text", ormNull),
					ormColumn("2fa", "boolean", ormDef("false")),
				},
				PrimaryKey: []string{"Id"},
				Indexes:    []ORMIndex{ormPK("Mixed Case Table_pkey", "Id")},
			},
			{
				Schema: "public", Name: "audit_log", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("at", "timestamp with time zone", ormDef("now()")),
					ormColumn("actor", "text", ormNull),
					ormColumn("action", "text"),
					ormColumn("payload", "jsonb", ormNull),
				},
			},
			{
				Schema: "public", Name: "categories", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "integer", ormDef("nextval('categories_id_seq'::regclass)")),
					ormColumn("slug", "text"),
					ormColumn("name", "text"),
					ormColumn("parent_id", "integer", ormNull),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("categories_pkey", "id"),
					ormUnique("categories_slug_key", "slug"),
				},
				ForeignKeys: []ForeignKey{
					ormFK("categories_parent_id_fkey", []string{"parent_id"}, "public", "categories", []string{"id"}, "NO ACTION"),
				},
			},
			{
				Schema: "public", Name: "customer_profiles", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("customer_id", "bigint"),
					ormColumn("bio", "text", ormNull),
					ormColumn("website", "character varying(255)", ormNull),
				},
				PrimaryKey: []string{"customer_id"},
				Indexes:    []ORMIndex{ormPK("customer_profiles_pkey", "customer_id")},
				ForeignKeys: []ForeignKey{
					ormFK("customer_profiles_customer_id_fkey", []string{"customer_id"}, "public", "customers", []string{"id"}, "CASCADE"),
				},
			},
			{
				Schema: "public", Name: "customers", Kind: ORMKindTable,
				Comment: "People who have an account in the shop",
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormDef("nextval('customers_id_seq'::regclass)")),
					ormColumn("email", "text"),
					ormColumn("full_name", "text"),
					ormColumn("tier", "customer_tier", ormEnumOf("public", "customer_tier"), ormDef("'free'::customer_tier")),
					ormColumn("country", "character(2)", ormNull),
					ormColumn("marketing_opt_in", "boolean", ormDef("false")),
					ormColumn("tags", "text[]", ormDef("'{}'::text[]")),
					ormColumn("profile", "jsonb", ormDef("'{}'::jsonb"), ormNote("Free-form preferences captured at sign-up")),
					ormColumn("avatar", "bytea", ormNull),
					ormColumn("external_id", "uuid", ormDef("gen_random_uuid()")),
					ormColumn("lifetime_value", "numeric(12,2)", ormDef("0")),
					ormColumn("created_at", "timestamp with time zone", ormDef("now()")),
					ormColumn("last_seen_at", "timestamp with time zone", ormNull),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("customers_pkey", "id"),
					ormUnique("customers_email_key", "email"),
					{Name: "customers_lower_email_idx", Columns: []string{}, Unique: true, Expression: true,
						Definition: "CREATE UNIQUE INDEX customers_lower_email_idx ON public.customers USING btree (lower(email))"},
					{Name: "customers_active_idx", Columns: []string{"last_seen_at"}, Partial: true,
						Definition: "CREATE INDEX customers_active_idx ON public.customers USING btree (last_seen_at) WHERE (last_seen_at IS NOT NULL)"},
				},
			},
			{
				Schema: "public", Name: "events", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormIdentity("by default")),
					ormColumn("name", "character varying(120)"),
					ormColumn("starts_at", "timestamp without time zone"),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("events_pkey", "id")},
			},
			{
				Schema: "public", Name: "measurements", Kind: ORMKindPartitioned,
				Columns: []ORMColumn{
					ormColumn("sensor_id", "integer"),
					ormColumn("taken_at", "timestamp with time zone"),
					ormColumn("reading", "double precision", ormNull),
				},
				PrimaryKey:   []string{"sensor_id", "taken_at"},
				Indexes:      []ORMIndex{ormPK("measurements_pkey", "sensor_id", "taken_at")},
				PartitionKey: "RANGE (taken_at)",
			},
			{
				Schema: "public", Name: "measurements_2024", Kind: ORMKindPartition,
				Columns: []ORMColumn{
					ormColumn("sensor_id", "integer"),
					ormColumn("taken_at", "timestamp with time zone"),
					ormColumn("reading", "double precision", ormNull),
				},
				PrimaryKey: []string{"sensor_id", "taken_at"},
			},
			{
				Schema: "public", Name: "messages", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "uuid", ormDef("gen_random_uuid()")),
					ormColumn("sender_id", "bigint"),
					ormColumn("recipient_id", "bigint", ormNull),
					ormColumn("body", "text"),
					ormColumn("sent_at", "timestamp(3) with time zone", ormDef("now()")),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("messages_pkey", "id")},
				ForeignKeys: []ForeignKey{
					ormFK("messages_recipient_id_fkey", []string{"recipient_id"}, "public", "customers", []string{"id"}, "SET NULL"),
					ormFK("messages_sender_id_fkey", []string{"sender_id"}, "public", "customers", []string{"id"}, "CASCADE"),
				},
			},
			{
				Schema: "public", Name: "order_items", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("order_id", "bigint"),
					ormColumn("line_no", "smallint"),
					ormColumn("product_id", "bigint"),
					ormColumn("quantity", "integer", ormDef("1")),
					ormColumn("unit_price_cents", "integer"),
				},
				PrimaryKey: []string{"order_id", "line_no"},
				Indexes: []ORMIndex{
					ormPK("order_items_pkey", "order_id", "line_no"),
					ormUnique("order_items_order_product_key", "order_id", "product_id"),
				},
				ForeignKeys: []ForeignKey{
					ormFK("order_items_order_id_fkey", []string{"order_id"}, "public", "orders", []string{"id"}, "CASCADE"),
					ormFK("order_items_product_id_fkey", []string{"product_id"}, "public", "products", []string{"id"}, "NO ACTION"),
				},
				Checks: []ORMCheck{{Name: "order_items_quantity_check", Definition: "CHECK ((quantity > 0))"}},
			},
			{
				Schema: "public", Name: "order_summary", Kind: ORMKindView,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormNull),
					ormColumn("email", "text", ormNull),
					ormColumn("status", "order_status", ormNull, ormEnumOf("public", "order_status")),
					ormColumn("total_cents", "bigint", ormNull),
				},
				CreateSQL: " SELECT o.id,\n    c.email,\n    o.status,\n    o.total_cents\n   FROM (orders o\n     JOIN customers c ON ((c.id = o.customer_id)));",
			},
			{
				Schema: "public", Name: "orders", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormDef("nextval('orders_id_seq'::regclass)")),
					ormColumn("customer_id", "bigint"),
					ormColumn("status", "order_status", ormEnumOf("public", "order_status"), ormDef("'pending'::order_status")),
					ormColumn("total_cents", "bigint", ormDef("0")),
					ormColumn("currency", "character(3)", ormDef("'EUR'::bpchar")),
					ormColumn("shipping_address", "jsonb", ormNull),
					ormColumn("note", "text", ormNull),
					ormColumn("placed_at", "timestamp with time zone", ormDef("now()")),
					ormColumn("shipped_at", "timestamp with time zone", ormNull),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("orders_pkey", "id"),
					{Name: "orders_status_placed_idx", Columns: []string{"status", "placed_at"}, Desc: []bool{false, true},
						Definition: "CREATE INDEX orders_status_placed_idx ON public.orders USING btree (status, placed_at DESC)"},
				},
				ForeignKeys: []ForeignKey{
					ormFK("orders_customer_id_fkey", []string{"customer_id"}, "public", "customers", []string{"id"}, "CASCADE"),
				},
			},
			{
				Schema: "public", Name: "products", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormDef("nextval('products_id_seq'::regclass)")),
					ormColumn("sku", "character varying(32)"),
					ormColumn("name", "character varying(200)"),
					ormColumn("description", "text", ormNull),
					ormColumn("category_id", "integer", ormNull),
					ormColumn("price_cents", "integer"),
					ormColumn("stock", "integer", ormDef("0")),
					ormColumn("attributes", "jsonb", ormDef("'{}'::jsonb")),
					ormColumn("weight_kg", "double precision", ormNull),
					ormColumn("discontinued", "boolean", ormDef("false")),
					ormColumn("released_on", "date", ormNull),
					ormColumn("updated_at", "timestamp without time zone", ormDef("now()")),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("products_pkey", "id"),
					ormUnique("products_sku_key", "sku"),
					{Name: "products_category_idx", Columns: []string{"category_id"},
						Definition: "CREATE INDEX products_category_idx ON public.products USING btree (category_id)"},
					{Name: "products_attributes_gin", Columns: []string{"attributes"}, Method: "gin",
						Definition: "CREATE INDEX products_attributes_gin ON public.products USING gin (attributes)"},
				},
				ForeignKeys: []ForeignKey{
					ormFK("products_category_id_fkey", []string{"category_id"}, "public", "categories", []string{"id"}, "NO ACTION"),
				},
			},
			{
				// A composite foreign key, and one that points at a unique column
				// rather than a primary key: the two shapes several ORMs cannot say.
				Schema: "public", Name: "shipments", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "integer", ormDef("nextval('shipments_id_seq'::regclass)")),
					ormColumn("order_id", "bigint"),
					ormColumn("line_no", "smallint"),
					ormColumn("category_slug", "text", ormNull),
					ormColumn("tracking_code", "character varying(40)"),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("shipments_pkey", "id"),
					ormUnique("shipments_tracking_code_key", "tracking_code"),
				},
				ForeignKeys: []ForeignKey{
					ormFK("shipments_category_slug_fkey", []string{"category_slug"}, "public", "categories", []string{"slug"}, "NO ACTION"),
					ormFK("shipments_item_fkey", []string{"order_id", "line_no"}, "public", "order_items", []string{"order_id", "line_no"}, "CASCADE"),
				},
			},
			{
				Schema: "public", Name: "type_samples", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "integer", ormIdentity("by default")),
					ormColumn("small", "smallint", ormDef("0")),
					ormColumn("ratio", "real", ormNull),
					ormColumn("price", "money", ormNull),
					ormColumn("amount", "numeric", ormNull),
					ormColumn("code", "character varying(16)", ormDef("'N/A'::character varying")),
					ormColumn("fixed", "character(4)", ormNull),
					ormColumn("starts", "time without time zone", ormNull),
					ormColumn("starts_tz", "time with time zone", ormNull),
					ormColumn("span", "interval", ormNull),
					ormColumn("addr", "inet", ormNull),
					ormColumn("doc", "xml", ormNull),
					ormColumn("flags", "bit(8)", ormNull),
					ormColumn("search", "tsvector", ormNull),
					ormColumn("scores", "integer[]", ormNull),
					ormColumn("day", "date", ormDef("CURRENT_DATE")),
					ormColumn("meta", "json", ormNull),
					ormColumn("label", "text", ormDef("'it''s'::text")),
					ormColumn("total", "numeric(10,2)", ormNull, ormGenerated("((small)::numeric * 1.5)")),
					ormColumn("stamp", "timestamp(3) without time zone", ormDef("timezone('utc'::text, now())")),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("type_samples_pkey", "id")},
			},
		},
	}
}

// ormMySQLFixture is a blog across two databases, in MySQL's own spellings.
func ormMySQLFixture() *ORMSchema {
	return &ORMSchema{
		Driver:   DriverMySQL,
		Detailed: true,
		Tables: []ORMTable{
			{
				Schema: "blog", Name: "authors", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "int unsigned", ormAuto),
					ormColumn("handle", "varchar(40)"),
					ormColumn("display_name", "varchar(120)"),
					ormColumn("bio", "text", ormNull),
					ormColumn("is_staff", "tinyint(1)", ormDef("0")),
					ormColumn("joined_at", "datetime", ormDef("CURRENT_TIMESTAMP"), func(c *ORMColumn) { c.DefaultExpr = true }),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PRIMARY", "id"), ormUnique("handle", "handle")},
				CreateSQL:  "CREATE TABLE `authors` (\n  `id` int unsigned NOT NULL AUTO_INCREMENT,\n  `handle` varchar(40) NOT NULL,\n  `display_name` varchar(120) NOT NULL,\n  `bio` text,\n  `is_staff` tinyint(1) NOT NULL DEFAULT '0',\n  `joined_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,\n  PRIMARY KEY (`id`),\n  UNIQUE KEY `handle` (`handle`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
			},
			{
				Schema: "blog", Name: "events", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormAuto),
					ormColumn("title", "varchar(200)"),
					ormColumn("held_in", "year", ormNull),
					// A string that looks like a call, as MySQL prints it: bare,
					// and with nothing in EXTRA to say it is an expression.
					ormColumn("colour", "varchar(32)", ormDef("rgb(0,0,0)")),
					// And a call that is one: flagged, and printed without the
					// parentheses it has to be written back in.
					ormColumn("details", "json", ormNull, ormDef("json_object()"), func(c *ORMColumn) { c.DefaultExpr = true }),
					// Labels are data; their case is not the generator's to change.
					ormColumn("audience", "enum('Public','Members')", ormDef("Public")),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PRIMARY", "id")},
				CreateSQL:  "CREATE TABLE `events` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  `title` varchar(200) NOT NULL,\n  `held_in` year DEFAULT NULL,\n  `colour` varchar(32) NOT NULL DEFAULT 'rgb(0,0,0)',\n  `details` json DEFAULT (json_object()),\n  `audience` enum('Public','Members') NOT NULL DEFAULT 'Public',\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
			},
			{
				Schema: "blog", Name: "post_tags", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("post_id", "bigint"),
					ormColumn("tag", "varchar(40)"),
				},
				PrimaryKey: []string{"post_id", "tag"},
				Indexes:    []ORMIndex{ormPK("PRIMARY", "post_id", "tag")},
				ForeignKeys: []ForeignKey{
					ormFK("fk_post_tags_post", []string{"post_id"}, "blog", "posts", []string{"id"}, "CASCADE"),
				},
				CreateSQL: "CREATE TABLE `post_tags` (\n  `post_id` bigint NOT NULL,\n  `tag` varchar(40) NOT NULL,\n  PRIMARY KEY (`post_id`,`tag`),\n  CONSTRAINT `fk_post_tags_post` FOREIGN KEY (`post_id`) REFERENCES `posts` (`id`) ON DELETE CASCADE\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
			},
			{
				Schema: "blog", Name: "posts", Kind: ORMKindTable, Comment: "Everything that has been written",
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormAuto),
					ormColumn("author_id", "int unsigned"),
					ormColumn("slug", "varchar(160)"),
					ormColumn("title", "varchar(200)"),
					ormColumn("body", "mediumtext", ormNull),
					ormColumn("status", "enum('draft','published','archived')", ormDef("draft")),
					ormColumn("flags", "set('pinned','featured')", ormNull),
					ormColumn("views", "int unsigned", ormDef("0")),
					ormColumn("rating", "decimal(4,2)", ormNull),
					ormColumn("meta", "json", ormNull),
					ormColumn("cover", "mediumblob", ormNull),
					ormColumn("digest", "binary(16)", ormNull),
					ormColumn("published_at", "datetime(6)", ormNull),
					ormColumn("updated_at", "timestamp", ormDef("CURRENT_TIMESTAMP"),
						func(c *ORMColumn) { c.DefaultExpr, c.OnUpdateNow = true, true }),
				},
				PrimaryKey: []string{"id"},
				Indexes: []ORMIndex{
					ormPK("PRIMARY", "id"),
					ormUnique("slug", "slug"),
					ormIndex("idx_posts_status", "status", "published_at"),
					{Name: "ft_posts_body", Columns: []string{"body"}, Method: "fulltext"},
				},
				ForeignKeys: []ForeignKey{
					ormFK("fk_posts_author", []string{"author_id"}, "blog", "authors", []string{"id"}, "RESTRICT"),
				},
				CreateSQL: "CREATE TABLE `posts` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  `author_id` int unsigned NOT NULL,\n  `slug` varchar(160) NOT NULL,\n  `title` varchar(200) NOT NULL,\n  `body` mediumtext,\n  `status` enum('draft','published','archived') NOT NULL DEFAULT 'draft',\n  `flags` set('pinned','featured') DEFAULT NULL,\n  `views` int unsigned NOT NULL DEFAULT '0',\n  `rating` decimal(4,2) DEFAULT NULL,\n  `meta` json DEFAULT NULL,\n  `cover` mediumblob,\n  `digest` binary(16) DEFAULT NULL,\n  `published_at` datetime(6) DEFAULT NULL,\n  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,\n  PRIMARY KEY (`id`),\n  UNIQUE KEY `slug` (`slug`),\n  KEY `idx_posts_status` (`status`,`published_at`),\n  KEY `fk_posts_author` (`author_id`),\n  FULLTEXT KEY `ft_posts_body` (`body`),\n  CONSTRAINT `fk_posts_author` FOREIGN KEY (`author_id`) REFERENCES `authors` (`id`) ON DELETE RESTRICT\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci COMMENT='Everything that has been written'",
			},
			{
				Schema: "blog", Name: "published_posts", Kind: ORMKindView,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormDef("0")),
					ormColumn("title", "varchar(200)"),
				},
			},
			{
				Schema: "stats", Name: "events", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormAuto),
					ormColumn("post_id", "bigint", ormNull),
					ormColumn("kind", "enum('view','share')"),
					ormColumn("seen_at", "timestamp(3)", ormDef("CURRENT_TIMESTAMP(3)"), func(c *ORMColumn) { c.DefaultExpr = true }),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PRIMARY", "id"), ormIndex("idx_events_post", "post_id")},
				ForeignKeys: []ForeignKey{
					ormFK("fk_events_post", []string{"post_id"}, "blog", "posts", []string{"id"}, "SET NULL"),
				},
				CreateSQL: "CREATE TABLE `events` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  `post_id` bigint DEFAULT NULL,\n  `kind` enum('view','share') NOT NULL,\n  `seen_at` timestamp(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),\n  PRIMARY KEY (`id`),\n  KEY `idx_events_post` (`post_id`),\n  CONSTRAINT `fk_events_post` FOREIGN KEY (`post_id`) REFERENCES `blog`.`posts` (`id`) ON DELETE SET NULL\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci",
			},
		},
	}
}

// ormSQLiteFixture is one file: SQLite has no schemas to collide.
func ormSQLiteFixture() *ORMSchema {
	return &ORMSchema{
		Driver:   DriverSQLite,
		Detailed: true,
		Tables: []ORMTable{
			{
				Schema: "main", Name: "notes", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "INTEGER", ormNull),
					ormColumn("user_id", "INTEGER"),
					ormColumn("title", "VARCHAR(120)"),
					ormColumn("body", "TEXT", ormNull),
					ormColumn("pinned", "BOOLEAN", ormDef("0")),
					ormColumn("score", "REAL", ormNull),
					ormColumn("price", "DECIMAL(10,2)", ormNull),
					ormColumn("payload", "JSON", ormNull),
					ormColumn("attachment", "BLOB", ormNull),
					ormColumn("created_at", "DATETIME", ormDef("CURRENT_TIMESTAMP")),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormIndex("idx_notes_user", "user_id")},
				ForeignKeys: []ForeignKey{
					{Name: "fk_0", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{""}, OnUpdate: "NO ACTION", OnDelete: "CASCADE"},
				},
				CreateSQL: "CREATE TABLE notes (\n  id INTEGER PRIMARY KEY AUTOINCREMENT,\n  user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,\n  title VARCHAR(120) NOT NULL,\n  body TEXT,\n  pinned BOOLEAN NOT NULL DEFAULT 0,\n  score REAL,\n  price DECIMAL(10,2),\n  payload JSON,\n  attachment BLOB,\n  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP\n)",
			},
			{
				Schema: "main", Name: "user_tags", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("user_id", "INTEGER"),
					ormColumn("tag", "TEXT"),
				},
				PrimaryKey: []string{"user_id", "tag"},
				Indexes:    []ORMIndex{ormPK("sqlite_autoindex_user_tags_1", "user_id", "tag")},
				ForeignKeys: []ForeignKey{
					{Name: "fk_0", Columns: []string{"user_id"}, RefTable: "users", RefColumns: []string{"id"}, OnUpdate: "NO ACTION", OnDelete: "NO ACTION"},
				},
				CreateSQL: "CREATE TABLE user_tags (\n  user_id INTEGER NOT NULL REFERENCES users(id),\n  tag TEXT NOT NULL,\n  PRIMARY KEY (user_id, tag)\n)",
			},
			{
				Schema: "main", Name: "users", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "INTEGER", ormNull),
					ormColumn("email", "TEXT"),
					ormColumn("name", "TEXT", ormNull),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormUnique("sqlite_autoindex_users_1", "email")},
				CreateSQL:  "CREATE TABLE users (\n  id INTEGER PRIMARY KEY,\n  email TEXT NOT NULL UNIQUE,\n  name TEXT\n)",
			},
		},
	}
}

// ormMSSQLFixture uses the types only SQL Server has.
func ormMSSQLFixture() *ORMSchema {
	return &ORMSchema{
		Driver:   DriverMSSQL,
		Detailed: true,
		Tables: []ORMTable{
			{
				Schema: "dbo", Name: "accounts", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "int", ormAuto),
					ormColumn("guid", "uniqueidentifier", ormDef("(newid())")),
					ormColumn("name", "nvarchar(200)"),
					ormColumn("notes", "nvarchar(MAX)", ormNull),
					ormColumn("code", "char(8)", ormNull),
					ormColumn("active", "bit", ormDef("((1))")),
					ormColumn("level", "tinyint", ormDef("((0))")),
					ormColumn("balance", "money", ormNull),
					ormColumn("credit", "decimal(18,2)", ormDef("((0))")),
					ormColumn("photo", "varbinary(MAX)", ormNull),
					ormColumn("opened_on", "date", ormNull),
					ormColumn("created_at", "datetime2", ormDef("(sysdatetime())")),
					ormColumn("seen_at", "datetimeoffset", ormNull),
					ormColumn("version", "timestamp"),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PK_accounts", "id"), ormUnique("UQ_accounts_guid", "guid")},
			},
			{
				Schema: "dbo", Name: "transfers", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "bigint", ormAuto),
					ormColumn("from_account", "int"),
					ormColumn("to_account", "int", ormNull),
					ormColumn("amount", "decimal(18,2)"),
					ormColumn("memo", "varchar(255)", ormDef("('')")),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PK_transfers", "id"), ormIndex("IX_transfers_from", "from_account")},
				ForeignKeys: []ForeignKey{
					ormFK("FK_transfers_from", []string{"from_account"}, "dbo", "accounts", []string{"id"}, "CASCADE"),
					ormFK("FK_transfers_to", []string{"to_account"}, "dbo", "accounts", []string{"id"}, "NO ACTION"),
				},
			},
			{
				Schema: "sales", Name: "accounts", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("id", "int"),
					ormColumn("region", "nchar(2)"),
					ormColumn("payload", "xml", ormNull),
				},
				PrimaryKey: []string{"id"},
				Indexes:    []ORMIndex{ormPK("PK_sales_accounts", "id")},
			},
		},
	}
}

// ormOracleFixture reads the way ALL_TAB_COLUMNS spells things.
func ormOracleFixture() *ORMSchema {
	return &ORMSchema{
		Driver: DriverOracle,
		Tables: []ORMTable{
			{
				Schema: "SHOP", Name: "CUSTOMERS", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("ID", "NUMBER(10,0)", ormDef(`"SHOP"."ISEQ$$_73412".nextval`)),
					ormColumn("EMAIL", "VARCHAR2(255)"),
					ormColumn("FULL_NAME", "NVARCHAR2(200)", ormNull),
					ormColumn("BALANCE", "NUMBER(18,2)", ormDef("0 ")),
					ormColumn("POINTS", "NUMBER", ormNull),
					ormColumn("BIG_ID", "NUMBER(18,0)", ormNull),
					ormColumn("RATIO", "BINARY_DOUBLE", ormNull),
					ormColumn("NOTES", "CLOB", ormNull),
					ormColumn("TOKEN", "RAW(16)", ormNull),
					ormColumn("STATE", "CHAR(1)", ormDef("'A'")),
					ormColumn("CREATED", "DATE", ormDef("SYSDATE")),
					ormColumn("UPDATED", "TIMESTAMP(6) WITH TIME ZONE", ormNull),
					ormColumn("KEPT_FOR", "INTERVAL DAY(2) TO SECOND(6)", ormNull),
				},
				PrimaryKey: []string{"ID"},
				Indexes:    []ORMIndex{ormPK("CUSTOMERS_PK", "ID"), ormUnique("CUSTOMERS_EMAIL_UK", "EMAIL")},
			},
			{
				Schema: "SHOP", Name: "ORDERS", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("ID", "NUMBER(10,0)"),
					ormColumn("CUSTOMER_ID", "NUMBER(10,0)"),
					ormColumn("TOTAL", "NUMBER(12,2)"),
					ormColumn("PLACED", "TIMESTAMP(6)", ormDef("SYSTIMESTAMP")),
				},
				PrimaryKey: []string{"ID"},
				Indexes:    []ORMIndex{ormPK("ORDERS_PK", "ID")},
				ForeignKeys: []ForeignKey{
					ormFK("ORDERS_CUSTOMER_FK", []string{"CUSTOMER_ID"}, "SHOP", "CUSTOMERS", []string{"ID"}, "CASCADE"),
				},
			},
		},
	}
}

// ormClickHouseFixture has what only ClickHouse has: wrappers around types, a
// sorting key in place of a primary key, and no relations.
func ormClickHouseFixture() *ORMSchema {
	return &ORMSchema{
		Driver:   DriverClickHouse,
		Detailed: true,
		Tables: []ORMTable{
			{
				Schema: "analytics", Name: "page_views", Kind: ORMKindTable,
				Columns: []ORMColumn{
					ormColumn("ts", "DateTime"),
					ormColumn("user_id", "UInt64"),
					ormColumn("path", "String"),
					ormColumn("country", "LowCardinality(String)"),
					ormColumn("duration_ms", "UInt32"),
					ormColumn("is_bot", "UInt8"),
					ormColumn("referrer", "Nullable(String)", ormNull),
					ormColumn("tags", "Array(String)"),
					ormColumn("device", "Enum8('desktop' = 1, 'mobile' = 2)"),
					ormColumn("revenue", "Decimal(18, 4)"),
					ormColumn("session", "UUID", ormDef("generateUUIDv4()")),
					ormColumn("seen", "DateTime64(3, 'UTC')", ormDef("now64(3)")),
					ormColumn("props", "Map(String, String)"),
					ormColumn("big", "UInt128"),
				},
				PrimaryKey: []string{"ts", "user_id"},
				Indexes:    []ORMIndex{{Name: "sorting key", Columns: []string{"ts", "user_id"}, Primary: true}},
				CreateSQL:  "CREATE TABLE analytics.page_views\n(\n    `ts` DateTime,\n    `user_id` UInt64,\n    `path` String,\n    `country` LowCardinality(String),\n    `duration_ms` UInt32,\n    `is_bot` UInt8,\n    `referrer` Nullable(String),\n    `tags` Array(String),\n    `device` Enum8('desktop' = 1, 'mobile' = 2),\n    `revenue` Decimal(18, 4),\n    `session` UUID DEFAULT generateUUIDv4(),\n    `seen` DateTime64(3, 'UTC') DEFAULT now64(3),\n    `props` Map(String, String),\n    `big` UInt128\n)\nENGINE = MergeTree\nPARTITION BY toYYYYMM(ts)\nORDER BY (ts, user_id)\nSETTINGS index_granularity = 8192",
			},
		},
	}
}

// ormFixtureFor returns the fixture for an engine, by the name the golden
// directories use.
func ormFixtureFor(engine string) *ORMSchema {
	switch engine {
	case "postgres":
		return ormPostgresFixture()
	case "cockroachdb":
		s := ormPostgresFixture()
		s.Flavor = "cockroachdb"
		return s
	case "mysql":
		return ormMySQLFixture()
	case "sqlite":
		return ormSQLiteFixture()
	case "sqlserver":
		return ormMSSQLFixture()
	case "oracle":
		return ormOracleFixture()
	case "clickhouse":
		return ormClickHouseFixture()
	}
	return nil
}
