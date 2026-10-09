package websocket_test

import (
	"testing"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/websocket"
)

const (
	host        = "example.com"
	httpsOrigin = "https://" + host
)

func TestOriginAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		origin    string
		corsHosts []string
		want      bool
	}{
		{"wildcard allows anything", "https://anything.example", []string{"*"}, true},
		{"wildcard among others", "https://anything.example", []string{"a.example", "*"}, true},
		{"empty config denies", httpsOrigin, nil, false},

		{"bare host matches", httpsOrigin, []string{host}, true},
		{"bare host matches any port", "https://example.com:8443", []string{host}, true},
		{"bare host is case insensitive", "https://EXAMPLE.com", []string{"Example.COM"}, true},

		{"explicit 443 matches implicit https port", httpsOrigin, []string{"example.com:443"}, true},
		{"explicit 80 matches implicit http port", "http://example.com", []string{"example.com:80"}, true},
		{"explicit port matches", "http://example.com:8080", []string{"example.com:8080"}, true},
		{"port mismatch denies", "http://example.com:9090", []string{"example.com:8080"}, false},
		{"https origin does not match :80 config", httpsOrigin, []string{"example.com:80"}, false},

		{"wss scheme defaults to 443", "wss://example.com", []string{"example.com:443"}, true},
		{"full url config", httpsOrigin, []string{httpsOrigin}, true},
		{"full url config with path", httpsOrigin, []string{"https://example.com/app"}, true},

		// The old substring match let these through.
		{"suffix attack denied", "https://example.com.evil.test", []string{host}, false},
		{"prefix attack denied", "https://notexample.com", []string{host}, false},
		{"host embedded in path denied", "https://evil.test/example.com", []string{host}, false},

		{"ipv6 with port", "http://[::1]:8080", []string{"[::1]:8080"}, true},
		{"ipv6 without port", "http://[::1]:8080", []string{"[::1]"}, true},

		{"garbage origin denied", "not a url", []string{"*"}, false},
		{"null origin denied", "null", []string{host}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := websocket.OriginAllowed(tt.origin, tt.corsHosts); got != tt.want {
				t.Errorf("OriginAllowed(%q, %v) = %v, want %v", tt.origin, tt.corsHosts, got, tt.want)
			}
		})
	}
}
