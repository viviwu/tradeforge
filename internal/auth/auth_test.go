package auth

import (
	"testing"
	"time"

	"github.com/viviwu/tradeforge/internal/domain"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("s3cret", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "s3cret") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password verified")
	}
}

func TestIssueAndParseAccess(t *testing.T) {
	manager := NewTokenManager("test-secret", time.Minute)
	user := domain.User{ID: "u1", Username: "alice", Role: "viewer"}
	token, expires, err := manager.IssueAccess(user)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(expires) <= 0 {
		t.Fatal("token already expired")
	}
	claims, err := manager.ParseAccess(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.UserID != user.ID || claims.Username != user.Username || claims.Role != user.Role {
		t.Fatalf("unexpected claims: %+v", claims)
	}
	if _, err := NewTokenManager("other-secret", time.Minute).ParseAccess(token); err == nil {
		t.Fatal("token accepted with wrong secret")
	}
}

func TestRefreshTokenHash(t *testing.T) {
	raw, hash, err := GenerateRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || hash == "" {
		t.Fatal("empty token")
	}
	if HashRefreshToken(raw) != hash {
		t.Fatal("hash mismatch")
	}
	if HashRefreshToken(raw+"x") == hash {
		t.Fatal("hash collision")
	}
}
