package hub

import (
	"testing"
	"time"
)

func testServer() *Server {
	return &Server{cfg: Config{Secret: []byte("test-secret"), Location: time.UTC}}
}

func TestWSTicketRoundTrip(t *testing.T) {
	s := testServer()
	now := time.Unix(1_800_000_000, 0)
	uid, err := s.verifyWSTicket(s.issueWSTicket(7, 42, now), 7, now.Add(30*time.Second))
	if err != nil || uid != 42 {
		t.Fatalf("got uid=%d err=%v", uid, err)
	}
}

func TestWSTicketRejectsOtherCompany(t *testing.T) {
	s := testServer()
	now := time.Unix(1_800_000_000, 0)
	if _, err := s.verifyWSTicket(s.issueWSTicket(7, 42, now), 8, now); err == nil {
		t.Fatal("ticket for company 7 accepted for company 8")
	}
}

func TestWSTicketExpires(t *testing.T) {
	s := testServer()
	now := time.Unix(1_800_000_000, 0)
	if _, err := s.verifyWSTicket(s.issueWSTicket(7, 42, now), 7, now.Add(wsTicketTTL+time.Second)); err == nil {
		t.Fatal("expired ticket accepted")
	}
}

func TestWSTicketRejectsTampering(t *testing.T) {
	s := testServer()
	now := time.Unix(1_800_000_000, 0)
	ticket := s.issueWSTicket(7, 42, now)
	forged := "7.99" + ticket[len("7.42"):]
	for _, tk := range []string{forged, "", "7.42", "garbage.a.b.c", ticket + "0"} {
		if _, err := s.verifyWSTicket(tk, 7, now); err == nil {
			t.Fatalf("accepted bad ticket %q", tk)
		}
	}
}

func TestWSTicketRejectsOtherSecret(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ticket := testServer().issueWSTicket(7, 42, now)
	other := &Server{cfg: Config{Secret: []byte("different"), Location: time.UTC}}
	if _, err := other.verifyWSTicket(ticket, 7, now); err == nil {
		t.Fatal("ticket signed with another secret accepted")
	}
}
