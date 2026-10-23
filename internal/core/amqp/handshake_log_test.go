package amqp

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
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
	_, _ = processStartOkContent(&ConnectionStartOk{Mechanism: "PLAIN", Response: "\x00alice\x00" + password})

	if !strings.Contains(buf.String(), "alice") {
		t.Fatalf("expected the handshake to log at debug level, got %q", buf.String())
	}
	if strings.Contains(buf.String(), password) {
		t.Errorf("the password was logged: %q", buf.String())
	}
}

// Decoding a connection.start-ok must not log its PLAIN response either.
func TestParseStartOk_DoesNotLogPassword(t *testing.T) {
	const password = "s3cret-pa55word"
	var buf bytes.Buffer
	logger, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = logger
		zerolog.SetGlobalLevel(level)
	})

	if _, err := parseMethodFrame(0, startOkPayload(t, "\x00alice\x00"+password)); err != nil {
		t.Fatalf("parseMethodFrame: %v", err)
	}

	if buf.Len() == 0 {
		t.Fatal("expected start-ok decoding to log at trace level, got nothing")
	}
	if strings.Contains(buf.String(), password) {
		t.Errorf("the password was logged: %q", buf.String())
	}
}

// The handshake doesn't log the start-ok frame it reads, in raw or hex form.
func TestHandshake_DoesNotLogPassword(t *testing.T) {
	const password = "s3cret-pa55word"
	addPlainTestUser(t, "loguser", password)
	cfg := HandshakeConfig{
		Protocol:   "AMQP 0-9-1",
		Mechanisms: []string{"PLAIN"},
		Locales:    []string{"en_US"},
		Tuning:     NewServerTuning(0, 0, 0),
	}
	header, err := buildProtocolHeader(cfg.Protocol)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger, level := log.Logger, zerolog.GlobalLevel()
	log.Logger = zerolog.New(&buf)
	zerolog.SetGlobalLevel(zerolog.TraceLevel)
	t.Cleanup(func() {
		log.Logger = logger
		zerolog.SetGlobalLevel(level)
	})

	server, client := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = handshake(cfg, server, context.Background())
		server.Close()
	}()
	if _, err := client.Write(header); err != nil {
		t.Fatalf("write protocol header: %v", err)
	}
	if _, err := readFrame(client, cfg.Tuning.FrameMax); err != nil {
		t.Fatalf("read connection.start: %v", err)
	}
	if _, err := client.Write(startOkFrame("loguser", password)); err != nil {
		t.Fatalf("write connection.start-ok: %v", err)
	}
	if _, err := readFrame(client, cfg.Tuning.FrameMax); err != nil {
		t.Fatalf("read connection.tune: %v", err)
	}
	client.Close() // ends the handshake before tune-ok
	_, _ = io.Copy(io.Discard, server)
	<-done

	if !strings.Contains(buf.String(), "loguser") {
		t.Fatalf("expected the handshake to log the username at debug level, got %q", buf.String())
	}
	for _, form := range []string{password, hex.EncodeToString([]byte(password))} {
		if strings.Contains(buf.String(), form) {
			t.Errorf("the password was logged: %q", buf.String())
		}
	}
}
