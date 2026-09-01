# `internal/sellerrating/rating.go`

## Purpose

Normalize each P2P venue's maker-trust signal onto one stored `Score`: a 0–100
`Value` (or nil), a `Confidence` methodology tag, and a `Raw` JSON blob of the
source inputs. The number is a **within-methodology helper**, not a ranking that
can be compared across HodlHodl, Peach, Bisq 1, and Bisq 2.

RoboSats fidelity bonds are not mapped onto this axis. Snapshot stores them on
`security_deposit_pct` and leaves the rating `unavailable`.

## Runtime Role

Snapshot fetchers call `applyRating` once per offer. Peach ratings are applied
later by `cmd/server/peach_reputation.go` (live makers with a `maker_id`, 6h
cache, capped sequential fetches). The monitor reads `seller_rating_score` /
`seller_rating_confidence` / `seller_rating_raw` from the live offer payload
and colors the badge by methodology.

Functions in this package are pure: no DB or HTTP.

## Scoring rules

### Peer feedback (HodlHodl, Peach)

`NormalizeFeedbackRating` shrinks a `[0,1]` average toward a conservative prior:

```
adjusted = (rating * n + 0.50 * 20) / (n + 20)
score    = 100 * adjusted
```

Constants (exported so HodlHodl and Peach cannot drift):

- `DefaultFeedbackPriorMean` = 0.50
- `DefaultFeedbackPriorWeight` = 20
- `DefaultFeedbackVerifiedThreshold` = 20 samples → `feedback_verified`, else `feedback_new`

Empty samples (`rating == nil` or `n <= 0`) return `unavailable` (nil score).
A single perfect trade is about 52, not 85. Peach's API sentinel `rating = -1`
is treated as no sample. Peach then subtracts 5 points per lost dispute
(`ApplyDisputePenalty`).

### Bisq 2

`NormalizeBisq2TotalScore` maps bonded-reputation `totalScore` (not linear
stars). `totalScore <= 0` is unstaked → `unavailable`, not score 0 (banned).

| totalScore | score |
| --- | --- |
| ≤ 0 | unavailable |
| 1–1,199 | 20 |
| 1,200–4,999 | 35 |
| 5,000–14,999 | 50 |
| 15,000–24,999 | 62 |
| 25,000–39,999 | 72 |
| 40,000–79,999 | 82 |
| 80,000–199,999 | 92 |
| ≥ 200,000 | 100 |

`five_system_score` and `ranking` stay in `Raw`. Confidence is
`reputation_bonded`.

### Bisq 1

`NormalizeBisqAccountSigning` uses sign-state bands (BANNED 0, UNSIGNED 20,
PEER_INITIAL 45, PEER_LIMIT_LIFTED 60, PEER_SIGNER 80, ARBITRATOR 90) plus up
to +10 for a year of account-age witness. Unknown/empty sign-state (unpatched
daemon) is `unavailable`. Confidence is `account_signing`.

### RoboSats

Fetchers call `Unavailable()`. `NormalizeStakeBond` remains in the package for
tests and leftover `stake_bonded` rows until the next snapshot overwrites them.
The monitor shows a bond chip from `security_deposit_pct` instead of a 0–100.

## Function Map

### `NormalizeFeedbackRating`

Signature:

```go
func NormalizeFeedbackRating(rating *float64, sampleCount int, priorMean, priorWeight float64, verifiedThreshold int, raw json.RawMessage) Score
```

Description:
Bayesian shrinkage of a rescaled `[0,1]` average. Returns `unavailable` when
there is no sample. Callers pass `DefaultFeedback*` constants.

Called by:
- `cmd/snapshot.mapHodlHodlTrader`
- `cmd/server.mapPeachReputation`

### `NormalizeBisq2TotalScore`

Signature:

```go
func NormalizeBisq2TotalScore(totalScore int64, raw json.RawMessage) Score
```

Description:
Piecewise map of Bisq Easy `totalScore` onto 20–100, or `unavailable` when
unstaked.

Called by:
- `cmd/snapshot.fetchBisq2Currency`

### `NormalizeBisqAccountSigning`

Signature:

```go
func NormalizeBisqAccountSigning(signState string, witnessDateMillis int64, now time.Time) Score
```

Description:
Sign-state band plus bounded account-age bonus. `BANNED` stays at 0 with no
age bonus.

Called by:
- `cmd/snapshot` Bisq 1 offer mapping

### `Unavailable`

Returns `{Value: nil, Confidence: unavailable, Raw: nil}`. Used by RoboSats
and by HodlHodl when the rating is missing or malformed.

### `ApplyDisputePenalty`

Subtracts `pointsPerLoss * disputesLost` from an existing score, floored at 0.
No-op when `Value` is nil. Peach uses 5 points per lost dispute.

## Monitor behavior

The badge colors by **confidence + score** (`feedback_new` is never green).
Settings “hide high-risk” only hides score 0 with `account_signing` or
`feedback_*` — not `unavailable`, leftover `stake_bonded`, or unstaked Bisq 2.

The 0–100 field is still one number on the offer so existing API clients keep
working; do not sort the live book by it across venues.
