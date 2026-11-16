package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"dexunifiedapi/internal/sellerrating"
)

// Peach is the only integrated exchange whose offer listing does NOT include the
// maker's reputation — it lives behind a separate GET /v1/user/:id call. We look
// it up for every live Peach offer that carries a maker id, with a 6h in-memory
// cache and a per-cycle fetch cap so a large book cannot stampede Peach's API.
// Remainder makers fill in on later snapshot cycles.

const (
	peachExchangeName = "peachbitcoin"

	// peachDisputePenaltyPts is subtracted per lost dispute, since a rolling
	// rating average alone can under-weight a clear-cut dispute loss.
	peachDisputePenaltyPts = 5.0

	// peachUserFetchTimeout bounds a single per-maker lookup.
	peachUserFetchTimeout = 8 * time.Second
	// peachReputationTTL is how long an in-memory cached maker reputation is
	// reused before refetching.
	peachReputationTTL = 6 * time.Hour
	// peachUncachedFetchCap bounds how many cache-miss HTTP lookups run in one
	// snapshot cycle (sequential). Cached makers are not counted.
	peachUncachedFetchCap = 15

	// peachProfileMaxBytes and peachRatingsMaxBytes cap response bodies. The
	// ratings list is one entry (~300 B) per rating received, so a 1000-trade
	// maker returns ~300 KB.
	peachProfileMaxBytes = 1 << 20
	peachRatingsMaxBytes = 8 << 20

	// peachRatingBurstWindow is the rolling window for max_ratings_24h: many
	// ratings packed into one day hints at farmed reputation.
	peachRatingBurstWindow = 24 * time.Hour
)

var peachHTTPClient = &http.Client{Timeout: 15 * time.Second}

// peachAPIBaseURL is a var so tests can point the fetchers at httptest.
var peachAPIBaseURL = "https://api.peachbitcoin.com"

// peachUserReputation is the maker profile returned by GET /v1/user/:id.
type peachUserReputation struct {
	CreationDate  string   `json:"creationDate"`
	Rating        float64  `json:"rating"` // -1..1
	RatingCount   int      `json:"ratingCount"`
	Trades        int      `json:"trades"`
	HistoryRating float64  `json:"historyRating"`
	PeachRating   float64  `json:"peachRating"`
	UserRating    float64  `json:"userRating"`
	RecentRating  float64  `json:"recentRating"`
	Medals        []string `json:"medals"`
	Disputes      struct {
		Opened int `json:"opened"`
		Won    int `json:"won"`
		Lost   int `json:"lost"`
	} `json:"disputes"`
}

// peachRating is one entry of GET /v1/user/:id/ratings. The signature is not
// decoded: it does not cover the date (one rater's repeat ratings of the same
// user carry an identical signature), so it cannot vouch for the history.
type peachRating struct {
	CreationDate string  `json:"creationDate"`
	Timestamp    int64   `json:"timestamp"` // epoch millis, same instant as CreationDate
	Rating       float64 `json:"rating"`    // +1 or -1
	RatedBy      string  `json:"ratedBy"`   // rater's public key
}

// peachRatingStats summarizes a maker's rating history into anti-farming
// signals. It feeds seller_rating_signals only, not the 0-100 score.
type peachRatingStats struct {
	Fetched         int
	DistinctRaters  int
	TopRaterRatings int // ratings left by the single most frequent rater
	FirstAt, LastAt time.Time
	MaxIn24h        int // most ratings inside any rolling 24h window
	// BeforeAccountCreation counts ratings dated before the account's
	// creationDate. Both dates come from Peach's server, so a non-zero value
	// means inconsistent data rather than manipulation by the maker.
	BeforeAccountCreation int
}

// peachMakerRating is the score plus extras from the user and ratings GETs.
type peachMakerRating struct {
	score   sellerrating.Score
	signals json.RawMessage
}

func peachRatingTime(r peachRating) (time.Time, bool) {
	if r.Timestamp > 0 {
		return time.UnixMilli(r.Timestamp).UTC(), true
	}
	return parsePeachCreationDate(r.CreationDate)
}

