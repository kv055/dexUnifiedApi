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
// maker's reputation — it lives behind a separate GET /v1/user/:id call. Fetching
// that for every Peach offer each snapshot cycle rate-limits us, so we only look
// it up for offers the server marks net-profitable at full volume
// (NetPremiumPct > 0), which is a handful. A tiny in-memory cache keeps the same
// profitable maker from being refetched every push.

const (
	peachExchangeName = "peachbitcoin"

	// Shrinkage parameters for Peach's peer-feedback rating (mirrors HodlHodl).
	peachRatingPriorMean   = 0.85
	peachRatingPriorWeight = 10
	peachVerifiedThreshold = 5
	// peachDisputePenaltyPts is subtracted per lost dispute, since a rolling
	// rating average alone can under-weight a clear-cut dispute loss.
	peachDisputePenaltyPts = 5.0

	// peachUserFetchTimeout bounds a single per-maker lookup.
	peachUserFetchTimeout = 8 * time.Second
	// peachReputationTTL is how long an in-memory cached maker reputation is
	// reused before refetching. Volume is tiny (profitable offers only), so this
	// only avoids refetching the same maker on every 1-minute push.
	peachReputationTTL = 6 * time.Hour
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
// rating is -1..1, so it is rescaled to 0..1 before the shared feedback formula,
// then a bounded penalty is applied per lost dispute. Pure — unit-testable.
func mapPeachReputation(r peachUserReputation) sellerrating.Score {
	rescaled := (r.Rating + 1) / 2
	raw, _ := json.Marshal(map[string]any{
		"rating":        r.Rating,
		"rating_count":  r.RatingCount,
		"trades":        r.Trades,
		"disputes_lost": r.Disputes.Lost,
	})
	s := sellerrating.NormalizeFeedbackRating(
		&rescaled, r.RatingCount,
		peachRatingPriorMean, peachRatingPriorWeight, peachVerifiedThreshold,
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

// Resolve returns a maker's reputation, using the cache when fresh and otherwise
// fetching. On a fetch failure it falls back to any stale cached value; ok is
// false only when there is no value at all (so the offer keeps no rating rather
// than a wrong one).
func (c *peachReputationCache) Resolve(ctx context.Context, makerID string) (sellerrating.Score, bool) {
	now := time.Now()
	c.mu.Lock()
	if e, ok := c.entries[makerID]; ok && now.Sub(e.fetchedAt) < c.ttl {
		c.mu.Unlock()
		return e.score, true
	}
	c.mu.Unlock()

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

// profitablePeachMakers returns offerID→makerID for the Peach offers that are
// net-profitable at full volume (NetPremiumPct > 0) and carry a maker id.
// makerByID maps every pushed offer's id to its maker id (empty for non-Peach).
// Pure — unit-testable.
func profitablePeachMakers(outOffers []liveOutOffer, makerByID map[int64]string) map[int64]string {
	out := make(map[int64]string)
	for _, o := range outOffers {
		if o.Exchange != peachExchangeName || o.NetPremiumPct <= 0 {
			continue
		}
		if mid := makerByID[o.ID]; mid != "" {
			out[o.ID] = mid
		}
	}
	return out
}

// resolveAndApplyPeachRatings looks up reputations for net-profitable Peach
// offers, then patches them onto the live hub and persists them to the offers
// table. Meant to run in a background goroutine so the snapshot's push is never
// blocked on Peach's (slow, rate-limited) API.
func resolveAndApplyPeachRatings(ctx context.Context, offers []LiveOffer, outOffers []liveOutOffer) {
	makerByID := make(map[int64]string, len(offers))
	for _, o := range offers {
		if o.Exchange == peachExchangeName && o.MakerID != "" {
			makerByID[o.ID] = o.MakerID
		}
	}
	targets := profitablePeachMakers(outOffers, makerByID)
	if len(targets) == 0 {
		return
	}

	// Resolve once per distinct maker, then fan back out to their offers.
	scoreByMaker := make(map[string]sellerrating.Score)
	for _, makerID := range targets {
		if _, done := scoreByMaker[makerID]; done {
			continue
		}
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
