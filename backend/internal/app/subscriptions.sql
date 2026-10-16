CREATE TABLE subscriptions (
 id text PRIMARY KEY,
 name text NOT NULL CHECK (length(trim(name)) > 0),
 amount numeric NOT NULL CHECK (amount > 0 AND amount <> 'NaN'::numeric AND scale(amount) <= 2),
 currency text NOT NULL CHECK (currency IN ('RUB','USD','EUR')),
 period_days integer NOT NULL CHECK (period_days IN (30,90,180,365)),
 next_payment_date date NOT NULL,
 archived boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL
);
INSERT INTO schema_migrations(version) VALUES (2);
