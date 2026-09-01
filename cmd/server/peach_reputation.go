package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
)

var peachHTTPClient = &http.Client{Timeout: 15 * time.Second}

// peachUserReputation is the maker profile returned by GET /v1/user/:id.
type peachUserReputation struct {
	Rating      float64 `json:"rating"` // -1..1
	RatingCount int     `json:"ratingCount"`
	Trades      int     `json:"trades"`
	Disputes    struct {
		Opened int `json:"opened"`
		Won    int `json:"won"`
		Lost   int `json:"lost"`
	} `json:"disputes"`
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
	reqURL := "https://api.peachbitcoin.com/v1/user/" + url.PathEscape(userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return rep, err
	}
	resp, err := peachHTTPClient.Do(req)
	if err != nil {
		return rep, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rep, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// peachReputationCache is a small in-memory per-maker cache. The server is
// long-running, so it survives between snapshot pushes.
type peachReputationCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]peachCacheEntry
}

type peachCacheEntry struct {
	score     sellerrating.Score
	fetchedAt time.Time
}

func newPeachReputationCache(ttl time.Duration) *peachReputationCache {
	return &peachReputationCache{ttl: ttl, entries: make(map[string]peachCacheEntry)}
}

var peachReputations = newPeachReputationCache(peachReputationTTL)

func (c *peachReputationCache) peekFresh(makerID string, now time.Time) (sellerrating.Score, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[makerID]
	if !ok || now.Sub(e.fetchedAt) >= c.ttl {
		return sellerrating.Score{}, false
	}
	return e.score, true
}

// Resolve returns a maker's reputation, using the cache when fresh and otherwise
// fetching. On a fetch failure it falls back to any stale cached value; ok is
// false only when there is no value at all (so the offer keeps no rating rather
// than a wrong one).
func (c *peachReputationCache) Resolve(ctx context.Context, makerID string) (sellerrating.Score, bool) {
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
			return e.score, true // slightly stale beats nothing
		}
		fmt.Printf("[PeachReputation] fetch failed for %s: %v\n", makerID, err)
		return sellerrating.Score{}, false
	}
	score := mapPeachReputation(rep)
	c.mu.Lock()
	c.entries[makerID] = peachCacheEntry{score: score, fetchedAt: now}
	c.mu.Unlock()
	return score, true
}

// peachMakersWithID returns offerID→makerID for every live Peach offer that
// carries a maker id. Unlike the old profitable-only filter, unprofitable
// Peach offers are included so the monitor can show a rating on the whole book.
// Pure — unit-testable.
func peachMakersWithID(outOffers []liveOutOffer, makerByID map[int64]string) map[int64]string {
	out := make(map[int64]string)
	for _, o := range outOffers {
		if o.Exchange != peachExchangeName {
			continue
		}
		if mid := makerByID[o.ID]; mid != "" {
			out[o.ID] = mid
		}
	}
	return out
}

// pickPeachFetchQueue returns uncached maker IDs to fetch this cycle, at most
// cap, first-seen order, de-duplicated. Cached makers are skipped. Pure.
func pickPeachFetchQueue(orderedMakers []string, isCached func(string) bool, cap int) []string {
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
	scoreByMaker := make(map[string]sellerrating.Score)
	ordered := make([]string, 0, len(targets))
	for _, makerID := range targets {
		ordered = append(ordered, makerID)
		if _, done := scoreByMaker[makerID]; done {
			continue
		}
		if s, ok := peachReputations.peekFresh(makerID, now); ok {
			scoreByMaker[makerID] = s
		}
	}

	toFetch := pickPeachFetchQueue(ordered, func(id string) bool {
		_, ok := scoreByMaker[id]
		return ok
	}, peachUncachedFetchCap)
	for _, makerID := range toFetch {
		if s, ok := peachReputations.Resolve(ctx, makerID); ok {
			scoreByMaker[makerID] = s
		}
	}

	byOfferID := make(map[int64]sellerrating.Score, len(targets))
	for offerID, makerID := range targets {
		if s, ok := scoreByMaker[makerID]; ok {
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
func (h *LiveHub) applySellerRatings(byOfferID map[int64]sellerrating.Score) {
	if len(byOfferID) == 0 {
		return
	}
	h.offersMu.Lock()
	for i := range h.offers {
		s, ok := byOfferID[h.offers[i].ID]
		if !ok {
			continue
		}
		h.offers[i].SellerRatingScore = s.Value
		conf := string(s.Confidence)
		h.offers[i].SellerRatingConfidence = &conf
		h.offers[i].SellerRatingRaw = s.Raw
	}
	h.offersMu.Unlock()
	h.broadcastAll()
}

// persistPeachRatings writes the resolved ratings onto the offers table so the
// DB-backed views match the live feed (same columns as the snapshot writes).
func persistPeachRatings(ctx context.Context, byOfferID map[int64]sellerrating.Score) {
	if db == nil {
		return
	}
	for id, s := range byOfferID {
		var raw any
		if len(s.Raw) > 0 {
			raw = string(s.Raw)
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE offers SET seller_rating_score=?, seller_rating_confidence=?, seller_rating_raw=?::jsonb WHERE id=?`,
			s.Value, string(s.Confidence), raw, id,
		); err != nil {
			fmt.Printf("[PeachReputation] persist failed for offer %d: %v\n", id, err)
		}
	}
}
