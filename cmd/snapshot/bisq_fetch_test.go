package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	bisqgrpc "dexunifiedapi/bisq_grpc/go"

	"google.golang.org/grpc"
)

func TestMapBisqOfferSellDirectionUsesBuyerDeposit(t *testing.T) {
	o := &bisqgrpc.OfferInfo{
		Id:                     "offer-1",
		Price:                  "54000.50",
		Amount:                 50_000_000, // 0.5 BTC in sats
		MinAmount:              10_000_000, // 0.1 BTC in sats
		Volume:                 "27000",
		MinVolume:              "5400",
		PaymentMethodShortName: "SEPA",
		BuyerSecurityDeposit:   7_500_000, // 0.075 BTC
		SellerSecurityDeposit:  2_500_000, // 0.025 BTC
	}

	got := mapBisqOffer(o, "ask", time.Now())

	if got.OfferID != "offer-1" {
		t.Fatalf("OfferID = %q, want offer-1", got.OfferID)
	}
	if got.Price != 54000.50 {
		t.Fatalf("Price = %v, want 54000.50", got.Price)
	}
	if got.BTCMax == nil || *got.BTCMax != 0.5 {
		t.Fatalf("BTCMax = %v, want 0.5", got.BTCMax)
	}
	if got.BTCMin == nil || *got.BTCMin != 0.1 {
		t.Fatalf("BTCMin = %v, want 0.1", got.BTCMin)
	}
	if got.FiatMax == nil || *got.FiatMax != 27000 {
		t.Fatalf("FiatMax = %v, want 27000", got.FiatMax)
	}
	if got.FiatMin == nil || *got.FiatMin != 5400 {
		t.Fatalf("FiatMin = %v, want 5400", got.FiatMin)
	}
	if got.FiatCurrency != "EUR" {
		t.Fatalf("FiatCurrency = %q, want EUR", got.FiatCurrency)
	}
	if len(got.PaymentMethods) != 1 || got.PaymentMethods[0] != "SEPA" {
		t.Fatalf("PaymentMethods = %v, want [SEPA]", got.PaymentMethods)
	}
	if got.PriceType == nil || *got.PriceType != "fixed" {
		t.Fatalf("PriceType = %v, want fixed", got.PriceType)
	}
	// Taker on a sell offer buys → posts the BUYER deposit.
	if got.SecurityDepositBTC == nil || *got.SecurityDepositBTC != 0.075 {
		t.Fatalf("SecurityDepositBTC = %v, want 0.075 (buyer deposit)", got.SecurityDepositBTC)
	}
	if got.SecurityDepositPct == nil || *got.SecurityDepositPct != 0.15 {
		t.Fatalf("SecurityDepositPct = %v, want 0.15", got.SecurityDepositPct)
	}
	// Maker on a sell offer sells → posts the SELLER deposit.
	if got.CounterpartyStakeBTC == nil || *got.CounterpartyStakeBTC != 0.025 {
		t.Fatalf("CounterpartyStakeBTC = %v, want 0.025 (seller deposit)", got.CounterpartyStakeBTC)
	}
	if got.CounterpartyStakePct == nil || *got.CounterpartyStakePct != 0.05 {
		t.Fatalf("CounterpartyStakePct = %v, want 0.05", got.CounterpartyStakePct)
	}
	// Unpatched daemon: empty sign-state → rating unavailable, not a crash.
	if got.SellerRatingScore != nil {
		t.Fatalf("SellerRatingScore = %v, want nil for empty sign-state", *got.SellerRatingScore)
	}
}

