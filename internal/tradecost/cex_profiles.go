package tradecost

// WithdrawalMethod is the settlement network used to move BTC from CEX to DEX.
type WithdrawalMethod string

const (
	MethodOnchain   WithdrawalMethod = "onchain"
	MethodLightning WithdrawalMethod = "lightning"
)

// CexWithdrawalProfile describes the onchain withdrawal characteristics of a CEX,
// used to build accurate transaction size estimates for the bitcoin fee predictor.
type CexWithdrawalProfile struct {
	// Methods lists the settlement networks this exchange supports for BTC withdrawals.
	Methods []WithdrawalMethod
	// OnchainTxType is the Bitcoin script type the exchange uses for onchain withdrawals.
	// Drives the virtual-size calculation in the fee predictor.
	// Supported values: "P2WPKH", "P2WSH", "P2TR", "P2PKH", "P2SH".
	OnchainTxType string
	// TypicalInputs is the expected number of inputs in a withdrawal transaction.
	// Most hot-wallet withdrawals use a single consolidated UTXO.
	TypicalInputs int
	// TypicalOutputs is the expected number of outputs (destination + change).
	// Standard CEX withdrawals produce exactly 2 outputs.
	TypicalOutputs int
}

// cexProfiles maps exchange name → withdrawal profile.
// Add new exchanges here — no other code changes are needed.
var cexProfiles = map[string]CexWithdrawalProfile{
	// Kraken: native SegWit (bech32), Lightning enabled.
	"kraken": {
		Methods:        []WithdrawalMethod{MethodOnchain, MethodLightning},
		OnchainTxType:  "P2WPKH",
		TypicalInputs:  1,
		TypicalOutputs: 2,
	},
	// Binance: native SegWit (bech32), Lightning enabled.
	"binance": {
		Methods:        []WithdrawalMethod{MethodOnchain},
		OnchainTxType:  "P2WPKH",
		TypicalInputs:  1,
		TypicalOutputs: 2,
	},
	// Coinbase: native SegWit (bech32), Lightning enabled.
	"coinbase": {
		Methods:        []WithdrawalMethod{MethodOnchain, MethodLightning},
		OnchainTxType:  "P2WPKH",
		TypicalInputs:  1,
		TypicalOutputs: 2,
	},
	// Bitstamp: native SegWit (bech32), onchain only.
	"bitstamp": {
		Methods:        []WithdrawalMethod{MethodOnchain},
		OnchainTxType:  "P2WPKH",
		TypicalInputs:  1,
		TypicalOutputs: 2,
	},
}

// defaultCexProfile is used for exchanges not in cexProfiles.
var defaultCexProfile = CexWithdrawalProfile{
	Methods:        []WithdrawalMethod{MethodOnchain},
	OnchainTxType:  "P2WPKH",
	TypicalInputs:  1,
	TypicalOutputs: 2,
}

// dexAcceptsLightning lists DEX platforms that support Lightning Network settlement.
// When both the CEX and DEX support Lightning, it is preferred over onchain.
var dexAcceptsLightning = map[string]bool{
	"robosats": true,
	"bisq":     false,
	"hodlhodl": false,
}

// cexProfileFor returns the withdrawal profile for the given exchange,
// falling back to defaultCexProfile for unknown exchanges.
func cexProfileFor(exchange string) CexWithdrawalProfile {
	if p, ok := cexProfiles[exchange]; ok {
		return p
	}
	return defaultCexProfile
}

// ProfileFor returns the withdrawal profile for the given exchange,
// falling back to the default profile for unknown exchanges.
func ProfileFor(exchange string) CexWithdrawalProfile {
	return cexProfileFor(exchange)
}

// resolveWithdrawalMethod returns the settlement method for a CEX→DEX trade.
// Lightning is preferred when both sides support it; onchain is the fallback.
func resolveWithdrawalMethod(profile CexWithdrawalProfile, dexExchange string) WithdrawalMethod {
	if dexAcceptsLightning[dexExchange] {
		for _, m := range profile.Methods {
			if m == MethodLightning {
				return MethodLightning
			}
		}
	}
	return MethodOnchain
}

// supportedWithdrawalMethods returns all settlement methods supported for the
// given CEX→DEX pair so callers can compare full route costs.
func supportedWithdrawalMethods(profile CexWithdrawalProfile, dexExchange string) []WithdrawalMethod {
	methods := make([]WithdrawalMethod, 0, len(profile.Methods))
	seen := make(map[WithdrawalMethod]struct{}, len(profile.Methods))

	for _, method := range profile.Methods {
		switch method {
		case MethodOnchain:
			if _, ok := seen[MethodOnchain]; !ok {
				methods = append(methods, MethodOnchain)
				seen[MethodOnchain] = struct{}{}
			}
		case MethodLightning:
			if dexAcceptsLightning[dexExchange] {
				if _, ok := seen[MethodLightning]; !ok {
					methods = append(methods, MethodLightning)
					seen[MethodLightning] = struct{}{}
				}
			}
		}
	}

	if len(methods) == 0 {
		return []WithdrawalMethod{MethodOnchain}
	}
	return methods
}
