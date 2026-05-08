package interfaces

import "testing"

func TestParseStrategyFromClientOrderID(t *testing.T) {
	cases := []struct {
		name string
		coid string
		want string
	}{
		{"empty", "", ""},
		{"no colon", "deadbeef-1234", ""},
		{"leading colon", ":no-strategy-prefix", ""},
		{"trend tagged", "trend:8f3a1c-de4f-1234", "trend"},
		{"penny tagged", "penny-momentum:abcd-efgh", "penny-momentum"},
		{"harvest tagged", "harvest:0001", "harvest"},
		{"multi-colon (only first split counts)", "trend:abc:def", "trend"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseStrategyFromClientOrderID(tc.coid)
			if got != tc.want {
				t.Errorf("ParseStrategyFromClientOrderID(%q) = %q, want %q", tc.coid, got, tc.want)
			}
		})
	}
}
