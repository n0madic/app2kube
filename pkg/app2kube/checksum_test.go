package app2kube

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// dataChecksum must be deterministic (order-independent) and change whenever the
// data changes — it backs the checksum/* pod annotations that roll a workload on
// config/secret change (#22).
func TestDataChecksum(t *testing.T) {
	// Order-independent: same data, different insertion order → same hash.
	a := dataChecksum(map[string][]byte{"B": []byte("2"), "A": []byte("1")})
	b := dataChecksum(map[string][]byte{"A": []byte("1"), "B": []byte("2")})
	if a != b {
		t.Fatalf("checksum must be order-independent: %s vs %s", a, b)
	}

	// A value change must change the checksum.
	if dataChecksum(map[string][]byte{"A": []byte("1")}) ==
		dataChecksum(map[string][]byte{"A": []byte("2")}) {
		t.Errorf("a value change must change the checksum")
	}

	// Empty data still hashes to a stable, non-empty hex string.
	if got := dataChecksum(nil); got != fmt.Sprintf("%x", sha256.Sum256(nil)) {
		t.Errorf("empty data checksum: got %s", got)
	}
}

// Regression (#10): the length-prefixed encoding must be injective — maps that
// differ only in where a '=' or newline falls must hash differently, where the
// previous unescaped "key=value\n" form collided.
func TestDataChecksumNoCanonicalizationCollision(t *testing.T) {
	one := dataChecksum(map[string][]byte{"A": []byte("1\nB=2")})
	two := dataChecksum(map[string][]byte{"A": []byte("1"), "B": []byte("2")})
	if one == two {
		t.Errorf("checksum must not collide on '='/newline reshaping: %s", one)
	}
}
