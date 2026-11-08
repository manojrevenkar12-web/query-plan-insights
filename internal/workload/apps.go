// Package workload runs a realistic query mix against a seeded schema, so faults injected into the database show up
// the way they would in production: as some queries getting slower while the rest carry on.
package workload

// Query is one statement the application issues, at a steady rate.
type Query struct {
	Name    string
	SQL     string
	PerSec  float64
	Workers int
	Args    func(r *Rand) []any
}

// App is a schema, its seed data and its query mix.
type App struct {
	Name   string
	Schema []string
	Seed   []string
	Mix    []Query
	// Batch is the batched lookup that the N+1 fault replaces with one query per item.
	Batch   Query
	PerItem Query
}

// Shop is the development application: customers, products, orders and their items.
func Shop() App {
	return App{
		Name: "shop",
		Schema: []string{
			`DROP SCHEMA IF EXISTS shop CASCADE`,
			`CREATE SCHEMA shop`,
			`CREATE TABLE shop.customers (id int PRIMARY KEY, name text NOT NULL, country text NOT NULL)`,
			`CREATE TABLE shop.products (id int PRIMARY KEY, name text NOT NULL, price numeric(10,2) NOT NULL, stock int NOT NULL)`,
			`CREATE TABLE shop.orders (id int PRIMARY KEY, customer_id int NOT NULL, created_at timestamptz NOT NULL,
				status text NOT NULL, total numeric(12,2) NOT NULL)`,
			`CREATE TABLE shop.order_items (order_id int NOT NULL, product_id int NOT NULL, qty int NOT NULL)`,
		},
		Seed: []string{
			`INSERT INTO shop.customers SELECT g, 'customer ' || g, (ARRAY['GB','DE','FR','IN','US'])[1 + g % 5] FROM generate_series(1, 50000) g`,
			`INSERT INTO shop.products SELECT g, 'product ' || g, (g % 500) + 0.99, 1000000 FROM generate_series(1, 2000) g`,
			`INSERT INTO shop.orders SELECT g, 1 + (g * 7919) % 50000, now() - ((g % 720) || ' hours')::interval,
				(ARRAY['paid','shipped','cancelled','refunded'])[1 + g % 4], (g % 900) + 10.5 FROM generate_series(1, 300000) g`,
			`INSERT INTO shop.order_items SELECT 1 + (g % 300000), 1 + (g * 31) % 2000, 1 + g % 3 FROM generate_series(1, 600000) g`,
			`CREATE INDEX orders_customer_idx ON shop.orders (customer_id)`,
			`CREATE INDEX order_items_order_idx ON shop.order_items (order_id)`,
			`ANALYZE`,
		},
		Mix: []Query{
			{Name: "customer_orders", SQL: `SELECT id, total, status FROM shop.orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 20`,
				PerSec: 25, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(50000)} }},
			{Name: "product", SQL: `SELECT name, price, stock FROM shop.products WHERE id = $1`,
				PerSec: 80, Workers: 3, Args: func(r *Rand) []any { return []any{1 + r.Intn(2000)} }},
			{Name: "reserve_stock", SQL: `UPDATE shop.products SET stock = stock - 1 WHERE id = $1`,
				PerSec: 20, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(40)} }},
			{Name: "order_items", SQL: `SELECT product_id, qty FROM shop.order_items WHERE order_id = $1`,
				PerSec: 40, Workers: 3, Args: func(r *Rand) []any { return []any{1 + r.Intn(300000)} }},
			{Name: "top_customers", SQL: `SELECT customer_id, sum(total) AS spend FROM shop.orders WHERE created_at >= now() - $1::interval
				GROUP BY customer_id ORDER BY spend DESC LIMIT 10`,
				PerSec: 2, Workers: 2, Args: func(r *Rand) []any { return []any{"240 hours"} }},
		},
		Batch: Query{Name: "basket_products", SQL: `SELECT id, name, price FROM shop.products WHERE id = ANY($1)`,
			PerSec: 6, Workers: 2, Args: func(r *Rand) []any { return []any{r.IDArray(20, 2000)} }},
		PerItem: Query{Name: "basket_product_each", SQL: `SELECT id, name, price FROM shop.products WHERE id = $1 AND price > 0`,
			PerSec: 120, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(2000)} }},
	}
}

