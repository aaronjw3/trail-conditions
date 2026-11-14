package httpui_test

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aaronjw3/trail-conditions/internal/adapters/httpui"
)

func TestMapConfigExposesOnlyPublicTokens(t *testing.T) {
	for _, token := range []string{"", "sk.test-secret", "pk.test-public"} {
		ui, err := httpui.New(nil, os.DirFS("../../.."), token)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		ui.Routes().ServeHTTP(recorder, httptest.NewRequest("GET", "/api/map-config", nil))
		if token == "pk.test-public" {
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), token) {
				t.Fatal("public token unavailable")
			}
		} else if recorder.Code != 503 || strings.Contains(recorder.Body.String(), "sk.test-secret") {
			t.Fatal("invalid token exposed")
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("config cached")
		}
	}
}
