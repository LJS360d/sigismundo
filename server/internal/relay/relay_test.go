package relay

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// The stats must reach the TCP socket under TLS, where the WebSocket's own
// NetConn is the TLS wrapper.
func TestTCPInfoUnderTLS(t *testing.T) {
	up := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err == nil {
			c.ReadMessage()
		}
	}))
	defer srv.Close()
	var tcp *net.TCPConn
	d := dialer(&tcp)
	d.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	conn, _, err := d.Dial("wss"+strings.TrimPrefix(srv.URL, "https"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, ok := conn.NetConn().(*net.TCPConn); ok {
		t.Fatal("expected a TLS conn")
	}
	if _, ok := tcpInfo(tcp); !ok {
		t.Fatal("no tcp info")
	}
}
