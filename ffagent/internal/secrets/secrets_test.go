package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const plain = "AEBsuperSecretCookieValue%2Bxyz"

func TestSecretNeverFormats(t *testing.T) {
	s := New(plain)
	holder := struct {
		Name   string
		Cookie Secret
		Ptr    *Secret
	}{"x", s, &s}
	var outs []string
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"} {
		outs = append(outs, fmt.Sprintf(verb, s), fmt.Sprintf(verb, holder))
	}
	outs = append(outs, fmt.Sprint(s), fmt.Sprintln(holder), s.String())
	j, _ := json.Marshal(holder)
	outs = append(outs, string(j))
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("x", "cookie", s, "holder", holder)
	outs = append(outs, buf.String())
	for _, o := range outs {
		if strings.Contains(o, plain) || strings.Contains(o, fmt.Sprintf("%x", plain)) {
			t.Fatalf("secret leaked: %s", o)
		}
	}
	if s.Reveal() != plain {
		t.Fatal("Reveal should return the value")
	}
}

func TestVaultRoundTripAndNoPlaintextOnDisk(t *testing.T) {
	pass := []byte("correct horse battery staple")
	data, err := Seal(pass, map[string]Secret{"espn_s2": New(plain), "swid": New("{ABC}")})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(plain)) || bytes.Contains(data, []byte("ABC")) {
		t.Fatal("vault contains plaintext")
	}
	path := filepath.Join(t.TempDir(), "sub", "secrets.vault")
	if err := WriteVault(path, data); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("vault mode %o, want 600", fi.Mode().Perm())
	}
	vals, err := ReadVault(path, pass)
	if err != nil {
		t.Fatal(err)
	}
	if vals["espn_s2"].Reveal() != plain || vals["swid"].Reveal() != "{ABC}" {
		t.Errorf("round trip mismatch")
	}
	// Fresh salt and nonce each time.
	data2, _ := Seal(pass, map[string]Secret{"espn_s2": New(plain)})
	if bytes.Equal(data, data2) {
		t.Error("two seals produced identical output")
	}
}

func TestVaultRejectsWrongPassphraseAndTampering(t *testing.T) {
	pass := []byte("correct horse battery staple")
	data, _ := Seal(pass, map[string]Secret{"espn_s2": New(plain)})
	if _, err := Open([]byte("wrong passphrase!!"), data); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong passphrase: %v", err)
	}

	var v vaultFile
	_ = json.Unmarshal(data, &v)
	v.Ciphertext[0] ^= 1
	bad, _ := json.Marshal(v)
	if _, err := Open(pass, bad); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered ciphertext: %v", err)
	}

	_ = json.Unmarshal(data, &v)
	v.Time++ // header is authenticated
	bad, _ = json.Marshal(v)
	if _, err := Open(pass, bad); !errors.Is(err, ErrDecrypt) {
		t.Errorf("tampered header: %v", err)
	}

	_ = json.Unmarshal(data, &v)
	v.MemoryKB = 8 // downgrade attempt
	bad, _ = json.Marshal(v)
	if _, err := Open(pass, bad); err == nil {
		t.Error("weakened KDF parameters accepted")
	}
}

func TestPassphraseSources(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "pass")
	os.WriteFile(f, []byte("from-file-passphrase\n"), 0o644)
	t.Setenv("FFAGENT_PASSPHRASE_FILE", f)
	if _, err := Passphrase(); err == nil {
		t.Error("world-readable passphrase file accepted")
	}
	os.Chmod(f, 0o600)
	if p, err := Passphrase(); err != nil || string(p) != "from-file-passphrase" {
		t.Errorf("passphrase file: %q %v", p, err)
	}

	t.Setenv("FFAGENT_PASSPHRASE_FILE", "")
	os.Unsetenv("FFAGENT_PASSPHRASE_FILE")
	t.Setenv("FFAGENT_PASSPHRASE", "from-env-passphrase")
	if p, err := Passphrase(); err != nil || string(p) != "from-env-passphrase" {
		t.Errorf("env passphrase: %q %v", p, err)
	}
	if _, ok := os.LookupEnv("FFAGENT_PASSPHRASE"); ok {
		t.Error("FFAGENT_PASSPHRASE should be removed from the environment after reading")
	}

	cred := filepath.Join(dir, "creds")
	os.Mkdir(cred, 0o700)
	os.WriteFile(filepath.Join(cred, "ffagent-passphrase"), []byte("systemd-cred"), 0o400)
	t.Setenv("CREDENTIALS_DIRECTORY", cred)
	if p, err := Passphrase(); err != nil || string(p) != "systemd-cred" {
		t.Errorf("systemd credential: %q %v", p, err)
	}
}
