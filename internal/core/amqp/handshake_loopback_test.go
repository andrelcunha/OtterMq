package amqp

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ottermq/ottermq/internal/persistdb"
	"golang.org/x/crypto/bcrypt"
)

var loopbackDBOnce sync.Once

// setupLoopbackTestDB creates users guest and alice, both with password
// "pw" and access to vhost "/".
func setupLoopbackTestDB(t *testing.T) {
	t.Helper()
	loopbackDBOnce.Do(func() {
		dir, err := os.MkdirTemp("", "ottermq-amqp-test-")
		if err != nil {
			t.Fatalf("temp dir: %v", err)
		}
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
			if err := persistdb.GrantVHostAccess(user, "/"); err != nil {
				t.Fatalf("grant vhost: %v", err)
			}
		}
	})
}

// remoteAddrConn reports remote as its peer, as a TCP connection from that
// address would.
type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

// startOkFrame builds a connection.start-ok with PLAIN credentials and no
// client properties.
func startOkFrame(user, password string) []byte {
	longStr := func(b []byte) []byte { return append(binary.BigEndian.AppendUint32(nil, uint32(len(b))), b...) }
	shortStr := func(s string) []byte { return append([]byte{byte(len(s))}, s...) }

	payload := binary.BigEndian.AppendUint16(nil, uint16(CONNECTION))
	payload = binary.BigEndian.AppendUint16(payload, uint16(CONNECTION_START_OK))
	payload = append(payload, longStr(nil)...) // client properties
	payload = append(payload, shortStr("PLAIN")...)
	payload = append(payload, longStr([]byte("\x00"+user+"\x00"+password))...)
	payload = append(payload, shortStr("en_US")...)
	frame := append([]byte{byte(TYPE_METHOD), 0, 0}, binary.BigEndian.AppendUint32(nil, uint32(len(payload)))...)
	frame = append(frame, payload...)
	return append(frame, FRAME_END)
}

// A user listed in LoopbackUsers is refused with connection.close 403 when
// it connects from a non-loopback address, and gets connection.tune from a
// loopback one. Users not in the list connect from anywhere.
func TestHandshake_LoopbackOnlyUsers(t *testing.T) {
	setupLoopbackTestDB(t)
	cfg := HandshakeConfig{
		Protocol:      "AMQP 0-9-1",
		Mechanisms:    []string{"PLAIN"},
		Locales:       []string{"en_US"},
		Tuning:        NewServerTuning(0, 0, 0),
		LoopbackUsers: []string{"guest"},
	}
	header, err := buildProtocolHeader(cfg.Protocol)
	if err != nil {
		t.Fatal(err)
	}

	remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 40000}
	tests := []struct {
		name       string
		user       string
		from       net.Addr
		wantMethod TypeMethod
	}{
		{"guest from a remote address", "guest", remote, CONNECTION_CLOSE},
		{"guest from 127.0.0.1", "guest", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}, CONNECTION_TUNE},
		{"guest from ::1", "guest", &net.TCPAddr{IP: net.IPv6loopback, Port: 40000}, CONNECTION_TUNE},
		{"other user from a remote address", "alice", remote, CONNECTION_TUNE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer client.Close()
			done := make(chan error, 1)
			go func() {
				_, err := handshake(cfg, remoteAddrConn{Conn: server, remote: tt.from}, context.Background())
				server.Close()
				done <- err
			}()

			if _, err := client.Write(header); err != nil {
				t.Fatalf("write protocol header: %v", err)
			}
			if _, err := readFrame(client, cfg.Tuning.FrameMax); err != nil {
				t.Fatalf("read connection.start: %v", err)
			}
			if _, err := client.Write(startOkFrame(tt.user, "pw")); err != nil {
				t.Fatalf("write connection.start-ok: %v", err)
			}
			reply, err := readFrame(client, cfg.Tuning.FrameMax)
			if err != nil {
				t.Fatalf("read reply to start-ok: %v", err)
			}
			payload := reply[7:]
			class, method := binary.BigEndian.Uint16(payload[0:2]), binary.BigEndian.Uint16(payload[2:4])
			if class != uint16(CONNECTION) || method != uint16(tt.wantMethod) {
				t.Fatalf("reply to start-ok is class %d method %d, want connection method %d", class, method, tt.wantMethod)
			}
			if tt.wantMethod == CONNECTION_CLOSE {
				if code := binary.BigEndian.Uint16(payload[4:6]); code != uint16(ACCESS_REFUSED) {
					t.Errorf("connection.close reply code = %d, want %d", code, ACCESS_REFUSED)
				}
				if err := <-done; err == nil {
					t.Error("handshake succeeded for a refused user")
				}
				return
			}
			client.Close()
			_, _ = io.Copy(io.Discard, client)
			<-done
		})
	}
}
