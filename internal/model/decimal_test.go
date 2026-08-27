package model

import "testing"

func TestParseDecimalIsExact(t *testing.T) {
	t.Parallel()
	tests := []struct {
		left       string
		right      string
		comparison int
	}{
		{left: "0.0000001", right: "1e-7", comparison: 0},
		{left: "22.5", right: "2.25e1", comparison: 0},
		{left: "0.1000000000000000001", right: "0.1", comparison: 1},
		{left: ".01", right: "1", comparison: -1},
	}
	for _, test := range tests {
		left, err := ParseDecimal(test.left)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", test.left, err)
		}
		right, err := ParseDecimal(test.right)
		if err != nil {
			t.Fatalf("ParseDecimal(%q): %v", test.right, err)
		}
		if comparison := left.Cmp(right); comparison != test.comparison {
			t.Fatalf("comparison of %q and %q = %d, want %d", test.left, test.right, comparison, test.comparison)
		}
	}
}

func TestParseDecimalRejectsMalformedValues(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", ".", "1.2.3", "NaN", "1e", "1e10001"} {
		if _, err := ParseDecimal(value); err == nil {
			t.Fatalf("ParseDecimal(%q) succeeded", value)
		}
	}
}