func TestMapBisqOfferBuyDirectionUsesSellerDeposit(t *testing.T) {
	o := &bisqgrpc.OfferInfo{
		Id:                    "offer-2",
		Price:                 "53000",
		Amount:                100_000_000, // 1 BTC
		BuyerSecurityDeposit:  7_500_000,
		SellerSecurityDeposit: 2_500_000,
		UseMarketBasedPrice:   true,
	}

	got := mapBisqOffer(o, "bid", time.Now())

	// Taker on a buy offer sells → posts the SELLER deposit.
	if got.SecurityDepositBTC == nil || *got.SecurityDepositBTC != 0.025 {
		t.Fatalf("SecurityDepositBTC = %v, want 0.025 (seller deposit)", got.SecurityDepositBTC)
	}
	if got.CounterpartyStakeBTC == nil || *got.CounterpartyStakeBTC != 0.075 {
		t.Fatalf("CounterpartyStakeBTC = %v, want 0.075 (buyer deposit)", got.CounterpartyStakeBTC)
	}
	if got.CounterpartyStakePct == nil || *got.CounterpartyStakePct != 0.075 {
		t.Fatalf("CounterpartyStakePct = %v, want 0.075", got.CounterpartyStakePct)
	}
	if got.PriceType == nil || *got.PriceType != "floating" {
		t.Fatalf("PriceType = %v, want floating for market-based price", got.PriceType)
	}
}

func TestMapBisqOfferZeroAmountSkipsDeposit(t *testing.T) {
	got := mapBisqOffer(&bisqgrpc.OfferInfo{Id: "z", Price: "1", Amount: 0}, "ask", time.Now())
	if got.SecurityDepositBTC != nil || got.SecurityDepositPct != nil {
		t.Fatal("deposit fields should be nil for zero-amount offer")
	}
	if got.CounterpartyStakeBTC != nil || got.CounterpartyStakePct != nil {
		t.Fatal("counterparty stake fields should be nil for zero-amount offer")
	}
}

func TestMapBisqOfferBannedRatingIsZero(t *testing.T) {
	got := mapBisqOffer(&bisqgrpc.OfferInfo{
		Id:                         "banned",
		Price:                      "53000",
		Amount:                     100_000_000,
		AccountAgeWitnessSignState: "BANNED",
		AccountAgeWitnessDate:      time.Now().UnixMilli(),
	}, "ask", time.Now())
	if got.SellerRatingScore == nil || *got.SellerRatingScore != 0 {
		t.Fatalf("SellerRatingScore = %v, want 0 for banned maker", got.SellerRatingScore)
	}
	if got.SellerRatingConfidence == nil || *got.SellerRatingConfidence != "account_signing" {
		t.Fatalf("SellerRatingConfidence = %v, want account_signing", got.SellerRatingConfidence)
	}
}

func TestMapBisqOfferWitnessRating(t *testing.T) {
	now := time.Now()
	o := &bisqgrpc.OfferInfo{
		Id:                         "offer-3",
		Price:                      "53000",
		Amount:                     100_000_000,
		AccountAgeWitnessSignState: "PEER_SIGNER",
		AccountAgeWitnessDate:      now.Add(-90 * 24 * time.Hour).UnixMilli(),
	}

	got := mapBisqOffer(o, "ask", now)

	if got.SellerRatingScore == nil {
		t.Fatal("SellerRatingScore = nil, want a score for a signed witness")
	}
	if got.SellerRatingConfidence == nil {
		t.Fatal("SellerRatingConfidence = nil, want account-signing confidence")
	}
	if len(got.SellerRatingRaw) == 0 {
		t.Fatal("SellerRatingRaw empty, want the raw sign-state payload")
	}
}

// --- Wire-path tests: drive fetchBisq against an in-process gRPC server ---

type fakeOffersServer struct {
	bisqgrpc.UnimplementedOffersServer
	offers map[string][]*bisqgrpc.OfferInfo // keyed by direction (upper-case)
	err    error
	gotReq *bisqgrpc.GetOffersRequest
}

func (s *fakeOffersServer) GetOffers(_ context.Context, req *bisqgrpc.GetOffersRequest) (*bisqgrpc.GetOffersReply, error) {
	s.gotReq = req
	if s.err != nil {
		return nil, s.err
	}
	return &bisqgrpc.GetOffersReply{Offers: s.offers[req.Direction]}, nil
}

