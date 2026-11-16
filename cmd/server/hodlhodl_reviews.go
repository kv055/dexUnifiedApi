package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hodl Hodl's API exposes no registration date and no review list for a
// trader (checked 2026-11-15: the docs at hodlhodl.com/api/docs list only
// users/me for users and a my_review field; /api/v1/users/:login and
// /api/v1/reviews are unrouted; /reviews answers JSON requests with 406; the
// site's own JS only POSTs/PATCHes reviews). The public review list
// (hodlhodl.com/reviews?user=<login>) is HTML but dates every review. A trader
// is at least as old as their oldest review, so that date gives a lower bound
// on account age. The list is HTML, newest first, 25 per page, with a link to
// the last page — so at most two requests per trader. Results feed
// seller_rating_signals only, not the 0-100 score.

const (
	hodlHodlExchangeName = "hodlhodl"

	hodlHodlReviewFetchTimeout = 10 * time.Second
	// hodlHodlReviewTTL is long because the oldest review never changes; the
	// refresh only keeps reviews_count current.
	hodlHodlReviewTTL = 24 * time.Hour
	// hodlHodlUncachedFetchCap bounds cache-miss traders per snapshot cycle.
	// Each costs up to two page loads, fetched sequentially.
	hodlHodlUncachedFetchCap   = 10
	hodlHodlReviewPageMaxBytes = 2 << 20
)

var hodlHodlHTTPClient = &http.Client{Timeout: 15 * time.Second}

// hodlHodlWebBaseURL is a var so tests can point the fetcher at httptest.
var hodlHodlWebBaseURL = "https://hodlhodl.com"

var (
	hodlHodlReviewTimestampRe = regexp.MustCompile(`class=['"]timestamp['"]>[^<]*?(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}) UTC`)
	hodlHodlReviewPageLinkRe  = regexp.MustCompile(`[?&](?:amp;)?page=(\d+)`)
)

// hodlHodlReviewPageMarker is page text present on every review list (also
// with zero reviews); its absence means the layout changed and parsing would
// silently find nothing.
const hodlHodlReviewPageMarker = "All reviews for"

// hodlHodlReviewStats is what one trader's review list tells us.
type hodlHodlReviewStats struct {
	OldestReviewAt time.Time // zero when the trader has no reviews
	ReviewPages    int
	ReviewsCount   int // full pages × page size + reviews on the last page
}

// parseHodlHodlReviewPage extracts review timestamps and the highest page
// number linked from one review list page. Pure — unit-testable.
func parseHodlHodlReviewPage(page string) (times []time.Time, lastPage int, err error) {
	if !strings.Contains(page, hodlHodlReviewPageMarker) {
		return nil, 0, fmt.Errorf("unexpected review page layout")
	}
	for _, m := range hodlHodlReviewTimestampRe.FindAllStringSubmatch(page, -1) {
		t, perr := time.ParseInLocation("2006-01-02 15:04:05", m[1], time.UTC)
		if perr == nil {
			times = append(times, t)
		}
	}
	lastPage = 1
	for _, m := range hodlHodlReviewPageLinkRe.FindAllStringSubmatch(page, -1) {
		if n, perr := strconv.Atoi(m[1]); perr == nil && n > lastPage {
			lastPage = n
		}
	}
	return times, lastPage, nil
}

func fetchHodlHodlReviewPage(ctx context.Context, login string, page int) (string, error) {
	q := url.Values{"user": {login}}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hodlHodlWebBaseURL+"/reviews?"+q.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := hodlHodlHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, hodlHodlReviewPageMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > hodlHodlReviewPageMaxBytes {
		return "", fmt.Errorf("review page exceeds %d bytes", hodlHodlReviewPageMaxBytes)
	}
	return string(body), nil
}

// fetchHodlHodlReviewStats loads the first review page and, when the list is
// paginated, the last one (where the oldest reviews are).
func fetchHodlHodlReviewStats(ctx context.Context, login string) (hodlHodlReviewStats, error) {
	var st hodlHodlReviewStats
	first, err := fetchHodlHodlReviewPage(ctx, login, 1)
	if err != nil {
		return st, err
	}
	firstTimes, lastPage, err := parseHodlHodlReviewPage(first)
	if err != nil {
		return st, err
	}
	st.ReviewPages = lastPage
	lastTimes := firstTimes
	if lastPage > 1 {
		last, err := fetchHodlHodlReviewPage(ctx, login, lastPage)
		if err != nil {
			return st, err
		}
		if lastTimes, _, err = parseHodlHodlReviewPage(last); err != nil {
			return st, err
		}
		if len(lastTimes) == 0 {
			return st, fmt.Errorf("last review page %d is empty", lastPage)
		}
	}
	// Page 1 is full whenever there is more than one page, so its length is
	// the page size.
	st.ReviewsCount = (lastPage-1)*len(firstTimes) + len(lastTimes)
	for _, t := range lastTimes {
		if st.OldestReviewAt.IsZero() || t.Before(st.OldestReviewAt) {
			st.OldestReviewAt = t
		}
	}
	return st, nil
}

// hodlHodlReviewSignals is the patch merged into a Hodl Hodl offer's
// seller_rating_signals. Pure — unit-testable.
func hodlHodlReviewSignals(st hodlHodlReviewStats, now time.Time) map[string]any {
	patch := map[string]any{
		"reviews_count": st.ReviewsCount,
		"review_pages":  st.ReviewPages,
	}
	if !st.OldestReviewAt.IsZero() {
		days := int(now.Sub(st.OldestReviewAt).Hours() / 24)
		if days < 0 {
			days = 0
		}
		patch["oldest_review_at"] = st.OldestReviewAt.UTC().Format(time.RFC3339)
		patch["min_account_age_days"] = days
	}
	return patch
}

