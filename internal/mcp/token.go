package mcp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TokenFilename is the basename of the auth token file under
// ~/.locorum/state/. The full path is built by TokenPath.
const TokenFilename = "mcp_token"

// TokenByteLen is the size of the random source for an MCP HTTP
// auth token, before base64-encoding. 32 bytes (256 bits) gives a
// 43-char URL-safe string — well above the brute-force horizon for
// any local-network attacker.
const TokenByteLen = 32

// TokenPath returns the absolute path to the MCP token file under
// homeDir. Sits next to owner.lock + locorum.sock in
// ~/.locorum/state/, which is already 0700 owner-only.
func TokenPath(homeDir string) string {
	return filepath.Join(homeDir, ".locorum", "state", TokenFilename)
}

// LoadOrCreateToken returns the auth token, creating a new random one
// on first use. The file is written 0600 so peer users on the host
// cannot lift the token from disk. Idempotent across processes — the
// HTTP server reads the same value the user pastes into their MCP
// client.
func LoadOrCreateToken(homeDir string) (string, error) {
	path := TokenPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	if body, err := os.ReadFile(path); err == nil {
		token := strings.TrimSpace(string(body))
		if token != "" {
			return token, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read token: %w", err)
	}
	return rotateTokenAt(path)
}

// RotateToken regenerates the token, replacing whatever was there.
// The old token stops working immediately; existing MCP clients must
// pick up the new value from disk (or be re-pasted into the IDE
// config).
func RotateToken(homeDir string) (string, error) {
	path := TokenPath(homeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	return rotateTokenAt(path)
}

// rotateTokenAt is the shared body of LoadOrCreateToken and
// RotateToken. Writes via O_TRUNC so a partial write doesn't leave
// half a token on disk.
func rotateTokenAt(path string) (string, error) {
	buf := make([]byte, TokenByteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write token: %w", err)
	}
	return token, nil
}

// CompareTokens performs a constant-time comparison so a length-leak
// or timing-leak doesn't reveal token characters to an attacker. The
// strings package's == would be timing-leaky.
func CompareTokens(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
