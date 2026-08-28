package secure

import (
	"bytes"
	"testing"
)

func TestSealRoundTrip(t *testing.T) {
	b, e := New(bytes.Repeat([]byte{7}, 32))
	if e != nil {
		t.Fatal(e)
	}
	x, e := b.Seal("top-secret")
	if e != nil {
		t.Fatal(e)
	}
	if x == "top-secret" {
		t.Fatal("plaintext leaked")
	}
	got, e := b.Open(x)
	if e != nil || got != "top-secret" {
		t.Fatalf("got %q, %v", got, e)
	}
}
func TestCiphertextIsRandomized(t *testing.T) {
	b, _ := New(bytes.Repeat([]byte{1}, 32))
	a, _ := b.Seal("same")
	c, _ := b.Seal("same")
	if a == c {
		t.Fatal("nonce was reused")
	}
}
