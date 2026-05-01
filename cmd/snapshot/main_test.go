package main

import "testing"

func TestNormalizeHodlHodlPriceType(t *testing.T) {
	tests := []struct {
		name        string
		priceSource string
		want        string
	}{
		{
			name:        "fixed value",
			priceSource: "fixed_value",
			want:        "fixed",
		},
		{
			name:        "exchange rate",
			priceSource: "exchange_rate",
			want:        "floating",
		},
		{
			name:        "missing public price source",
			priceSource: "",
			want:        "unknown",
		},
		{
			name:        "unrecognized public price source",
			priceSource: "unexpected",
			want:        "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeHodlHodlPriceType(tc.priceSource)
			if got == nil {
				t.Fatalf("price type = nil, want %q", tc.want)
			}
			if *got != tc.want {
				t.Fatalf("price type = %q, want %q", *got, tc.want)
			}
		})
	}
}

func TestNonHodlHodlPriceTypeHelpersStayProviderSpecific(t *testing.T) {
	fixed := priceTypeFixed()
	if fixed == nil || *fixed != "fixed" {
		t.Fatalf("fixed price type = %#v, want fixed", fixed)
	}

	floating := priceTypeFloating()
	if floating == nil || *floating != "floating" {
		t.Fatalf("floating price type = %#v, want floating", floating)
	}
}
