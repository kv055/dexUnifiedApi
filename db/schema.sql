CREATE TABLE snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    exchange TEXT NOT NULL,
    fiat_currency TEXT NOT NULL,
    direction TEXT NOT NULL,
    btc_price REAL NOT NULL
);

CREATE TABLE offers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    exchange TEXT NOT NULL,
    direction TEXT NOT NULL,
    price REAL NOT NULL,
    price_diff_percent REAL,
    btc_min REAL,
    btc_max REAL,
    fiat_min INTEGER,
    fiat_max INTEGER,
    payment_method TEXT,
    offer_id TEXT,
    first_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_live BOOLEAN NOT NULL DEFAULT 0,
    duration INTEGER,
    historical_analysis_status TEXT,
    avg_ref_price REAL,
    avg_premium_pct REAL,
    avg_trade_cost_pct REAL,
    avg_trade_cost_btc REAL,
    avg_hedge_fee_sum_btc REAL,
    cex_taker_fee_btc REAL,
    cex_withdraw_fee_btc REAL,
    network_fee_btc REAL,
    hedge_open_fee_btc REAL,
    hedge_close_fee_btc REAL,
    hedge_funding_fee_btc REAL,
    hedge_fee_sum_btc REAL,
    hedge_venue TEXT,
    hedge_instrument TEXT,
    hedge_transfer_minutes INTEGER,
    dex_fee_btc REAL,
    avg_net_premium_pct REAL,
    avg_estimated_profit_btc REAL,
    avg_estimated_profit_fiat REAL,
    avg_btc_amount REAL,
    price_type TEXT,
    best_route_cex TEXT,
    best_route_method TEXT
);

CREATE TABLE order_lifecycle (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    offer_db_id      INTEGER NOT NULL,
    snapshot_time    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    price            REAL    NOT NULL,
    ref_price        REAL    NOT NULL,
    premium_pct      REAL    NOT NULL,
    trade_cost_pct   REAL,
    trade_cost_btc   REAL,
    cex_taker_fee_btc REAL,
    cex_withdraw_fee_btc REAL,
    network_fee_btc  REAL,
    dex_fee_btc      REAL,
    net_premium_pct  REAL,
    net_premium_btc  REAL,
    btc_amount       REAL
);

CREATE INDEX idx_snapshots_time ON snapshots(timestamp);
CREATE INDEX idx_snapshots_exchange ON snapshots(exchange);
CREATE INDEX idx_offers_exchange ON offers(exchange);
CREATE INDEX idx_offers_direction ON offers(direction);
CREATE INDEX idx_offers_last_seen ON offers(last_seen);
CREATE INDEX idx_offers_fingerprint ON offers(exchange, direction, price, btc_min, btc_max, payment_method);
CREATE UNIQUE INDEX idx_offers_offer_id ON offers(offer_id) WHERE offer_id IS NOT NULL;

CREATE INDEX idx_lifecycle_offer ON order_lifecycle(offer_db_id);
CREATE INDEX idx_lifecycle_time  ON order_lifecycle(snapshot_time);

CREATE TABLE snapshot_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    exchanges TEXT NOT NULL,
    offers_count INTEGER NOT NULL
);

CREATE TABLE kraken_ohlc (
    time  DATETIME PRIMARY KEY,
    open  REAL NOT NULL,
    high  REAL NOT NULL,
    low   REAL NOT NULL,
    close REAL NOT NULL
);
