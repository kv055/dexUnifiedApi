CREATE TABLE IF NOT EXISTS ohlc (
    time TIMESTAMPTZ NOT NULL,
    fiat_currency fiat_currency NOT NULL DEFAULT 'EUR',
    open DOUBLE PRECISION NOT NULL CHECK (open > 0),
    high DOUBLE PRECISION NOT NULL CHECK (high > 0),
    low DOUBLE PRECISION NOT NULL CHECK (low > 0),
    close DOUBLE PRECISION NOT NULL CHECK (close > 0),
    CHECK (high >= low),
    CHECK (high >= open),
    CHECK (high >= close),
    CHECK (low <= open),
    CHECK (low <= close),
    PRIMARY KEY (time, fiat_currency)
);
