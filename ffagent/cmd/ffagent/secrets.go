package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/avitechnix/lexx587-experimental/ffagent/internal/config"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/espn"
	"github.com/avitechnix/lexx587-experimental/ffagent/internal/secrets"
)

var swidPattern = regexp.MustCompile(`^\{[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}\}$`)

// runSecrets manages the encrypted vault. Secret values are only ever read
// from the terminal with echo off and are never printed.
func runSecrets(sub, path string) error {
	switch sub {
	case "set":
		return secretsSet(path)
	case "check":
		return secretsCheck(path)
	case "passwd":
		return secretsPasswd(path)
	}
	return fmt.Errorf("unknown secrets command %q (want set, check or passwd)", sub)
}

func secretsSet(path string) error {
	var pass []byte
	var err error
	if _, statErr := os.Stat(path); statErr == nil {
		pass, err = secrets.Passphrase()
		if err != nil {
			return err
		}
		if _, err := secrets.ReadVault(path, pass); err != nil {
			wipe(pass)
			return err
		}
	} else {
		fmt.Fprintln(os.Stderr, "Creating a new encrypted vault at", path)
		if pass, err = secrets.NewPassphrase(); err != nil {
			return err
		}
	}
	defer wipe(pass)

	fmt.Fprintln(os.Stderr, "Paste values from your browser's espn.com cookies. Input is hidden.")
	s2, err := secrets.Prompt("espn_s2: ")
	if err != nil {
		return err
	}
	defer wipe(s2)
	swidRaw, err := secrets.Prompt("SWID: ")
	if err != nil {
		return err
	}
	defer wipe(swidRaw)

	espnS2 := strings.TrimSpace(string(s2))
	swid := espn.NormalizeSWID(string(swidRaw))
	if espnS2 == "" {
		return errors.New("espn_s2 is empty")
	}
	if !swidPattern.MatchString(swid) {
		return errors.New("SWID doesn't look right; it should look like {XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX}")
	}
	data, err := secrets.Seal(pass, map[string]secrets.Secret{
		config.VaultKeyESPNS2: secrets.New(espnS2),
		config.VaultKeySWID:   secrets.New(swid),
	})
	if err != nil {
		return err
	}
	if err := secrets.WriteVault(path, data); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Saved encrypted credentials to", path)
	return nil
}

func secretsCheck(path string) error {
	pass, err := secrets.Passphrase()
	if err != nil {
		return err
	}
	defer wipe(pass)
	vals, err := secrets.ReadVault(path, pass)
	if err != nil {
		return err
	}
	fmt.Println("Vault decrypts OK:", path)
	for _, k := range []string{config.VaultKeyESPNS2, config.VaultKeySWID} {
		state := "missing"
		if v, ok := vals[k]; ok && !v.Empty() {
			state = "set"
		}
		fmt.Printf("  %-8s %s\n", k, state)
	}
	return nil
}

func secretsPasswd(path string) error {
	old, err := secrets.Passphrase()
	if err != nil {
		return err
	}
	vals, err := secrets.ReadVault(path, old)
	wipe(old)
	if err != nil {
		return err
	}
	pass, err := secrets.NewPassphrase()
	if err != nil {
		return err
	}
	defer wipe(pass)
	data, err := secrets.Seal(pass, vals)
	if err != nil {
		return err
	}
	if err := secrets.WriteVault(path, data); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Passphrase changed.")
	return nil
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
