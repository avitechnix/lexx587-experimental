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
func runSecrets(sub string, args []string, path string) error {
	switch sub {
	case "set":
		which := "espn"
		if len(args) > 0 {
			which = args[0]
		}
		return secretsSet(path, which)
	case "check":
		return secretsCheck(path)
	case "passwd":
		return secretsPasswd(path)
	}
	return fmt.Errorf("unknown secrets command %q (want set, check or passwd)", sub)
}

// openOrCreate returns the vault passphrase and current values, creating a
// new (empty) vault passphrase if there's no vault yet.
func openOrCreate(path string) ([]byte, map[string]secrets.Secret, error) {
	if _, err := os.Stat(path); err == nil {
		pass, err := secrets.Passphrase()
		if err != nil {
			return nil, nil, err
		}
		vals, err := secrets.ReadVault(path, pass)
		if err != nil {
			wipe(pass)
			return nil, nil, err
		}
		return pass, vals, nil
	}
	fmt.Fprintln(os.Stderr, "Creating a new encrypted vault at", path)
	// A service install supplies a generated passphrase (e.g. a systemd
	// encrypted credential); otherwise ask for one.
	pass, ok, err := secrets.Unattended()
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		if pass, err = secrets.NewPassphrase(); err != nil {
			return nil, nil, err
		}
	}
	return pass, map[string]secrets.Secret{}, nil
}

func secretsSet(path, which string) error {
	if which != "espn" && which != "email" {
		return fmt.Errorf("secrets set: want 'espn' or 'email', not %q", which)
	}
	pass, vals, err := openOrCreate(path)
	if err != nil {
		return err
	}
	defer wipe(pass)

	switch which {
	case "espn":
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
		vals[config.VaultKeyESPNS2], vals[config.VaultKeySWID] = secrets.New(espnS2), secrets.New(swid)
	case "email":
		fmt.Fprintln(os.Stderr, "Paste the app password for the agent's email account (for Gmail: myaccount.google.com/apppasswords). Input is hidden.")
		pw, err := secrets.Prompt("App password: ")
		if err != nil {
			return err
		}
		defer wipe(pw)
		v := strings.ReplaceAll(strings.TrimSpace(string(pw)), " ", "") // Gmail shows it in groups of 4
		if len(v) < 8 {
			return errors.New("that app password looks too short")
		}
		vals[config.VaultKeyEmail] = secrets.New(v)
	}
	data, err := secrets.Seal(pass, vals)
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
	for _, k := range []string{config.VaultKeyESPNS2, config.VaultKeySWID, config.VaultKeyEmail} {
		state := "missing"
		if v, ok := vals[k]; ok && !v.Empty() {
			state = "set"
		}
		fmt.Printf("  %-15s %s\n", k, state)
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
