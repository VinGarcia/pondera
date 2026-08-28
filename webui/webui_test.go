package webui_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sylgarcia00/pondera/webui"
)

func TestHandlerServesTheSPAAndRuntime(t *testing.T) {
	srv := httptest.NewServer(webui.Handler())
	defer srv.Close()

	tests := []struct {
		desc        string
		path        string
		wantType    string
		wantContent string
	}{
		{"root serves the SPA shell", "/", "text/html", "<title>pondera"},
		{"runtime is served same-origin", "/vue.global.prod.js", "text/javascript", "Vue"},
	}

	for _, test := range tests {
		t.Run(test.desc, func(t *testing.T) {
			resp, err := http.Get(srv.URL + test.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				t.Fatalf("got status %d, want 200", resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, test.wantType) {
				t.Fatalf("got content-type %q, want to contain %q", ct, test.wantType)
			}
		})
	}
}

func TestExportedAssetsAreEmbedded(t *testing.T) {
	if len(webui.Index) == 0 {
		t.Fatal("webui.Index is empty; the SPA shell did not embed")
	}
	if len(webui.Runtime) == 0 {
		t.Fatal("webui.Runtime is empty; the Vue runtime did not embed")
	}
	// The SPA must carry the auth-aware fetch wrapper so a cluster host can attach
	// a JWT; losing it would silently break authenticated calls.
	if !strings.Contains(string(webui.Index), "apiFetch") {
		t.Fatal("SPA shell is missing the apiFetch auth wrapper")
	}
}