// startFakeBisqDaemon serves the Offers service on a random localhost port and
// points the package-level bisqGRPCHost at it for the duration of the test.
func startFakeBisqDaemon(t *testing.T, impl *fakeOffersServer) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer()
	bisqgrpc.RegisterOffersServer(srv, impl)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	oldHost := bisqGRPCHost
	bisqGRPCHost = lis.Addr().String()
	t.Cleanup(func() { bisqGRPCHost = oldHost })
}

func TestFetchBisqMapsOffersFromWire(t *testing.T) {
	startFakeBisqDaemon(t, &fakeOffersServer{offers: map[string][]*bisqgrpc.OfferInfo{
		"SELL": {
			{Id: "w1", Price: "54000", Amount: 50_000_000, BuyerSecurityDeposit: 7_500_000},
			{Id: "w2", Price: "54100", Amount: 25_000_000, BuyerSecurityDeposit: 3_750_000},
		},
	}})

	result := fetchBisq(context.Background(), "ask", 54000)
	if result == nil {
		t.Fatal("fetchBisq returned nil, want a populated result")
	}
	if len(result.Offers) != 2 {
		t.Fatalf("offers = %d, want 2", len(result.Offers))
	}
	if result.Offers[0].OfferID != "w1" || result.Offers[1].OfferID != "w2" {
		t.Fatalf("offer IDs = %q,%q, want w1,w2", result.Offers[0].OfferID, result.Offers[1].OfferID)
	}
	if result.BTCPrice != 54000 {
		t.Fatalf("BTCPrice = %v, want 54000", result.BTCPrice)
	}
}

func TestFetchBisqSendsUpperCaseDirectionAndEUR(t *testing.T) {
	impl := &fakeOffersServer{}
	startFakeBisqDaemon(t, impl)

	if result := fetchBisq(context.Background(), "bid", 54000); result == nil {
		t.Fatal("fetchBisq returned nil for a healthy empty daemon")
	}
	if impl.gotReq == nil {
		t.Fatal("daemon never received a GetOffers request")
	}
	if impl.gotReq.Direction != "BUY" {
		t.Fatalf("Direction = %q, want BUY", impl.gotReq.Direction)
	}
	if impl.gotReq.CurrencyCode != "EUR" {
		t.Fatalf("CurrencyCode = %q, want EUR", impl.gotReq.CurrencyCode)
	}
}

// A reachable daemon with an empty offer book must yield a NON-nil zero-offer
// result: recordFetchResult then keeps bisq in the snapshot log with count 0
// (dex_exchange_up=0) instead of dropping the series. This is the "up but no
// offers" state that previously went invisible.
func TestFetchBisqEmptyBookIsOnlineWithZeroOffers(t *testing.T) {
	startFakeBisqDaemon(t, &fakeOffersServer{})

	result := fetchBisq(context.Background(), "ask", 54000)
	if result == nil {
		t.Fatal("fetchBisq returned nil for reachable daemon with empty book, want zero-offer result")
	}
	if len(result.Offers) != 0 {
		t.Fatalf("offers = %d, want 0", len(result.Offers))
	}
}

func TestFetchBisqRPCErrorReturnsNil(t *testing.T) {
	startFakeBisqDaemon(t, &fakeOffersServer{err: errors.New("daemon exploded")})

	if result := fetchBisq(context.Background(), "ask", 54000); result != nil {
		t.Fatalf("fetchBisq = %+v, want nil on RPC error", result)
	}
}

func TestFetchBisqZeroReferencePriceReturnsNil(t *testing.T) {
	if result := fetchBisq(context.Background(), "ask", 0); result != nil {
		t.Fatalf("fetchBisq = %+v, want nil when reference price unavailable", result)
	}
}
