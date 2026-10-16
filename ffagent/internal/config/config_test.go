package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectsPlaintextSecretsInFile(t *testing.T) {
	t.Setenv("ESPN_LEAGUE_ID", "1")
	for _, key := range []string{"espn_s2", "SWID"} {
		p := filepath.Join(t.TempDir(), "c.json")
		os.WriteFile(p, []byte(fmt.Sprintf(`{"league_id": 1, %q: "AEBplaintext"}`, key)), 0o600)
		_, err := Load(p)
		if !errors.Is(err, ErrPlaintextSecret) {
			t.Errorf("%s in config: err = %v", key, err)
		}
		if err != nil && strings.Contains(err.Error(), "AEBplaintext") {
			t.Errorf("error message leaks the value: %v", err)
		}
	}
}

func TestRejectsPlaintextSecretsInEnv(t *testing.T) {
	t.Setenv("ESPN_LEAGUE_ID", "1")
	t.Setenv("ESPN_S2", "AEBplaintext")
	_, err := Load("")
	if !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := os.LookupEnv("ESPN_S2"); ok {
		t.Error("plaintext env var should be scrubbed from the environment")
	}
}

func TestExampleConfigIsAccepted(t *testing.T) {
	if _, err := Load("../../config.example.json"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectsNestedEmailPassword(t *testing.T) {
	t.Setenv("ESPN_LEAGUE_ID", "1")
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"league_id": 1, "email": {"address": "a@b.c", "app_password": "abcd efgh"}}`), 0o600)
	if _, err := Load(p); !errors.Is(err, ErrPlaintextSecret) {
		t.Fatalf("nested password accepted: %v", err)
	}
}
