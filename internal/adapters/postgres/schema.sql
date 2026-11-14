CREATE TABLE IF NOT EXISTS current_snapshot (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    retrieved_at timestamptz NOT NULL,
    route jsonb NOT NULL CHECK (jsonb_typeof(route) = 'object'),
    forecasts jsonb NOT NULL CHECK (jsonb_typeof(forecasts) = 'array')
);
