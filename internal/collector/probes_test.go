package collector

import (
	"context"
	"encoding/binary"
	"golang.org/x/net/dns/dnsmessage"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExplicitAddressAndFamily(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	c := Check{Kind: "http", URL: "http://test-target.invalid:" + port, ConnectIP: "127.0.0.1", Network: "tcp4", ExpectedStatus: 204}
	if got := Probe(context.Background(), c); got.Status != "healthy" {
		t.Fatal(got)
	}
	c.Network = "tcp6"
	if got := Probe(context.Background(), c); got.Status != "down" {
		t.Fatal("IPv4 silently satisfied IPv6 probe")
	}
}
func TestDNSRequiresWellFormedRequestedAnswer(t *testing.T) {
	for _, valid := range []bool{true, false} {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			buf := make([]byte, 512)
			n, addr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			var q dnsmessage.Message
			if q.Unpack(buf[:n]) != nil {
				return
			}
			q.Header.Response = true
			q.Header.RCode = dnsmessage.RCodeSuccess
			q.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
			reply, _ := q.Pack()
			if !valid {
				reply = append([]byte(nil), buf[:n]...)
				binary.BigEndian.PutUint16(reply[2:4], 0x8180)
				binary.BigEndian.PutUint16(reply[6:8], 1)
			}
			_, _ = conn.WriteTo(reply, addr)
		}()
		got := Probe(context.Background(), Check{Kind: "dns", DNSServer: conn.LocalAddr().String(), Query: "example.com"})
		conn.Close()
		want := "healthy"
		if !valid {
			want = "down"
		}
		if got.Status != want {
			t.Fatalf("valid=%v got %v", valid, got)
		}
	}
}
