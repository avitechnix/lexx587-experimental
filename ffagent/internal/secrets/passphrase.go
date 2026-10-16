package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/term"
)

// MinPassphraseLen is the shortest passphrase accepted for a new vault.
const MinPassphraseLen = 12

// Passphrase finds the vault passphrase for this run, in order:
//
//  1. $CREDENTIALS_DIRECTORY/ffagent-passphrase — a systemd encrypted
//     credential (LoadCredentialEncrypted=), decrypted only for the service.
//  2. $FFAGENT_PASSPHRASE_FILE — a file that must be private (mode 600).
//  3. $FFAGENT_PASSPHRASE — for CI secret stores (e.g. GitHub Actions
//     secrets). It is removed from the environment as soon as it's read so
//     child processes never inherit it.
//  4. An interactive prompt with echo off.
//
// The caller should wipe the returned slice when done.
func Passphrase() ([]byte, error) {
	if dir := os.Getenv("CREDENTIALS_DIRECTORY"); dir != "" {
		p := filepath.Join(dir, "ffagent-passphrase")
		if _, err := os.Stat(p); err == nil {
			return readPassFile(p, false)
		}
	}
	if p := os.Getenv("FFAGENT_PASSPHRASE_FILE"); p != "" {
		return readPassFile(p, true)
	}
	if v, ok := os.LookupEnv("FFAGENT_PASSPHRASE"); ok {
		os.Unsetenv("FFAGENT_PASSPHRASE")
		if v == "" {
			return nil, errors.New("secrets: FFAGENT_PASSPHRASE is empty")
		}
		return []byte(v), nil
	}
	return Prompt("Vault passphrase: ")
}

func readPassFile(path string, requirePrivate bool) ([]byte, error) {
	if requirePrivate {
		if err := checkPrivate(path); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimRight(b, "\r\n")
	if len(b) == 0 {
		return nil, fmt.Errorf("secrets: passphrase file %s is empty", path)
	}
	return b, nil
}

// Prompt reads a line from the terminal without echoing it. It refuses to
// read from a pipe or file, so secrets can't be fed in from plaintext.
func Prompt(label string) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("secrets: no terminal to prompt on; provide the passphrase via a systemd credential, FFAGENT_PASSPHRASE_FILE, or a CI secret in FFAGENT_PASSPHRASE")
	}
	fmt.Fprint(os.Stderr, label)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return b, err
}

// NewPassphrase prompts twice for a new passphrase and checks its length.
func NewPassphrase() ([]byte, error) {
	p1, err := Prompt("New vault passphrase: ")
	if err != nil {
		return nil, err
	}
	if len(p1) < MinPassphraseLen {
		wipe(p1)
		return nil, fmt.Errorf("secrets: passphrase must be at least %d characters", MinPassphraseLen)
	}
	p2, err := Prompt("Repeat passphrase: ")
	if err != nil {
		wipe(p1)
		return nil, err
	}
	defer wipe(p2)
	if !bytes.Equal(p1, p2) {
		wipe(p1)
		return nil, errors.New("secrets: passphrases don't match")
	}
	return p1, nil
}
