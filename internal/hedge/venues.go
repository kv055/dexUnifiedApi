package hedge

import (
	"fmt"
	"strings"
	"time"
)

type Venue string

const (
	VenueKrakenFutures   Venue = "kraken_futures"
	VenueCoinbaseFutures Venue = "coinbase_futures"
)

type VenueProfile struct {
	Venue                 Venue
	Instrument            string
	MaxLeverage           float64
	MaintenanceMarginRate float64
	FallbackOpenFeeRate   float64
	FallbackCloseFeeRate  float64
	FallbackFundingRate   float64
}

func defaultVenueProfiles() map[Venue]VenueProfile {
	cfg, err := loadHedgeConfig()
	if err != nil {
		return map[Venue]VenueProfile{
			VenueKrakenFutures: {
				Venue:                 VenueKrakenFutures,
				Instrument:            "PI_XBTUSD",
				MaxLeverage:           5,
				MaintenanceMarginRate: 0.01,
				FallbackOpenFeeRate:   0.0005,
				FallbackCloseFeeRate:  0.0005,
				FallbackFundingRate:   0.0001,
			},
			VenueCoinbaseFutures: {
				Venue:                 VenueCoinbaseFutures,
				Instrument:            "BTC-PERP",
				MaxLeverage:           10,
				MaintenanceMarginRate: 0.01,
				FallbackOpenFeeRate:   0.0006,
				FallbackCloseFeeRate:  0.0006,
				FallbackFundingRate:   0.0001,
			},
		}
	}

	out := make(map[Venue]VenueProfile, len(cfg.Venues))
	for name, venueCfg := range cfg.Venues {
		profileVenue := Venue(name)
		out[profileVenue] = VenueProfile{
			Venue:                 profileVenue,
			Instrument:            venueCfg.Instrument,
			MaxLeverage:           venueCfg.MaxLeverage,
			MaintenanceMarginRate: venueCfg.MaintenanceMarginRate,
			FallbackOpenFeeRate:   venueCfg.FallbackOpenFeeRate,
			FallbackCloseFeeRate:  venueCfg.FallbackCloseFeeRate,
			FallbackFundingRate:   venueCfg.FallbackFundingRate,
		}
	}
	return out
}

func DefaultVenueProfile(venue Venue) (VenueProfile, error) {
	profile, ok := defaultVenueProfiles()[venue]
	if !ok {
		return VenueProfile{}, fmt.Errorf("unsupported hedge venue %q", venue)
	}
	return profile, nil
}

func DefaultVenueProfileByName(name string) (VenueProfile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "kraken", "kraken_futures", "kraken futures":
		return DefaultVenueProfile(VenueKrakenFutures)
	case "coinbase", "coinbase_futures", "coinbase futures":
		return DefaultVenueProfile(VenueCoinbaseFutures)
	default:
		return VenueProfile{}, fmt.Errorf("unsupported hedge venue %q", name)
	}
}

// ApplyVenueProfile copies the hardcoded venue defaults into the optimizer config.
// This keeps venue selection outside the optimizer while removing the need to
// source leverage caps, maintenance margin, or standard symbols elsewhere.
func ApplyVenueProfile(cfg Config, profile VenueProfile) Config {
	cfg.MaxLeverage = profile.MaxLeverage
	cfg.MaintenanceMarginRate = profile.MaintenanceMarginRate
	return cfg
}

func venueFundingInterval(venue Venue) time.Duration {
	cfg, err := loadHedgeConfig()
	if err == nil {
		if v, ok := cfg.Venues[string(venue)]; ok && v.FundingIntervalHours > 0 {
			return time.Duration(v.FundingIntervalHours * float64(time.Hour))
		}
	}
	if venue == VenueKrakenFutures {
		return 4 * time.Hour
	}
	return 0
}
