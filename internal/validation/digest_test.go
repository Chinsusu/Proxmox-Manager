package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityDigester_Digest_StableAndPrefixed(t *testing.T) {
	d := NewIdentityDigester([]byte("test-hmac-key"))

	got := d.Digest("0123456789abcdef0123456789abcdef")
	if !strings.HasPrefix(got, "hmac-sha256:") {
		t.Fatalf("Digest() = %q, want hmac-sha256: prefix", got)
	}
	// 64 hex chars sau tiền tố (SHA-256 = 32 byte).
	if hexPart := strings.TrimPrefix(got, "hmac-sha256:"); len(hexPart) != 64 {
		t.Errorf("hex part len = %d, want 64", len(hexPart))
	}

	again := d.Digest("0123456789abcdef0123456789abcdef")
	if got != again {
		t.Errorf("Digest() không ổn định: %q != %q", got, again)
	}
}

func TestIdentityDigester_Digest_DifferentKeyDifferentDigest(t *testing.T) {
	machineID := "0123456789abcdef0123456789abcdef"
	d1 := NewIdentityDigester([]byte("key-1"))
	d2 := NewIdentityDigester([]byte("key-2"))

	if d1.Digest(machineID) == d2.Digest(machineID) {
		t.Error("hai key khac nhau khong duoc tao cung mot digest")
	}
}

func TestIdentityDigester_Digest_DifferentMachineIDDifferentDigest(t *testing.T) {
	d := NewIdentityDigester([]byte("test-hmac-key"))
	a := d.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b := d.Digest("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if a == b {
		t.Error("hai machine-id khac nhau khong duoc tao cung mot digest")
	}
}

func TestLoadHMACKeyFromFile_TrimsWhitespaceAndNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hmac-key")
	if err := os.WriteFile(path, []byte("  super-secret-key\n"), 0o600); err != nil {
		t.Fatalf("write test key file: %v", err)
	}

	key, err := LoadHMACKeyFromFile(path)
	if err != nil {
		t.Fatalf("LoadHMACKeyFromFile() error: %v", err)
	}
	if string(key) != "super-secret-key" {
		t.Errorf("key = %q, want trimmed %q", key, "super-secret-key")
	}
}

func TestLoadHMACKeyFromFile_EmptyFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty-key")
	if err := os.WriteFile(path, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write test key file: %v", err)
	}

	if _, err := LoadHMACKeyFromFile(path); err == nil {
		t.Fatal("LoadHMACKeyFromFile() error = nil, want error for empty key file")
	}
}

func TestLoadHMACKeyFromFile_MissingFileErrors(t *testing.T) {
	if _, err := LoadHMACKeyFromFile(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("LoadHMACKeyFromFile() error = nil, want error for missing file")
	}
}
