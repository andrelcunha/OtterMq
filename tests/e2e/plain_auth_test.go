package e2e

import (
	"testing"

	"github.com/ottermq/ottermq/internal/persistdb"
	amqp "github.com/rabbitmq/amqp091-go"
	"golang.org/x/crypto/bcrypt"
)

// A user whose password contains spaces can log in over AMQP with PLAIN
// (#326). The user is stored as the definitions import stores users, with
// the minimum bcrypt cost: under -race the production cost of AddUser takes
// longer than the handshake timeout to verify.
func TestPlainAuth_PasswordWithSpaces(t *testing.T) {
	if testBroker == nil {
		t.Skip("using an external broker; the test user can't be created in its database")
	}
	const username = "e2e-spaced-password"
	const password = "correct horse battery staple"

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("Failed to hash password: %v", err)
	}
	if err := persistdb.AddUserWithHash(username, string(hash), 1); err != nil {
		t.Fatalf("Failed to add user: %v", err)
	}
	if err := persistdb.GrantVHostAccess(username, "/"); err != nil {
		t.Fatalf("Failed to grant vhost access: %v", err)
	}

	conn, err := amqp.DialConfig("amqp://localhost:5672/", amqp.Config{
		SASL: []amqp.Authentication{&amqp.PlainAuth{Username: username, Password: password}},
	})
	if err != nil {
		t.Fatalf("Failed to connect as a user whose password contains spaces: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("Failed to open channel: %v", err)
	}
	defer ch.Close()
}
