-- Persist coordinator checkpoints and one immutable signed request per action.
-- NULL data identifies sessions made by the earlier incomplete implementation;
-- those sessions require recovery rather than inventing new trade keys.
ALTER TABLE mostro_trade_sessions ADD COLUMN IF NOT EXISTS session_data JSONB;
ALTER TABLE mostro_outbox ADD COLUMN IF NOT EXISTS action VARCHAR(96);
CREATE UNIQUE INDEX IF NOT EXISTS mostro_outbox_session_action_uidx
 ON mostro_outbox(session_id, action) WHERE action IS NOT NULL;

-- Keep only encrypted chat events; deduplicate both inner and outer IDs.
CREATE TABLE IF NOT EXISTS mostro_chat_events (
 session_id BIGINT NOT NULL REFERENCES mostro_trade_sessions(id) ON DELETE CASCADE,
 event_id VARCHAR(64) PRIMARY KEY,
 inner_event_id VARCHAR(64) NOT NULL,
 event_json JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 UNIQUE(session_id,inner_event_id)
);
CREATE INDEX IF NOT EXISTS mostro_chat_events_session_time_idx
 ON mostro_chat_events(session_id,created_at);

-- Protocol version was previously guessed from an order event, which cannot
-- advertise transport capabilities. Resolve it from a fresh signed node event.
UPDATE offers SET venue_protocol_version=NULL WHERE exchange='mostro';
ALTER TABLE mostro_identities ADD COLUMN IF NOT EXISTS identity_pubkey VARCHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS mostro_identities_pubkey_uidx
 ON mostro_identities(identity_pubkey) WHERE identity_pubkey IS NOT NULL;
