package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (RFC 9106's second recommended option).
const (
	vaultVersion  = 1
	kdfName       = "argon2id"
	argonTime     = 3
	argonMemoryKB = 64 * 1024
	argonThreads  = 4
	keyLen        = 32 // AES-256
	saltLen       = 16
)

// ErrDecrypt means the passphrase is wrong or the vault was modified.
var ErrDecrypt = errors.New("secrets: can't decrypt vault (wrong passphrase, or the file was modified)")

// vaultFile is the on-disk format. Only the ciphertext holds secrets; the
// header is authenticated as GCM additional data so it can't be altered.
type vaultFile struct {
	Version    int    `json:"version"`
	KDF        string `json:"kdf"`
	Time       uint32 `json:"time"`
	MemoryKB   uint32 `json:"memory_kb"`
	Threads    uint8  `json:"threads"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func (v *vaultFile) aad() []byte {
	return []byte(fmt.Sprintf("ffagent-vault|v%d|%s|t%d|m%d|p%d|%x",
		v.Version, v.KDF, v.Time, v.MemoryKB, v.Threads, v.Salt))
}

func deriveKey(passphrase, salt []byte, t, m uint32, p uint8) []byte {
	return argon2.IDKey(passphrase, salt, t, m, p, keyLen)
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// Seal encrypts values under passphrase and returns the vault file bytes.
func Seal(passphrase []byte, values map[string]Secret) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, errors.New("secrets: empty passphrase")
	}
	plain := make(map[string]string, len(values))
	for k, v := range values {
		plain[k] = v.Reveal()
	}
	pt, err := json.Marshal(plain)
	if err != nil {
		return nil, err
	}
	defer wipe(pt)

	v := &vaultFile{Version: vaultVersion, KDF: kdfName, Time: argonTime, MemoryKB: argonMemoryKB, Threads: argonThreads}
	v.Salt = make([]byte, saltLen)
	if _, err := rand.Read(v.Salt); err != nil {
		return nil, err
	}
	key := deriveKey(passphrase, v.Salt, v.Time, v.MemoryKB, v.Threads)
	defer wipe(key)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	v.Nonce = make([]byte, gcm.NonceSize())
	if _, err := rand.Read(v.Nonce); err != nil {
		return nil, err
	}
	v.Ciphertext = gcm.Seal(nil, v.Nonce, pt, v.aad())
	return json.MarshalIndent(v, "", "  ")
}

// Open decrypts vault file bytes.
func Open(passphrase, data []byte) (map[string]Secret, error) {
	var v vaultFile
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("secrets: vault is not valid: %w", err)
	}
	if v.Version != vaultVersion || v.KDF != kdfName {
		return nil, fmt.Errorf("secrets: unsupported vault (version %d, kdf %q)", v.Version, v.KDF)
	}
	// Refuse weakened parameters from a tampered header.
	if v.Time < argonTime || v.MemoryKB < argonMemoryKB || v.Threads == 0 || len(v.Salt) < saltLen {
		return nil, errors.New("secrets: vault key-derivation parameters are too weak")
	}
	key := deriveKey(passphrase, v.Salt, v.Time, v.MemoryKB, v.Threads)
	defer wipe(key)
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(v.Nonce) != gcm.NonceSize() {
		return nil, ErrDecrypt
	}
	pt, err := gcm.Open(nil, v.Nonce, v.Ciphertext, v.aad())
	if err != nil {
		return nil, ErrDecrypt
	}
	defer wipe(pt)
	var plain map[string]string
	if err := json.Unmarshal(pt, &plain); err != nil {
		return nil, ErrDecrypt
	}
	out := make(map[string]Secret, len(plain))
	for k, s := range plain {
		out[k] = New(s)
	}
	return out, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// DefaultVaultPath is $FFAGENT_VAULT, else <user config dir>/ffagent/secrets.vault.
func DefaultVaultPath() string {
	if p := os.Getenv("FFAGENT_VAULT"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ffagent", "secrets.vault")
}

// WriteVault atomically writes a vault file readable only by its owner.
func WriteVault(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vault-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadVault reads and decrypts the vault at path.
func ReadVault(path string, passphrase []byte) (map[string]Secret, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("secrets: no vault at %s; create one with `ffagent secrets set`", path)
	}
	if err != nil {
		return nil, err
	}
	return Open(passphrase, data)
}

// checkPrivate rejects files that anyone but the owner can read or write.
func checkPrivate(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("secrets: %s is not a regular file", path)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("secrets: %s is accessible by other users (mode %s); run: chmod 600 %s",
			path, strconv.FormatUint(uint64(perm), 8), path)
	}
	return nil
}
