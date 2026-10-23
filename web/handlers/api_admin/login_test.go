package api_admin

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/ottermq/ottermq/internal/persistdb"
	"golang.org/x/crypto/bcrypt"
)

const loginTestKey = "0123456789abcdef0123456789abcdef"

// setupLoginTestDB creates users guest and alice with password "pw".
func setupLoginTestDB(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "ottermq-login-test-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { persistdb.CloseDB(); os.RemoveAll(dir) })
	persistdb.SetDbPath(filepath.Join(dir, "test.db"))
	if err := persistdb.OpenDB(); err != nil {
		t.Fatalf("open db: %v", err)
	}
	persistdb.InitDB()
	persistdb.AddDefaultRoles()
	persistdb.AddDefaultPermissions()
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	for _, user := range []string{"guest", "alice"} {
		if err := persistdb.AddUserWithHash(user, string(hash), 1); err != nil {
			t.Fatalf("add user %s: %v", user, err)
		}
	}
}

func newLoginApp() *fiber.App {
	app := fiber.New()
	app.Post("/api/login", Login(loginTestKey, []string{"guest"}))
	return app
}

func loginBody(user string) string {
	return `{"username":"` + user + `","password":"pw"}`
}

// A user in the loopback list can log in from a loopback address only; the
// X-Forwarded-For header doesn't count. Other users log in from anywhere.
func TestLogin_LoopbackOnlyUsers(t *testing.T) {
	setupLoginTestDB(t)
	app := newLoginApp()

	// app.Test serves the request over a connection whose peer is 0.0.0.0,
	// so it stands for a remote client.
	for _, tt := range []struct {
		name       string
		user       string
		forwarded  string
		wantStatus int
	}{
		{"guest from a remote address", "guest", "", http.StatusUnauthorized},
		{"guest claiming localhost in X-Forwarded-For", "guest", "127.0.0.1", http.StatusUnauthorized},
		{"other user from a remote address", "alice", "", http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(loginBody(tt.user)))
			req.Header.Set("Content-Type", "application/json")
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("status %d (%s), want %d", resp.StatusCode, body, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized && !strings.Contains(string(body), "only log in from localhost") {
				t.Errorf("body %s doesn't say why the login was refused", body)
			}
		})
	}

	t.Run("guest from localhost", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(ln) }()
		t.Cleanup(func() { _ = app.Shutdown() })

		resp, err := http.Post("http://"+ln.Addr().String()+"/api/login", "application/json", strings.NewReader(loginBody("guest")))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != http.StatusOK || got.Token == "" {
			t.Fatalf("status %d, token %q; want 200 with a token", resp.StatusCode, got.Token)
		}
	})
}
