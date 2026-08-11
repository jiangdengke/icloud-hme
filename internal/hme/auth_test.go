package hme

import (
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

func TestAuthURLsFollowAccountRegion(t *testing.T) {
	state := &authState{frameId: "frame-1", clientId: OAuthClientID}

	tests := []struct {
		name string
		host string
		want string
	}{
		{name: "global", host: "icloud.com", want: "https://idmsa.apple.com/"},
		{name: "china", host: "icloud.com.cn", want: "https://idmsa.apple.com.cn/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{Host: tt.host}
			start := c.authStartURL(state)
			if !strings.HasPrefix(start, tt.want) {
				t.Fatalf("auth start URL = %q, want prefix %q", start, tt.want)
			}
			if !strings.Contains(start, "redirect_uri=https%3A%2F%2Fwww."+tt.host) {
				t.Fatalf("auth start URL does not use the account region: %q", start)
			}
			if got := c.authWebURL(); !strings.HasPrefix(got, "https://setup."+tt.host+"/") {
				t.Fatalf("auth web URL = %q", got)
			}
		})
	}
}

func TestUpdateAuthHeadersIncludesAppleAuthContext(t *testing.T) {
	c := &Client{Host: "icloud.com.cn"}
	state := &authState{
		frameId:  "frame-1",
		clientId: OAuthClientID,
		authAttr: "auth-attributes",
	}
	got := c.updateAuthHeaders(http.Header{}, state)

	checks := map[string]string{
		"Origin":                           "https://idmsa.apple.com.cn",
		"Referer":                          "https://idmsa.apple.com.cn/",
		"X-Apple-Auth-Attributes":          "auth-attributes",
		"X-Apple-Widget-Key":               OAuthClientID,
		"X-Apple-Oauth-Client-Id":          OAuthClientID,
		"X-Apple-Oauth-Redirect-URI":       "https://www.icloud.com.cn",
		"X-Apple-Oauth-State":              "auth-frame-1",
		"X-Apple-Frame-Id":                 "auth-frame-1",
		"X-Apple-Oauth-Require-Grant-Code": "true",
	}
	for key, want := range checks {
		if got.Get(key) != want {
			t.Errorf("%s = %q, want %q", key, got.Get(key), want)
		}
	}
}
