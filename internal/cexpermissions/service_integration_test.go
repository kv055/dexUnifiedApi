package cexpermissions

import (
	"context"
	"testing"
	"time"

	"dexunifiedapi/internal/testdb"
)

func TestServiceSelectsCEXAccountsAndActiveKeys(t *testing.T) {
	database := testdb.Open(t)
	ctx := context.Background()
	var krakenID, bitstampID int64
	if err := database.QueryRowContext(ctx, `
		INSERT INTO accounts (provider, category) VALUES ('Kraken', 'CEX') RETURNING id
	`).Scan(&krakenID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `
		INSERT INTO accounts (provider, category) VALUES ('Bitstamp', 'CEX') RETURNING id
	`).Scan(&bitstampID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO accounts (provider, category) VALUES ('Bisq', 'DEX')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO api_keys (account_id, name, api_key, is_active, permissions, permissions_checked_at)
		VALUES
		  ($1, 'spot', 'encrypted', TRUE,
		   '{"fetch_deposit_address":"granted","spot_trading":"granted","read_balances":"denied","withdraw":"denied","hedge_positions":"unsupported"}',
		   $2),
		  ($1, 'funding', 'encrypted', TRUE,
		   '{"fetch_deposit_address":"denied","spot_trading":"denied","read_balances":"granted","withdraw":"granted","hedge_positions":"unsupported"}',
		   $2),
		  ($1, 'inactive', 'encrypted', FALSE,
		   '{"hedge_positions":"granted"}',
		   $2)
	`, krakenID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	response, err := NewService(database).Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Exchanges) != 2 {
		t.Fatalf("exchanges = %+v", response.Exchanges)
	}
	var kraken, bitstamp *Exchange
	for index := range response.Exchanges {
		exchange := &response.Exchanges[index]
		switch exchange.AccountID {
		case krakenID:
			kraken = exchange
		case bitstampID:
			bitstamp = exchange
		}
	}
	if kraken == nil || kraken.ActiveKeyCount != 2 || !kraken.Spot.Available {
		t.Fatalf("kraken = %+v", kraken)
	}
	if kraken.Permissions[ReadBalances] != Granted || kraken.Permissions[Withdraw] != Granted {
		t.Fatalf("complementary permissions = %+v", kraken.Permissions)
	}
	if bitstamp == nil || bitstamp.OptimizerSupported || bitstamp.Spot.Available {
		t.Fatalf("bitstamp = %+v", bitstamp)
	}
}
