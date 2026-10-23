package middleware

import (
	"fmt"
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/rs/zerolog/log"
)

// Recover turns a panic in a handler into a 500 for that request and logs it
// with its stack. Fiber doesn't recover by default, and the web servers run in
// the broker's process, so an unrecovered panic would take AMQP down too.
func Recover() fiber.Handler {
	return recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e any) {
			log.Error().Str("method", c.Method()).Str("path", c.Path()).
				Str("panic", fmt.Sprint(e)).Str("stack", string(debug.Stack())).
				Msg("Recovered panic in web handler")
		},
	})
}
