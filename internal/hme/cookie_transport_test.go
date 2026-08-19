package hme

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestRequestDoesNotSendDuplicateCookies(t *testing.T) {
	seen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer server.Close()

	client, err := NewClient(map[string]string{
		"session": `"quoted-value"`,
		"plain":   "plain-value",
	}, "icloud.com", "", false)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.httpc.SetCookies(u, []*fhttp.Cookie{
		{Name: "session", Value: `"quoted-value"`, Path: "/"},
		{Name: "plain", Value: "plain-value", Path: "/"},
	})

	if _, err := client.request("GET", server.URL, nil, 0, 1); err != nil {
		t.Fatal(err)
	}
	header := <-seen
	for _, name := range []string{"session=", "plain="} {
		if got := strings.Count(header, name); got != 1 {
			t.Fatalf("Cookie %q sent %d times in %q", name, got, header)
		}
	}
}
