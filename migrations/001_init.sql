DO $$ BEGIN
 IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='p27_worker') THEN CREATE ROLE p27_worker LOGIN PASSWORD 'p27-local-worker'; END IF;
 IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='p27_receiver') THEN CREATE ROLE p27_receiver LOGIN PASSWORD 'p27-local-receiver'; END IF;
END $$;
CREATE SCHEMA worker AUTHORIZATION p27_worker;
CREATE SCHEMA receiver AUTHORIZATION p27_receiver;
SET ROLE p27_worker;
CREATE TABLE worker.jobs (
 event_id text PRIMARY KEY, payload bytea NOT NULL, hash text NOT NULL,
 state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','processing','retry_wait','delivered','failed','uncertain')),
 attempt_count int NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 3),
 ambiguous boolean NOT NULL DEFAULT false, due_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 claim_token text NOT NULL DEFAULT '', lease_until timestamptz,
 receipt jsonb, last_reason text NOT NULL DEFAULT '', traceparent text NOT NULL DEFAULT ''
);
CREATE TABLE worker.attempts (
 event_id text REFERENCES worker.jobs, number int NOT NULL, token text NOT NULL,
 phase text NOT NULL, reason text NOT NULL DEFAULT '', ambiguous boolean NOT NULL DEFAULT false,
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (event_id, number, phase)
);
RESET ROLE;
SET ROLE p27_receiver;
CREATE TABLE receiver.receipts (event_id text PRIMARY KEY, hash text NOT NULL, receipt_id text NOT NULL UNIQUE);
CREATE TABLE receiver.effects (event_id text PRIMARY KEY REFERENCES receiver.receipts, sale_reference text NOT NULL, currency text NOT NULL, amount_minor bigint NOT NULL);
RESET ROLE;