// summarizePeachRatings computes rater diversity, burst and consistency
// signals from a maker's rating list. accountCreated is ignored when
// haveCreated is false. Pure — unit-testable.
func summarizePeachRatings(ratings []peachRating, accountCreated time.Time, haveCreated bool) peachRatingStats {
	st := peachRatingStats{Fetched: len(ratings)}
	byRater := make(map[string]int, len(ratings))
	times := make([]time.Time, 0, len(ratings))
	for _, r := range ratings {
		if r.RatedBy != "" {
			byRater[r.RatedBy]++
		}
		t, ok := peachRatingTime(r)
		if !ok {
			continue
		}
		times = append(times, t)
		if haveCreated && t.Before(accountCreated) {
			st.BeforeAccountCreation++
		}
	}
	st.DistinctRaters = len(byRater)
	for _, n := range byRater {
		if n > st.TopRaterRatings {
			st.TopRaterRatings = n
		}
	}
	if len(times) == 0 {
		return st
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	st.FirstAt, st.LastAt = times[0], times[len(times)-1]
	start := 0
	for end := range times {
		for times[end].Sub(times[start]) >= peachRatingBurstWindow {
			start++
		}
		if n := end - start + 1; n > st.MaxIn24h {
			st.MaxIn24h = n
		}
	}
	return st
}

// mapPeachSignals builds the Peach seller_rating_signals blob. ratings is nil
// when the ratings lookup failed; the profile fields are still recorded.
func mapPeachSignals(r peachUserReputation, ratings *peachRatingStats, now time.Time) json.RawMessage {
	blob := map[string]any{
		"source":          "peach",
		"creation_date":   r.CreationDate,
		"trades":          r.Trades,
		"rating_count":    r.RatingCount,
		"user_rating":     r.UserRating,
		"peach_rating":    r.PeachRating,
		"recent_rating":   r.RecentRating,
		"history_rating":  r.HistoryRating,
		"medals":          r.Medals,
		"disputes_opened": r.Disputes.Opened,
		"disputes_won":    r.Disputes.Won,
		"disputes_lost":   r.Disputes.Lost,
	}
	if created, ok := parsePeachCreationDate(r.CreationDate); ok {
		days := int(now.Sub(created).Hours() / 24)
		if days < 0 {
			days = 0
		}
		blob["account_age_days"] = days
	}
	if ratings != nil {
		blob["ratings_fetched"] = ratings.Fetched
		blob["distinct_raters"] = ratings.DistinctRaters
		blob["top_rater_ratings"] = ratings.TopRaterRatings
		if ratings.Fetched > 0 {
			share := float64(ratings.TopRaterRatings) / float64(ratings.Fetched)
			blob["top_rater_share"] = math.Round(share*1000) / 1000
		}
		blob["max_ratings_24h"] = ratings.MaxIn24h
		blob["ratings_before_account_creation"] = ratings.BeforeAccountCreation
		if !ratings.FirstAt.IsZero() {
			blob["first_rating_at"] = ratings.FirstAt.UTC().Format(time.RFC3339)
			blob["last_rating_at"] = ratings.LastAt.UTC().Format(time.RFC3339)
		}
	}
	b, err := json.Marshal(blob)
	if err != nil {
		return nil
	}
	return b
}

func parsePeachCreationDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// mapPeachReputation normalizes a Peach maker profile into a Score. Peach's
// rating is -1..1 (sentinel -1 means "no rating yet"), so a real rating is
// rescaled to 0..1 before the shared feedback formula, then a bounded penalty
// is applied per lost dispute. Empty samples and the -1 sentinel are
// Unavailable() — we do not mint a prior-only score. Pure — unit-testable.
func mapPeachReputation(r peachUserReputation) sellerrating.Score {
	raw, _ := json.Marshal(map[string]any{
		"rating":        r.Rating,
		"rating_count":  r.RatingCount,
		"trades":        r.Trades,
		"disputes_lost": r.Disputes.Lost,
	})
	if r.RatingCount <= 0 || r.Rating < 0 {
		return sellerrating.Score{Value: nil, Confidence: sellerrating.ConfidenceUnavailable, Raw: raw}
	}
	rescaled := (r.Rating + 1) / 2
	s := sellerrating.NormalizeFeedbackRating(
		&rescaled, r.RatingCount,
		sellerrating.DefaultFeedbackPriorMean, sellerrating.DefaultFeedbackPriorWeight, sellerrating.DefaultFeedbackVerifiedThreshold,
		raw,
	)
	return sellerrating.ApplyDisputePenalty(s, r.Disputes.Lost, peachDisputePenaltyPts)
}

// fetchPeachUserReputation fetches one maker's profile with a caller-supplied
// context (for the per-call timeout).
func fetchPeachUserReputation(ctx context.Context, userID string) (peachUserReputation, error) {
	var rep peachUserReputation
	err := fetchPeachJSON(ctx, "/v1/user/"+url.PathEscape(userID), peachProfileMaxBytes, &rep)
	return rep, err
}

// fetchPeachUserRatings fetches every rating a maker has received (public, no
// auth). The endpoint returns the full list unpaginated.
func fetchPeachUserRatings(ctx context.Context, userID string) ([]peachRating, error) {
	var ratings []peachRating
	err := fetchPeachJSON(ctx, "/v1/user/"+url.PathEscape(userID)+"/ratings", peachRatingsMaxBytes, &ratings)
	return ratings, err
}

func fetchPeachJSON(ctx context.Context, path string, maxBytes int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, peachAPIBaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := peachHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return json.Unmarshal(body, out)
}

// fetchPeachRatingStats fetches and summarizes a maker's ratings. A failure is
// logged and yields nil so the profile-based score is still applied.
func fetchPeachRatingStats(ctx context.Context, makerID string, rep peachUserReputation) *peachRatingStats {
	fctx, cancel := context.WithTimeout(ctx, peachUserFetchTimeout)
	ratings, err := fetchPeachUserRatings(fctx, makerID)
	cancel()
	if err != nil {
		fmt.Printf("[PeachReputation] ratings fetch failed for %s: %v\n", makerID, err)
		return nil
	}
	created, ok := parsePeachCreationDate(rep.CreationDate)
	st := summarizePeachRatings(ratings, created, ok)
	return &st
}

// peachReputationCache is a small in-memory per-maker cache. The server is
// long-running, so it survives between snapshot pushes.
type peachReputationCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]peachCacheEntry
}

