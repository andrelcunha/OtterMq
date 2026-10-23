package amqp

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The PLAIN response holds the password, so it must not reach the log, even
// at debug level.
func TestProcessStartOkContent_DoesNotLogPassword(t *testing.T) {
	const password = "s3cret-pa55word"
	setupLoopbackTestDB(t)
	var buf bytes.Buffer
	logger, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = logger
		zerolog.SetGlobalLevel(level)
	})

	// alice's password is "pw", so authentication fails; the response has
	// been parsed (and was logged) by then.
	_, _ = processStartOkContent(&ConnectionStartOk{Mechanism: "PLAIN", Response: " alice " + password})

	if !strings.Contains(buf.String(), "alice") {
		t.Fatalf("expected the handshake to log at debug level, got %q", buf.String())
	}
	if strings.Contains(buf.String(), password) {
		t.Errorf("the password was logged: %q", buf.String())
	}
}