// hodlHodlReviewCache is a per-trader in-memory cache; the server is
// long-running, so it survives between snapshot pushes.
type hodlHodlReviewCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]hodlHodlReviewEntry
}

type hodlHodlReviewEntry struct {
	stats     hodlHodlReviewStats
	fetchedAt time.Time
}

func newHodlHodlReviewCache(ttl time.Duration) *hodlHodlReviewCache {
	return &hodlHodlReviewCache{ttl: ttl, entries: make(map[string]hodlHodlReviewEntry)}
}

var hodlHodlReviews = newHodlHodlReviewCache(hodlHodlReviewTTL)

func (c *hodlHodlReviewCache) peekFresh(login string, now time.Time) (hodlHodlReviewStats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[login]
	if !ok || now.Sub(e.fetchedAt) >= c.ttl {
		return hodlHodlReviewStats{}, false
	}
	return e.stats, true
}

// Resolve returns a trader's review stats, from cache when fresh, otherwise
// fetched; a failed fetch falls back to any stale entry.
func (c *hodlHodlReviewCache) Resolve(ctx context.Context, login string) (hodlHodlReviewStats, bool) {
	now := time.Now()
	if st, ok := c.peekFresh(login, now); ok {
		return st, true
	}
	fctx, cancel := context.WithTimeout(ctx, hodlHodlReviewFetchTimeout)
	st, err := fetchHodlHodlReviewStats(fctx, login)
	cancel()
	if err != nil {
		c.mu.Lock()
		e, ok := c.entries[login]
		c.mu.Unlock()
		if ok {
			return e.stats, true
		}
		fmt.Printf("[HodlHodlReviews] fetch failed for %s: %v\n", login, err)
		return hodlHodlReviewStats{}, false
	}
	c.mu.Lock()
	c.entries[login] = hodlHodlReviewEntry{stats: st, fetchedAt: now}
	c.mu.Unlock()
	return st, true
}

// resolveAndApplyHodlHodlReviews looks up review history for live Hodl Hodl
// offers, merges it into their seller_rating_signals on the live hub and in
// the offers table. Runs in a background goroutine like the Peach lookup.
func resolveAndApplyHodlHodlReviews(ctx context.Context, offers []LiveOffer, outOffers []liveOutOffer) {
	makerByID := make(map[int64]string, len(offers))
	for _, o := range offers {
		if o.Exchange == hodlHodlExchangeName && o.MakerID != "" {
			makerByID[o.ID] = o.MakerID
		}
	}
	targets := exchangeMakersWithID(hodlHodlExchangeName, outOffers, makerByID)
	if len(targets) == 0 {
		return
	}

	now := time.Now()
	statsByLogin := make(map[string]hodlHodlReviewStats)
	ordered := make([]string, 0, len(targets))
	for _, login := range targets {
		ordered = append(ordered, login)
		if st, ok := hodlHodlReviews.peekFresh(login, now); ok {
			statsByLogin[login] = st
		}
	}
	toFetch := pickUncachedFetchQueue(ordered, func(login string) bool {
		_, ok := statsByLogin[login]
		return ok
	}, hodlHodlUncachedFetchCap)
	for _, login := range toFetch {
		if st, ok := hodlHodlReviews.Resolve(ctx, login); ok {
			statsByLogin[login] = st
		}
	}

	patches := make(map[int64]map[string]any, len(targets))
	for offerID, login := range targets {
		if st, ok := statsByLogin[login]; ok {
			patches[offerID] = hodlHodlReviewSignals(st, now)
		}
	}
	if len(patches) == 0 {
		return
	}
	hub.mergeSellerSignals(patches)
	persistSellerSignalPatches(ctx, patches)
}

// mergeSignals returns base with patch's keys set. An empty or unparsable
// base starts from an empty object. Pure.
func mergeSignals(base json.RawMessage, patch map[string]any) json.RawMessage {
	merged := map[string]any{}
	if len(base) > 0 {
		_ = json.Unmarshal(base, &merged)
		if merged == nil {
			merged = map[string]any{}
		}
	}
	for k, v := range patch {
		merged[k] = v
	}
	b, err := json.Marshal(merged)
	if err != nil {
		return base
	}
	return b
}

// mergeSellerSignals merges extra keys into the in-memory offers'
// seller_rating_signals by id (keeping what the snapshot wrote) and
// rebroadcasts.
func (h *LiveHub) mergeSellerSignals(patches map[int64]map[string]any) {
	if len(patches) == 0 {
		return
	}
	h.offersMu.Lock()
	for i := range h.offers {
		if p, ok := patches[h.offers[i].ID]; ok {
			h.offers[i].SellerRatingSignals = mergeSignals(h.offers[i].SellerRatingSignals, p)
		}
	}
	h.offersMu.Unlock()
	h.broadcastAll()
}

// persistSellerSignalPatches merges the patches into the offers table with
// jsonb concatenation, so the snapshot-written keys stay intact.
func persistSellerSignalPatches(ctx context.Context, patches map[int64]map[string]any) {
	if db == nil {
		return
	}
	for id, p := range patches {
		b, err := json.Marshal(p)
		if err != nil {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE offers SET seller_rating_signals = COALESCE(seller_rating_signals, '{}'::jsonb) || ?::jsonb WHERE id=?`,
			string(b), id,
		); err != nil {
			fmt.Printf("[HodlHodlReviews] persist failed for offer %d: %v\n", id, err)
		}
	}
}