type peachCacheEntry struct {
	rating    peachMakerRating
	fetchedAt time.Time
}

func newPeachReputationCache(ttl time.Duration) *peachReputationCache {
	return &peachReputationCache{ttl: ttl, entries: make(map[string]peachCacheEntry)}
}

var peachReputations = newPeachReputationCache(peachReputationTTL)

func (c *peachReputationCache) peekFresh(makerID string, now time.Time) (peachMakerRating, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[makerID]
	if !ok || now.Sub(e.fetchedAt) >= c.ttl {
		return peachMakerRating{}, false
	}
	return e.rating, true
}

// Resolve returns a maker's reputation, using the cache when fresh and otherwise
// fetching. On a fetch failure it falls back to any stale cached value; ok is
// false only when there is no value at all (so the offer keeps no rating rather
// than a wrong one).
func (c *peachReputationCache) Resolve(ctx context.Context, makerID string) (peachMakerRating, bool) {
	now := time.Now()
	if s, ok := c.peekFresh(makerID, now); ok {
		return s, true
	}

	fctx, cancel := context.WithTimeout(ctx, peachUserFetchTimeout)
	rep, err := fetchPeachUserReputation(fctx, makerID)
	cancel()
	if err != nil {
		c.mu.Lock()
		e, ok := c.entries[makerID]
		c.mu.Unlock()
		if ok {
			return e.rating, true // slightly stale beats nothing
		}
		fmt.Printf("[PeachReputation] fetch failed for %s: %v\n", makerID, err)
		return peachMakerRating{}, false
	}
	rating := peachMakerRating{
		score:   mapPeachReputation(rep),
		signals: mapPeachSignals(rep, fetchPeachRatingStats(ctx, makerID, rep), now),
	}
	c.mu.Lock()
	c.entries[makerID] = peachCacheEntry{rating: rating, fetchedAt: now}
	c.mu.Unlock()
	return rating, true
}

// peachMakersWithID returns offerID→makerID for every live Peach offer that
// carries a maker id. Unlike the old profitable-only filter, unprofitable
// Peach offers are included so the monitor can show a rating on the whole book.
// Pure — unit-testable.
func peachMakersWithID(outOffers []liveOutOffer, makerByID map[int64]string) map[int64]string {
	return exchangeMakersWithID(peachExchangeName, outOffers, makerByID)
}

// exchangeMakersWithID returns offerID→makerID for every live offer of the
// given exchange that carries a maker id. Pure.
func exchangeMakersWithID(exchange string, outOffers []liveOutOffer, makerByID map[int64]string) map[int64]string {
	out := make(map[int64]string)
	for _, o := range outOffers {
		if o.Exchange != exchange {
			continue
		}
		if mid := makerByID[o.ID]; mid != "" {
			out[o.ID] = mid
		}
	}
	return out
}

