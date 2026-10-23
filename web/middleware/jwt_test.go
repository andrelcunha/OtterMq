package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/ottermq/ottermq/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret"

func makeTokenWithRole(t *testing.T, role string) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":      "ottermq",
		"sub":      1,
		"aud":      "ottermq",
		"exp":      jwt.NewNumericDate(time.Now().Add(time.Hour)),
		"username": "testuser",
		"role":     role,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)
	return signed
}

func newTestApp() *fiber.App {
	app := fiber.New()
	app.Use(JwtMiddleware(testSecret))
	app.Use(AdminOnly)
	app.Get("/", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func doRequest(app *fiber.App, token string) *http.Response {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, _ := app.Test(req, -1)
	return resp
}

func TestAdminOnly_AdminRole_Allowed(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, makeTokenWithRole(t, "admin"))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestAdminOnly_UserRole_Forbidden(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, makeTokenWithRole(t, "user"))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestAdminOnly_GuestRole_Forbidden(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, makeTokenWithRole(t, "guest"))
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestAdminOnly_NoToken_Unauthorized(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, "")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAdminOnly_InvalidToken_Unauthorized(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, "not-a-valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAdminOnly_ExpiredToken_Unauthorized(t *testing.T) {
	claims := jwt.MapClaims{
		"iss":      "ottermq",
		"sub":      1,
		"aud":      "ottermq",
		"exp":      jwt.NewNumericDate(time.Now().Add(-time.Hour)),
		"username": "testuser",
		"role":     "admin",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)

	app := newTestApp()
	resp := doRequest(app, signed)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestAdminOnly_ResponseBody_Forbidden(t *testing.T) {
	app := newTestApp()
	resp := doRequest(app, makeTokenWithRole(t, "user"))
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Admin access required")
}

// On a fresh install the signing key is generated, so a token forged with
// the old default secret "secret" is rejected.
func TestJwtMiddleware_RejectsOldDefaultSecretOnFreshInstall(t *testing.T) {
	key, _, err := config.ResolveJwtSecret(&config.Config{DataDir: t.TempDir()})
	require.NoError(t, err)

	app := fiber.New()
	app.Use(JwtMiddleware(key))
	app.Get("/", func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

	sign := func(secret string) string {
		claims := jwt.MapClaims{
			"iss": "ottermq", "sub": 1, "aud": "ottermq",
			"exp":      jwt.NewNumericDate(time.Now().Add(time.Hour)),
			"username": "guest", "role": "admin",
		}
		signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		require.NoError(t, err)
		return signed
	}
	for secret, want := range map[string]int{"secret": fiber.StatusUnauthorized, key: fiber.StatusOK} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+sign(secret))
		resp, err := app.Test(req)
		require.NoError(t, err)
		assert.Equal(t, want, resp.StatusCode, "token signed with %q", secret)
	}
}
