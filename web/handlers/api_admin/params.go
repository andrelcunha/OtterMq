package api_admin

import (
	"net/url"

	"github.com/gofiber/fiber/v2"
)

// decodedParam returns a URL-decoded path parameter. Fiber leaves path parameters
// escaped, so a vhost "/" arrives as "%2F"; without decoding, permissions were
// stored for a vhost literally named "%2F".
func decodedParam(c *fiber.Ctx, key string) string {
	val := c.Params(key)
	if decoded, err := url.PathUnescape(val); err == nil {
		return decoded
	}
	return val
}
