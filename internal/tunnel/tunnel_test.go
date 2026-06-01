package tunnel

import "testing"

func TestFindURL(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"2024-01-01 INF |  https://vic-workstation-bedding-spoken.trycloudflare.com  |", "https://vic-workstation-bedding-spoken.trycloudflare.com"},
		{"Your quick Tunnel has been created! Visit it at:", ""},
		{"https://example.com bukan trycloudflare", ""},
		{"+--------------------------------------------------------+", ""},
		{"https://abc123.trycloudflare.com", "https://abc123.trycloudflare.com"},
	}
	for _, c := range cases {
		if got := findURL(c.line); got != c.want {
			t.Errorf("findURL(%q)=%q, mau %q", c.line, got, c.want)
		}
	}
}