// pickUncachedFetchQueue returns uncached maker IDs to fetch this cycle, at
// most cap, first-seen order, de-duplicated. Cached makers are skipped. Pure.
func pickUncachedFetchQueue(orderedMakers []string, isCached func(string) bool, cap int) []string {
	out := make([]string, 0, cap)
	seen := make(map[string]struct{})
	for _, id := range orderedMakers {
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if isCached(id) {
			continue
		}
		out = append(out, id)
		if cap >= 0 && len(out) >= cap {
			break
		}
	}
	return out
}

// resolveAndApplyPeachRatings looks up reputations for live Peach offers with a
// maker id, then patches them onto the live hub and persists them to the offers
// table. Meant to run in a background goroutine so the snapshot's push is never
// blocked on Peach's (slow, rate-limited) API. Cache hits are applied every
// cycle; uncached makers are fetched sequentially up to peachUncachedFetchCap.
func resolveAndApplyPeachRatings(ctx context.Context, offers []LiveOffer, outOffers []liveOutOffer) {
	makerByID := make(map[int64]string, len(offers))
	for _, o := range offers {
		if o.Exchange == peachExchangeName && o.MakerID != "" {
			makerByID[o.ID] = o.MakerID
		}
	}
	targets := peachMakersWithID(outOffers, makerByID)
	if len(targets) == 0 {
		return
	}

	now := time.Now()
	ratingByMaker := make(map[string]peachMakerRating)
	ordered := make([]string, 0, len(targets))
	for _, makerID := range targets {
		ordered = append(ordered, makerID)
		if _, done := ratingByMaker[makerID]; done {
			continue
		}
		if s, ok := peachReputations.peekFresh(makerID, now); ok {
			ratingByMaker[makerID] = s
		}
	}

	toFetch := pickUncachedFetchQueue(ordered, func(id string) bool {
		_, ok := ratingByMaker[id]
		return ok
	}, peachUncachedFetchCap)
	for _, makerID := range toFetch {
		if s, ok := peachReputations.Resolve(ctx, makerID); ok {
			ratingByMaker[makerID] = s
		}
	}

	byOfferID := make(map[int64]peachMakerRating, len(targets))
	for offerID, makerID := range targets {
		if s, ok := ratingByMaker[makerID]; ok {
			byOfferID[offerID] = s
		}
	}
	if len(byOfferID) == 0 {
		return
	}

	hub.applySellerRatings(byOfferID)
	persistPeachRatings(ctx, byOfferID)
}

// applySellerRatings patches seller-rating fields onto the in-memory offers by
// id and rebroadcasts, so ratings resolved after ingest reach subscribers.
func (h *LiveHub) applySellerRatings(byOfferID map[int64]peachMakerRating) {
	if len(byOfferID) == 0 {
		return
	}
	h.offersMu.Lock()
	for i := range h.offers {
		s, ok := byOfferID[h.offers[i].ID]
		if !ok {
			continue
		}
		h.offers[i].SellerRatingScore = s.score.Value
		conf := string(s.score.Confidence)
		h.offers[i].SellerRatingConfidence = &conf
		h.offers[i].SellerRatingRaw = s.score.Raw
		h.offers[i].SellerRatingSignals = s.signals
	}
	h.offersMu.Unlock()
	h.broadcastAll()
}

// persistPeachRatings writes the resolved ratings onto the offers table so the
// DB-backed views match the live feed (same columns as the snapshot writes).
func persistPeachRatings(ctx context.Context, byOfferID map[int64]peachMakerRating) {
	if db == nil {
		return
	}
	for id, s := range byOfferID {
		var raw any
		if len(s.score.Raw) > 0 {
			raw = string(s.score.Raw)
		}
		var signals any
		if len(s.signals) > 0 {
			signals = string(s.signals)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE offers SET seller_rating_score=?, seller_rating_confidence=?, seller_rating_raw=?::jsonb, seller_rating_signals=?::jsonb WHERE id=?`,
			s.score.Value, string(s.score.Confidence), raw, signals, id,
		); err != nil {
			fmt.Printf("[PeachReputation] persist failed for offer %d: %v\n", id, err)
		}
	}
}
