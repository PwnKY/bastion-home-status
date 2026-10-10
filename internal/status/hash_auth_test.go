package status

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDigestOnlyServerDoesNotNeedCollectorSecret(t *testing.T) {
	original, _, token, now := fixture(t)
	c := original.Config
	c.Agents = append([]Agent(nil), c.Agents...)
	digest := sha256.Sum256([]byte(token))
	value := hex.EncodeToString(digest[:])
	file := filepath.Join(t.TempDir(), "digest")
	_ = os.WriteFile(file, []byte(value), 0600)
	c.Agents[0].SecretFile = ""
	c.Agents[0].SecretHashFile = file
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := NewHandler(s)
	h.Now = func() time.Time { return now }
	if w := request(h, "POST", "/api/v1/heartbeat", token, Heartbeat{ID: "valid"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := request(h, "POST", "/api/v1/heartbeat", value, Heartbeat{ID: "digest-is-not-token"}); w.Code != 401 {
		t.Fatal("digest usable as bearer")
	}
}
