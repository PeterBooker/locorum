package traefik

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestGenerateAdminCredentials(t *testing.T) {
	password, hash, err := generateAdminCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if password == "" {
		t.Fatal("password is empty")
	}
	if hash == "" {
		t.Fatal("hash is empty")
	}
	if len(password) < 32 {
		t.Errorf("password too short: %d chars (want >=32)", len(password))
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		t.Errorf("hash does not verify password: %v", err)
	}
}

func TestGenerateAdminCredentials_Unique(t *testing.T) {
	a, _, err := generateAdminCredentials()
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := generateAdminCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two invocations produced identical passwords — randomness is broken")
	}
}
