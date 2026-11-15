BEGIN;
CREATE TABLE IF NOT EXISTS route_snapshots (
    id text PRIMARY KEY,
    name text NOT NULL,
    retrieved_at timestamptz NOT NULL,
    route jsonb NOT NULL CHECK (jsonb_typeof(route) = 'object'),
    forecasts jsonb NOT NULL CHECK (jsonb_typeof(forecasts) = 'array')
);
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.tables
        WHERE table_schema = current_schema() AND table_name = 'current_snapshot'
    ) THEN
        INSERT INTO route_snapshots (id,name,retrieved_at,route,forecasts)
        SELECT 'saved-route','Saved route',retrieved_at,route,forecasts FROM current_snapshot;
        DROP TABLE current_snapshot;
    END IF;
END $$;
COMMIT;
