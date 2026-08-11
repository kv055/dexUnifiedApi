ALTER TABLE submarine_swaps
    ADD COLUMN IF NOT EXISTS reservation_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_submarine_swaps_reservation_id
    ON submarine_swaps(reservation_id)
    WHERE reservation_id <> '';
