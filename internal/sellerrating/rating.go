// Package sellerrating normalizes the seller/maker reputation signals of
// several P2P exchanges into one comparable 0-100 score plus a confidence tag,
// mirroring commons/paymentmethods as the established pattern for cross-exchange
// normalization.
//
// Each exchange expresses maker trust differently — a peer-feedback average
// (HodlHodl, Peach), a platform-computed composite reputation score (Bisq 2),
// a raw financial bond percentage (RoboSats), or nothing at all (Bisq 1). The
// functions here take the exchange-specific raw signal and return a Score whose
// Value is on a single 0-100 scale, whose Confidence records which methodology
// produced it (so a bond-derived estimate is never silently compared as if it
// were a verified feedback score), and whose Raw preserves the source inputs so
// the score can be re-derived later without re-fetching.
//
// All functions are pure (no DB/HTTP dependency) and unit-testable.
package sellerrating

import (
	"encoding/json"
	"math"
	"time"
)

// Confidence records how much a Score.Value can be trusted, independent of the
// exchange it came from. New tags added here must be mirrored in the
// offers.seller_rating_confidence CHECK constraint.
type Confidence string

const (
	// ConfidenceUnavailable means no reputation signal was obtainable at all
	// (e.g. Bisq 1, whose signal is not exposed by the data we ingest). Paired
	// with a nil Score.Value.
	ConfidenceUnavailable Confidence = "unavailable"
	// ConfidenceFeedbackNew is a peer-feedback exchange whose sample count is
	// below the trust threshold — the score is real but dominated by the prior.
	ConfidenceFeedbackNew Confidence = "feedback_new"
	// ConfidenceFeedbackVerified is a peer-feedback exchange with enough trades
	// behind the rating to trust it.
	ConfidenceFeedbackVerified Confidence = "feedback_verified"
	// ConfidenceStakeBonded is trust derived from a raw financial bond
	// percentage rather than peer feedback (RoboSats).
	ConfidenceStakeBonded Confidence = "stake_bonded"
	// ConfidenceReputationBonded is a platform-computed composite reputation
	// score, backed by economic stake plus history (Bisq 2).
	ConfidenceReputationBonded Confidence = "reputation_bonded"
	// ConfidenceAccountSigning is trust derived from Bisq 1's account-signing
	// web of trust (sign-state tier) plus account age, rather than peer
	// feedback, a bond, or a composite score.
	ConfidenceAccountSigning Confidence = "account_signing"
)

// Score is the normalized 0-100 reputation signal stored on an offer, plus
// enough provenance to explain and re-derive it later.
type Score struct {
	// Value is the normalized reputation on a 0-100 scale. A nil Value means no
	// data at all (see ConfidenceUnavailable).
	Value *float64
	// Confidence records which methodology produced Value.
	Confidence Confidence
	// Raw carries the exchange-specific inputs that produced Value, for the
	// JSONB column. Nil when there were no inputs.
	Raw json.RawMessage
}

// ConfidencePtr returns Confidence as a *string, for storing into the nullable
// offer field. It is never nil (Confidence is always set), but the pointer form
// matches the offer struct's convention of pointer-nullable primitives.
func (s Score) ConfidencePtr() *string {
	c := string(s.Confidence)
	return &c
}

// Unavailable returns the Score for any exchange/situation with no reputation
// signal at all.
func Unavailable() Score {
	return Score{Value: nil, Confidence: ConfidenceUnavailable, Raw: nil}
}

// NormalizeFeedbackRating converts a peer-feedback average (already rescaled by
// the caller into [0,1]) plus a sample count into a shrinkage-adjusted 0-100
// score. It is shared by every "feedback average + sample count" exchange
// (HodlHodl, Peach) — each rescales its own raw range to [0,1] first.
//
// rating == nil means "no feedback recorded yet"; the score is still computed
// from the prior alone (a heavily-shrunk value), which is distinct from the
// Unavailable() nil case reserved for exchanges with no mechanism. sampleCount
// is the number of trades/ratings behind rating. priorMean is the neutral prior
// in [0,1]; priorWeight is the shrinkage strength (how many fictitious prior
// observations). A brand-new account's lucky first trade is pulled toward the
// prior so it cannot outrank a long track record. Confidence is
// ConfidenceFeedbackVerified once sampleCount >= verifiedThreshold, else
// ConfidenceFeedbackNew. raw is passed through verbatim into Score.Raw.
func NormalizeFeedbackRating(rating *float64, sampleCount int, priorMean, priorWeight float64, verifiedThreshold int, raw json.RawMessage) Score {
	effectiveRating := priorMean
	if rating != nil {
		effectiveRating = *rating
	}

	n := float64(sampleCount)
	adjusted := priorMean
	if denom := n + priorWeight; denom > 0 {
		adjusted = (effectiveRating*n + priorMean*priorWeight) / denom
	}

	value := clamp0100(round2(adjusted * 100))
	confidence := ConfidenceFeedbackNew
	if sampleCount >= verifiedThreshold {
		confidence = ConfidenceFeedbackVerified
	}
	return Score{Value: &value, Confidence: confidence, Raw: raw}
}

