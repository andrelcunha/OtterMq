package web

import (
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSetupApp_UIFallback(t *testing.T) {
	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "index.html"), []byte("<html>ui</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(uiDir, "_app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "_app", "app.js"), []byte("console.log(1)"), 0o600); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWebServer(&Config{EnableUI: true, UIPath: uiDir, ApiPrefix: "/api", JwtKey: "test"}, nil)
	app := ws.SetupApp(nil)

	tests := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{"/", 200, "<html>ui</html>"},
		{"/queues", 200, "<html>ui</html>"},     // client-side route
		{"/_app/app.js", 200, "console.log(1)"}, // real asset still served
		{"/api/does-not-exist", 404, ""},        // API paths never get the HTML page
	}
	for _, tt := range tests {
		resp, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != tt.wantStatus {
			t.Errorf("%s: status %d, want %d", tt.path, resp.StatusCode, tt.wantStatus)
		}
		if tt.wantBody != "" && string(body) != tt.wantBody {
			t.Errorf("%s: body %q, want %q", tt.path, body, tt.wantBody)
		}
	}
}
