package api_admin

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestDecodedParam(t *testing.T) {
	app := fiber.New()
	app.Get("/permissions/:vhost/:username", func(c *fiber.Ctx) error {
		return c.SendString(decodedParam(c, "vhost") + "|" + decodedParam(c, "username"))
	})

	tests := []struct {
		path string
		want string
	}{
		{"/permissions/%2F/guest", "/|guest"},
		{"/permissions/my%20vhost/j%C3%BCrgen", "my vhost|jürgen"},
		{"/permissions/plain/bob", "plain|bob"},
	}
	for _, tt := range tests {
		resp, err := app.Test(httptest.NewRequest("GET", tt.path, nil))
		if err != nil {
			t.Fatalf("%s: %v", tt.path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		if string(body) != tt.want {
			t.Errorf("%s: got %q, want %q", tt.path, body, tt.want)
		}
	}
}
