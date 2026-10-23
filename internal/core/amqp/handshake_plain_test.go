package amqp

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/ottermq/ottermq/internal/persistdb"
	"golang.org/x/crypto/bcrypt"
)

// A PLAIN response is authzid NUL authcid NUL passwd (RFC 4616). Only the
// NULs separate the fields: spaces belong to the username or password.
func TestParsePlainResponse(t *testing.T) {
	accepted := []struct {
		name     string
		response string
		username string
		password string
	}{
		{"empty authzid", "\x00user\x00pass", "user", "pass"},
		{"authzid equal to username", "user\x00user\x00pass", "user", "pass"},
		{"password with spaces", "\x00user\x00pa ss word ", "user", "pa ss word "},
		{"username with spaces", "\x00first last\x00pass", "first last", "pass"},
		{"authzid and username with spaces", "first last\x00first last\x00pass", "first last", "pass"},
	}
	for _, tt := range accepted {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			username, password, err := parsePlainResponse(tt.response)
			if err != nil {
				t.Fatalf("parsePlainResponse returned error %v, want none", err)
			}
			if username != tt.username || password != tt.password {
				t.Errorf("parsePlainResponse = (%q, %q), want (%q, %q)", username, password, tt.username, tt.password)
			}
		})
	}

	rejected := []struct {
		name     string
		response string
	}{
		{"different authzid", "admin\x00user\x00pass"},
		{"authzid differing only in case", "User\x00user\x00pass"},
		{"empty response", ""},
		{"no NUL", "user pass"},
		{"space-separated credentials", " user pass"},
		{"one NUL", "\x00user"},
		{"missing authzid separator", "user\x00pass"},
		{"extra NUL", "\x00user\x00pass\x00"},
		{"extra field", "\x00user\x00pass\x00more"},
		{"NUL in password", "\x00user\x00pa\x00ss"},
		{"empty username", "\x00\x00pass"},
		{"empty username with authzid", "user\x00\x00pass"},
		{"empty password", "\x00user\x00"},
		{"only NULs", "\x00\x00"},
	}
	for _, tt := range rejected {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			username, password, err := parsePlainResponse(tt.response)
			if err == nil {
				t.Fatalf("parsePlainResponse = (%q, %q), want an error", username, password)
			}
			if username != "" || password != "" {
				t.Errorf("parsePlainResponse returned credentials alongside error %v", err)
			}
		})
	}
}

// addPlainTestUser creates username with password in the test database
// unless it already exists.
func addPlainTestUser(t *testing.T, username, password string) {
	t.Helper()
	setupLoopbackTestDB(t)
	if _, err := persistdb.GetUserByUsername(username); err == nil {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := persistdb.AddUserWithHash(username, string(hash), 1); err != nil {
		t.Fatalf("add user %s: %v", username, err)
	}
}

// startOkPayload encodes a connection.start-ok method payload carrying
// response as its PLAIN response.
func startOkPayload(response string) []byte {
	var method bytes.Buffer
	EncodeLongStr(&method, EncodeTable(map[string]any{"product": "test"}))
	EncodeShortStr(&method, "PLAIN")
	EncodeLongStr(&method, []byte(response))
	EncodeShortStr(&method, "en_US")

	payload := binary.BigEndian.AppendUint16(nil, uint16(CONNECTION))
	payload = binary.BigEndian.AppendUint16(payload, uint16(CONNECTION_START_OK))
	return append(payload, method.Bytes()...)
}

// A start-ok decoded off the wire authenticates users whose username or
// password contains spaces, and clients that send an authzid equal to the
// username.
func TestStartOk_PlainCredentialsAuthenticate(t *testing.T) {
	addPlainTestUser(t, "plain spaced", "pw with spaces")
	addPlainTestUser(t, "plainuser", "two words")

	tests := []struct {
		name     string
		response string
		username string
	}{
		{"password with spaces", "\x00plainuser\x00two words", "plainuser"},
		{"username and password with spaces", "\x00plain spaced\x00pw with spaces", "plain spaced"},
		{"authzid equal to username", "alice\x00alice\x00pw", "alice"},
		{"empty authzid", "\x00alice\x00pw", "alice"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := parseMethodFrame(0, startOkPayload(tt.response))
			if err != nil {
				t.Fatalf("parseMethodFrame: %v", err)
			}
			startOk, ok := state.MethodFrame.Content.(*ConnectionStartOk)
			if !ok {
				t.Fatalf("content is %T, want *ConnectionStartOk", state.MethodFrame.Content)
			}
			username, err := processStartOkContent(startOk)
			if err != nil {
				t.Fatalf("processStartOkContent returned error %v, want none", err)
			}
			if username != tt.username {
				t.Errorf("processStartOkContent = %q, want %q", username, tt.username)
			}
		})
	}
}

// A start-ok whose response is not a valid PLAIN response, or names another
// authorization identity, fails authentication.
func TestStartOk_PlainCredentialsRejected(t *testing.T) {
	setupLoopbackTestDB(t)

	tests := []struct {
		name     string
		response string
	}{
		{"different authzid", "guest\x00alice\x00pw"},
		{"space-separated credentials", " alice pw"},
		{"extra field", "\x00alice\x00pw\x00x"},
		{"wrong password", "\x00alice\x00nope"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := parseMethodFrame(0, startOkPayload(tt.response))
			if err != nil {
				t.Fatalf("parseMethodFrame: %v", err)
			}
			startOk := state.MethodFrame.Content.(*ConnectionStartOk)
			if username, err := processStartOkContent(startOk); err == nil {
				t.Fatalf("processStartOkContent = %q, want an error", username)
			}
		})
	}
}