// NormalizeStakeBond min-max scales a bond/fidelity percentage onto 0-100 using
// the documented [minPct,maxPct] range, clamping values outside the range
// rather than erroring (a maker may post a bond above/below the default range).
// Always ConfidenceStakeBonded.
func NormalizeStakeBond(bondPct, minPct, maxPct float64, raw json.RawMessage) Score {
	frac := 0.0
	if span := maxPct - minPct; span > 0 {
		frac = (bondPct - minPct) / span
	}
	value := clamp0100(round2(clamp01(frac) * 100))
	return Score{Value: &value, Confidence: ConfidenceStakeBonded, Raw: raw}
}

// NormalizeReputationScore converts a platform-computed star rating (already
// bucketed by the exchange, e.g. Bisq 2's 0-5 fiveSystemScore) onto 0-100.
// A stars value of 0 is an honest "no reputation stake" score, distinct from
// Unavailable(). Always ConfidenceReputationBonded.
func NormalizeReputationScore(stars, maxStars float64, raw json.RawMessage) Score {
	frac := 0.0
	if maxStars > 0 {
		frac = stars / maxStars
	}
	value := clamp0100(round2(clamp01(frac) * 100))
	return Score{Value: &value, Confidence: ConfidenceReputationBonded, Raw: raw}
}

// Bisq 1 sign-state trust bands (0-90). getSignState returns one of these enum
// names; higher = more established in Bisq's account-signing web of trust. On
// top of the band, an older account-age witness adds a small bounded age bonus.
const (
	bisqSignBandBanned          = 0.0  // banned maker: hard negative floor
	bisqSignBandUnsigned        = 20.0 // no signing yet (also the no-witness case)
	bisqSignBandPeerInitial     = 45.0 // peer-signed, still in initial trade-limit window
	bisqSignBandPeerLimitLifted = 60.0 // peer-signed, limit lifted
	bisqSignBandPeerSigner      = 80.0 // is itself a signer (can sign others)
	bisqSignBandArbitrator      = 90.0 // arbitrator-signed (strongest)

	// bisqAgeBonusMax is the most an old account adds on top of its band;
	// bisqAgeBonusFullDays is the account age at which that full bonus applies.
	bisqAgeBonusMax      = 10.0
	bisqAgeBonusFullDays = 365.0
)

// NormalizeBisqAccountSigning maps a Bisq 1 maker's account-signing sign-state
// plus account-age-witness date to a 0-100 score. Bisq 1 exposes no peer-feedback
// or bond signal — trust is its account-signing web of trust (unsigned → peer
// initial → limit lifted → signer → arbitrator, with BANNED as a hard negative)
// combined with how long the account-age witness has existed. The sign-state
// sets a base band; account age adds a small bounded bonus on top (except for
// BANNED, which stays at the floor).
//
// signState is Bisq's SignState.name() as sent by the (patched) daemon; an empty
// or unrecognized value returns Unavailable(), so an unpatched daemon — which
// sends "" — degrades gracefully. witnessDateMillis is the witness creation time
// in epoch millis, or -1 when the maker has no witness (treated as zero age).
// Always ConfidenceAccountSigning when a signal is present.
func NormalizeBisqAccountSigning(signState string, witnessDateMillis int64, now time.Time) Score {
	base, ok := bisqSignStateBand(signState)
	if !ok {
		return Unavailable()
	}

	ageDays := 0.0
	if witnessDateMillis > 0 {
		ageDays = now.Sub(time.UnixMilli(witnessDateMillis)).Hours() / 24
		if ageDays < 0 {
			ageDays = 0
		}
	}

	value := base
	if signState != "BANNED" {
		value += bisqAgeBonusMax * clamp01(ageDays/bisqAgeBonusFullDays)
	}
	value = clamp0100(round2(value))

	raw, _ := json.Marshal(map[string]any{
		"sign_state":       signState,
		"account_age_days": round2(ageDays),
	})
	return Score{Value: &value, Confidence: ConfidenceAccountSigning, Raw: raw}
}

// bisqSignStateBand returns the base score for a Bisq SignState.name() and
// whether it was recognized (an unknown value degrades to Unavailable()).
func bisqSignStateBand(signState string) (float64, bool) {
	switch signState {
	case "BANNED":
		return bisqSignBandBanned, true
	case "UNSIGNED":
		return bisqSignBandUnsigned, true
	case "PEER_INITIAL":
		return bisqSignBandPeerInitial, true
	case "PEER_LIMIT_LIFTED":
		return bisqSignBandPeerLimitLifted, true
	case "PEER_SIGNER":
		return bisqSignBandPeerSigner, true
	case "ARBITRATOR":
		return bisqSignBandArbitrator, true
	default:
		return 0, false
	}
}

// ApplyDisputePenalty subtracts a bounded per-dispute penalty from an existing
// Score's Value (floored at 0), for exchanges that expose a lost-dispute count
// as a separate signal from the rolling feedback average (Peach). It no-ops
// (returns s unchanged) when s.Value is nil.
func ApplyDisputePenalty(s Score, disputesLost int, pointsPerLoss float64) Score {
	if s.Value == nil || disputesLost <= 0 {
		return s
	}
	v := clamp0100(round2(*s.Value - float64(disputesLost)*pointsPerLoss))
	s.Value = &v
	return s
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func clamp01(v float64) float64 { return clamp(v, 0, 1) }

func clamp0100(v float64) float64 { return clamp(v, 0, 100) }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
