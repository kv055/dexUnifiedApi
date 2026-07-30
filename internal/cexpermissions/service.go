package cexpermissions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	FetchDepositAddress = "fetch_deposit_address"
	SpotTrading         = "spot_trading"
	ReadBalances        = "read_balances"
	Withdraw            = "withdraw"
	HedgePositions      = "hedge_positions"

	Granted     = "granted"
	Unknown     = "unknown"
	Denied      = "denied"
	Unsupported = "unsupported"
)

var (
	canonicalPermissions = []string{FetchDepositAddress, SpotTrading, ReadBalances, Withdraw, HedgePositions}
	spotPermissions      = []string{FetchDepositAddress, SpotTrading, ReadBalances, Withdraw}
	hedgePermissions     = []string{HedgePositions}
	optimizerProviders   = map[string]bool{"binance": true, "kraken": true, "coinbase": true}
)

// Queryer is the database surface used by Service.
type Queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type Service struct {
	db Queryer
}

func NewService(db Queryer) *Service {
	return &Service{db: db}
}

type Response struct {
	Exchanges []Exchange `json:"exchanges"`
}

type Availability struct {
	Available           bool     `json:"available"`
	BlockingPermissions []string `json:"blocking_permissions"`
	Reason              string   `json:"reason,omitempty"`
}

type Exchange struct {
	AccountID            int64             `json:"account_id"`
	Provider             string            `json:"provider"`
	Exchange             string            `json:"exchange"`
	OptimizerSupported   bool              `json:"optimizer_supported"`
	ActiveKeyCount       int               `json:"active_key_count"`
	PermissionsCheckedAt *time.Time        `json:"permissions_checked_at"`
	Permissions          map[string]string `json:"permissions"`
	Spot                 Availability      `json:"spot"`
	Hedge                Availability      `json:"hedge"`
}

type keyResult struct {
	permissions map[string]string
	checkedAt   *time.Time
	checkFailed bool
}

func (s *Service) Get(ctx context.Context) (Response, error) {
	if s == nil || s.db == nil {
		return Response{}, fmt.Errorf("cex permissions database is unavailable")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id,
		       a.provider,
		       k.id,
		       k.permissions,
		       k.permissions_checked_at,
		       k.permission_check_error
		FROM accounts a
		LEFT JOIN api_keys k
		  ON k.account_id = a.id
		 AND k.is_active = TRUE
		WHERE a.category = 'CEX'
		ORDER BY a.provider, a.id, k.id
	`)
	if err != nil {
		return Response{}, fmt.Errorf("query CEX permissions: %w", err)
	}
	defer rows.Close()

	type account struct {
		id       int64
		provider string
		keys     []keyResult
	}
	accounts := make([]account, 0)
	byID := make(map[int64]int)
	for rows.Next() {
		var (
			accountID  int64
			provider   string
			keyID      sql.NullInt64
			raw        []byte
			checkedAt  sql.NullTime
			checkError sql.NullString
		)
		if err := rows.Scan(&accountID, &provider, &keyID, &raw, &checkedAt, &checkError); err != nil {
			return Response{}, fmt.Errorf("scan CEX permissions: %w", err)
		}
		index, ok := byID[accountID]
		if !ok {
			index = len(accounts)
			byID[accountID] = index
			accounts = append(accounts, account{id: accountID, provider: provider})
		}
		if !keyID.Valid {
			continue
		}
		key := keyResult{
			permissions: map[string]string{},
			checkFailed: strings.TrimSpace(checkError.String) != "" || !checkedAt.Valid || len(raw) == 0,
		}
		if checkedAt.Valid {
			value := checkedAt.Time
			key.checkedAt = &value
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &key.permissions); err != nil {
				key.checkFailed = true
			}
		}
		accounts[index].keys = append(accounts[index].keys, key)
	}
	if err := rows.Err(); err != nil {
		return Response{}, fmt.Errorf("iterate CEX permissions: %w", err)
	}

	response := Response{Exchanges: make([]Exchange, 0, len(accounts))}
	for _, account := range accounts {
		response.Exchanges = append(response.Exchanges, aggregate(account.id, account.provider, account.keys))
	}
	return response, nil
}

func aggregate(accountID int64, provider string, keys []keyResult) Exchange {
	exchangeName := strings.ToLower(strings.TrimSpace(provider))
	result := Exchange{
		AccountID:          accountID,
		Provider:           provider,
		Exchange:           exchangeName,
		OptimizerSupported: optimizerProviders[exchangeName],
		ActiveKeyCount:     len(keys),
		Permissions:        make(map[string]string, len(canonicalPermissions)),
	}
	for _, key := range keys {
		if key.checkedAt != nil && (result.PermissionsCheckedAt == nil || key.checkedAt.After(*result.PermissionsCheckedAt)) {
			value := *key.checkedAt
			result.PermissionsCheckedAt = &value
		}
	}
	for _, permission := range canonicalPermissions {
		result.Permissions[permission] = aggregatePermission(permission, keys)
	}
	if len(keys) == 0 {
		const reason = "No active API keys are configured."
		result.Spot = Availability{Available: false, BlockingPermissions: append([]string(nil), spotPermissions...), Reason: reason}
		result.Hedge = Availability{Available: false, BlockingPermissions: append([]string(nil), hedgePermissions...), Reason: reason}
		return result
	}
	result.Spot = availability(result.Permissions, spotPermissions)
	result.Hedge = availability(result.Permissions, hedgePermissions)
	return result
}

func aggregatePermission(permission string, keys []keyResult) string {
	unknown := len(keys) == 0
	denied := false
	reliable := 0
	unsupported := 0
	for _, key := range keys {
		value, present := key.permissions[permission]
		if key.checkFailed || !present || value == "" || value == Unknown {
			unknown = true
			continue
		}
		switch value {
		case Granted:
			return Granted
		case Denied:
			reliable++
			denied = true
		case Unsupported:
			reliable++
			unsupported++
		default:
			unknown = true
		}
	}
	if unknown {
		return Unknown
	}
	if denied {
		return Denied
	}
	if reliable > 0 && unsupported == reliable {
		return Unsupported
	}
	return Unknown
}

func availability(permissions map[string]string, required []string) Availability {
	blocking := make([]string, 0)
	for _, permission := range required {
		if value := permissions[permission]; value == Denied || value == Unsupported {
			blocking = append(blocking, permission)
		}
	}
	return Availability{Available: len(blocking) == 0, BlockingPermissions: blocking}
}
