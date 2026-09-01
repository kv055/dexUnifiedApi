// Package sellerrating normalizes the seller/maker reputation signals of
// several P2P exchanges into one 0-100 score plus a confidence tag, mirroring
// commons/paymentmethods as the established pattern for cross-exchange
// normalization.
//
// The 0-100 value is a within-methodology helper, not a cross-venue ranking:
// a HodlHodl 73 and a Bisq 2 72 are not the same kind of evidence. Confidence
// records which methodology produced the number (peer feedback, Bisq 2 bonded
// reputation, Bisq 1 account signing) so a bond or an empty book is never
// silently compared as if it were verified feedback. Raw preserves the source
// inputs so the score can be re-derived later without re-fetching.
//
// RoboSats fidelity bonds are not mapped onto this axis; fetchers store them
// on security_deposit_pct and leave the rating Unavailable(). NormalizeStakeBond
// remains for leftover rows and tests.
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
// offers.seller_rating_confidence enum.
type Confidence string

const (
	// ConfidenceUnavailable means no reputation signal was obtainable (empty
	// feedback sample, unstaked Bisq 2, RoboSats, unpatched Bisq 1). Paired
	// with a nil Score.Value.
	ConfidenceUnavailable Confidence = "unavailable"
	// ConfidenceFeedbackNew is a peer-feedback exchange whose sample count is
	// below the trust threshold — the score is real but dominated by the prior.
	ConfidenceFeedbackNew Confidence = "feedback_new"
	// ConfidenceFeedbackVerified is a peer-feedback exchange with enough trades
	// behind the rating to trust it.
	ConfidenceFeedbackVerified Confidence = "feedback_verified"
	// ConfidenceStakeBonded is trust derived from a raw financial bond
	// percentage rather than peer feedback. Current fetchers no longer write
	// this (RoboSats bonds stay on security_deposit_pct); the tag remains for
	// leftover rows until the next snapshot overwrites them.
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

// Default shrinkage for every peer-feedback exchange (HodlHodl, Peach). A
// conservative 0.50 prior with weight 20 keeps a lucky first trade near 52
// rather than minting a green 85; verifiedThreshold 20 is the sample count at
// which Confidence flips from feedback_new to feedback_verified.
const (
	DefaultFeedbackPriorMean         = 0.50
	DefaultFeedbackPriorWeight       = 20.0
	DefaultFeedbackVerifiedThreshold = 20
)

// NormalizeFeedbackRating converts a peer-feedback average (already rescaled by
// the caller into [0,1]) plus a sample count into a shrinkage-adjusted 0-100
// score. It is shared by every "feedback average + sample count" exchange
// (HodlHodl, Peach) — each rescales its own raw range to [0,1] first.
//
// rating == nil or sampleCount <= 0 means no usable feedback: the result is
// Unavailable() (nil Value) rather than a prior-only 50/85 that would look like
// a real score. sampleCount is the number of trades/ratings behind rating.
// priorMean is the conservative prior in [0,1]; priorWeight is the shrinkage
// strength (how many fictitious prior observations). A brand-new account's
// lucky first trade is pulled toward the prior so it cannot outrank a long
// track record. Confidence is ConfidenceFeedbackVerified once sampleCount >=
// verifiedThreshold, else ConfidenceFeedbackNew. raw is passed through
// verbatim into Score.Raw, including on the unavailable path.
func NormalizeFeedbackRating(rating *float64, sampleCount int, priorMean, priorWeight float64, verifiedThreshold int, raw json.RawMessage) Score {
	if rating == nil || sampleCount <= 0 {
		return Score{Value: nil, Confidence: ConfidenceUnavailable, Raw: raw}
	}

	n := float64(sampleCount)
	adjusted := *rating
	if denom := n + priorWeight; denom > 0 {
		adjusted = (*rating*n + priorMean*priorWeight) / denom
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
// bucketed by the exchange) onto 0-100. Kept for tests and leftover callers;
// Bisq 2 ingest uses NormalizeBisq2TotalScore so unstaked makers are
// Unavailable() rather than an honest 0. Always ConfidenceReputationBonded
// when a number is produced.
func NormalizeReputationScore(stars, maxStars float64, raw json.RawMessage) Score {
	frac := 0.0
	if maxStars > 0 {
		frac = stars / maxStars
	}
	value := clamp0100(round2(clamp01(frac) * 100))
	return Score{Value: &value, Confidence: ConfidenceReputationBonded, Raw: raw}
}

// Bisq Easy five-system buckets (ReputationScore.toFiveSystemScore-style
// thresholds) mapped onto a conservative 20-100 scale. totalScore <= 0 is not
// a bucket: that is "no stake yet", returned as Unavailable() so it is not
// confused with a banned / score-0 maker.
//
//	totalScore        score
//	<= 0              unavailable
//	1 .. 1_199        20
//	1_200 .. 4_999    35
//	5_000 .. 14_999   50
//	15_000 .. 24_999  62
//	25_000 .. 39_999  72
//	40_000 .. 79_999  82
//	80_000 .. 199_999 92
//	>= 200_000        100
func bisq2ScoreForTotal(totalScore int64) (float64, bool) {
	switch {
	case totalScore <= 0:
		return 0, false
	case totalScore < 1_200:
		return 20, true
	case totalScore < 5_000:
		return 35, true
	case totalScore < 15_000:
		return 50, true
	case totalScore < 25_000:
		return 62, true
	case totalScore < 40_000:
		return 72, true
	case totalScore < 80_000:
		return 82, true
	case totalScore < 200_000:
		return 92, true
	default:
		return 100, true
	}
}

// NormalizeBisq2TotalScore maps Bisq 2's bonded-reputation totalScore onto
// 0-100 using the piecewise table above. fiveSystemScore and ranking belong
// in raw (callers stash them) but do not drive the number: a 0-star unstaked
// maker is Unavailable(), not 0. Always ConfidenceReputationBonded when a
// score is produced.
func NormalizeBisq2TotalScore(totalScore int64, raw json.RawMessage) Score {
	value, ok := bisq2ScoreForTotal(totalScore)
	if !ok {
		return Score{Value: nil, Confidence: ConfidenceUnavailable, Raw: raw}
	}
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
