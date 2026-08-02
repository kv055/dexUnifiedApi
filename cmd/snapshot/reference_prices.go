package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// bisqPriceNodes are the known Bisq price node onion addresses, tried in order.
var bisqPriceNodes = []string{
	"http://runbtcpn7gmbj5rgqeyfyvepqokrijem6rbw7o5wgqbguimuoxrmcdyd.onion",
	"http://ro7nv73awqs3ga2qtqeqawrjpbxwarsazznszvr6whv7tes5ehffopid.onion",
}

func priceBisqTor(fiat string) (int, error) {
	var lastErr error
	for _, node := range bisqPriceNodes {
		resp, err := torClient().Get(node + "/getAllMarketPrices")
		if err != nil {
			lastErr = err
			fmt.Printf("[RefPrice] bisq_tor: %s failed: %v\n", node, err)
			continue
		}
		body, _ := readAllLimited(resp.Body)
		resp.Body.Close()

		var result struct {
			Data []struct {
				CurrencyCode string  `json:"currencyCode"`
				Price        float64 `json:"price"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			lastErr = err
			continue
		}
		for _, c := range result.Data {
			if strings.EqualFold(c.CurrencyCode, fiat) {
				return int(c.Price), nil
			}
		}
		lastErr = fmt.Errorf("currency %s not found", fiat)
	}
	return 0, lastErr
}

func priceKraken(fiat string) (int, error) {
	pair := "XBT" + strings.ToUpper(fiat)
	resp, err := clearnetClient().Get("https://api.kraken.com/0/public/Ticker?pair=" + pair)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := readAllLimited(resp.Body)

	var data struct {
		Result map[string]struct {
			C []string `json:"c"`
		} `json:"result"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	for _, ticker := range data.Result {
		if len(ticker.C) > 0 {
			v, _ := strconv.ParseFloat(ticker.C[0], 64)
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("no ticker data for %s", pair)
}

func priceCoinGecko(fiat string) (int, error) {
	resp, err := clearnetClient().Get("https://api.coingecko.com/api/v3/simple/price?ids=bitcoin&vs_currencies=" + fiat)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := readAllLimited(resp.Body)

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return 0, err
	}
	if btc, ok := data["bitcoin"]; ok {
		if price, ok := btc[fiat]; ok {
			return int(price), nil
		}
	}
	return 0, fmt.Errorf("no price for %s", fiat)
}

func fetchReferencePrice(fiat string) int {
	allProviders := []struct {
		name string
		fn   func(string) (int, error)
	}{
		{"bisq_tor", priceBisqTor},
		{"kraken", priceKraken},
		{"coingecko", priceCoinGecko},
	}

	ordered := make([]struct {
		name string
		fn   func(string) (int, error)
	}, 0, len(allProviders))
	for _, provider := range allProviders {
		if provider.name == priceProvider {
			ordered = append([]struct {
				name string
				fn   func(string) (int, error)
			}{provider}, ordered...)
			continue
		}
		ordered = append(ordered, provider)
	}
	if len(ordered) == 0 || ordered[0].name != priceProvider {
		fmt.Printf("[RefPrice] Unknown provider '%s', falling back to all\n", priceProvider)
		ordered = allProviders
	}

	for _, provider := range ordered {
		price, err := provider.fn(fiat)
		if err != nil {
			fmt.Printf("[RefPrice] Error from %s: %v\n", provider.name, err)
			continue
		}
		fmt.Printf("[RefPrice] %s: %d %s\n", provider.name, price, strings.ToUpper(fiat))
		return price
	}
	return 0
}