// Billing is the held-out application: different tables, indexes, query shapes and rates.
func Billing() App {
	return App{
		Name: "billing",
		Schema: []string{
			`DROP SCHEMA IF EXISTS billing CASCADE`,
			`CREATE SCHEMA billing`,
			`CREATE TABLE billing.accounts (id int PRIMARY KEY, plan text NOT NULL, balance numeric(14,2) NOT NULL)`,
			`CREATE TABLE billing.invoices (id int PRIMARY KEY, account_id int NOT NULL, issued_at timestamptz NOT NULL,
				amount numeric(12,2) NOT NULL, state text NOT NULL)`,
			`CREATE TABLE billing.payments (id int PRIMARY KEY, invoice_id int NOT NULL, paid_at timestamptz NOT NULL, amount numeric(12,2) NOT NULL)`,
			`CREATE TABLE billing.usage (account_id int NOT NULL, metric text NOT NULL, day date NOT NULL, quantity bigint NOT NULL)`,
		},
		Seed: []string{
			`INSERT INTO billing.accounts SELECT g, (ARRAY['free','team','business'])[1 + g % 3], 0 FROM generate_series(1, 20000) g`,
			`INSERT INTO billing.invoices SELECT g, 1 + (g * 104729) % 20000, now() - ((g % 1000) || ' hours')::interval,
				(g % 400) + 5, (ARRAY['open','paid','void'])[1 + g % 3] FROM generate_series(1, 400000) g`,
			`INSERT INTO billing.payments SELECT g, 1 + (g * 13) % 400000, now() - ((g % 900) || ' hours')::interval, (g % 400) + 5
				FROM generate_series(1, 250000) g`,
			`INSERT INTO billing.usage SELECT 1 + g % 20000, (ARRAY['api_calls','storage_gb','seats'])[1 + g % 3],
				current_date - (g % 60), g % 10000 FROM generate_series(1, 400000) g`,
			`CREATE INDEX invoices_account_issued_idx ON billing.invoices (account_id, issued_at DESC)`,
			`CREATE INDEX payments_invoice_idx ON billing.payments (invoice_id)`,
			`ANALYZE`,
		},
		Mix: []Query{
			{Name: "latest_invoices", SQL: `SELECT id, amount, state FROM billing.invoices WHERE account_id = $1 ORDER BY issued_at DESC LIMIT 5`,
				PerSec: 30, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(20000)} }},
			{Name: "invoice_payments", SQL: `SELECT paid_at, amount FROM billing.payments WHERE invoice_id = $1`,
				PerSec: 40, Workers: 3, Args: func(r *Rand) []any { return []any{1 + r.Intn(400000)} }},
			{Name: "charge_account", SQL: `UPDATE billing.accounts SET balance = balance + $2 WHERE id = $1`,
				PerSec: 15, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(25), r.Intn(50)} }},
			{Name: "account", SQL: `SELECT plan, balance FROM billing.accounts WHERE id = $1`,
				PerSec: 60, Workers: 3, Args: func(r *Rand) []any { return []any{1 + r.Intn(20000)} }},
			{Name: "usage_rollup", SQL: `SELECT account_id, metric, sum(quantity) AS q FROM billing.usage WHERE day >= current_date - $1::int
				GROUP BY account_id, metric ORDER BY q DESC LIMIT 20`,
				PerSec: 2, Workers: 2, Args: func(r *Rand) []any { return []any{30} }},
		},
		Batch: Query{Name: "accounts_batch", SQL: `SELECT id, plan FROM billing.accounts WHERE id = ANY($1)`,
			PerSec: 5, Workers: 2, Args: func(r *Rand) []any { return []any{r.IDArray(25, 20000)} }},
		PerItem: Query{Name: "account_each", SQL: `SELECT id, plan FROM billing.accounts WHERE id = $1 AND balance >= 0`,
			PerSec: 125, Workers: 4, Args: func(r *Rand) []any { return []any{1 + r.Intn(20000)} }},
	}
}
