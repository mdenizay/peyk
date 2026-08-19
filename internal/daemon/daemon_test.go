package daemon

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "topsecret"

	if !validSignature(secret, sign(secret, body), body) {
		t.Error("valid signature rejected")
	}
	if validSignature(secret, sign("wrong", body), body) {
		t.Error("wrong-secret signature accepted")
	}
	if validSignature(secret, "sha256=deadbeef", body) {
		t.Error("garbage signature accepted")
	}
	if validSignature(secret, "", body) {
		t.Error("empty signature accepted")
	}
	if validSignature("", sign("", body), body) {
		t.Error("empty secret accepted — projects must always have a secret")
	}
}
