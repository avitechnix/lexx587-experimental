// Package secrets keeps credentials encrypted at rest and out of output.
//
// Credentials live only in a vault file sealed with AES-256-GCM under a key
// derived from a passphrase with Argon2id. In memory they are held in a
// Secret, which redacts itself everywhere it could be printed or encoded;
// the plaintext is only reachable through Reveal, at the point of use.
package secrets

import (
	"fmt"
	"log/slog"
)

const redacted = "[REDACTED]"

// Secret is a string that never prints, logs or encodes its value.
type Secret struct{ v string }

// New wraps a plaintext value.
func New(v string) Secret { return Secret{v: v} }

// Reveal returns the plaintext. Call it only where the value is consumed
// (e.g. building an HTTP cookie), never to display it.
func (s Secret) Reveal() string { return s.v }

// Empty reports whether no value is set.
func (s Secret) Empty() bool { return s.v == "" }

// String implements fmt.Stringer.
func (s Secret) String() string { return redacted }

// GoString implements fmt.GoStringer (%#v).
func (s Secret) GoString() string { return redacted }

// Format handles every fmt verb, including %x and %q, which would otherwise
// bypass String for struct fields.
func (s Secret) Format(f fmt.State, _ rune) { fmt.Fprint(f, redacted) }

// MarshalJSON keeps secrets out of JSON output.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"` + redacted + `"`), nil }

// MarshalText keeps secrets out of text encodings.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }

// LogValue keeps secrets out of structured logs.
func (s Secret) LogValue() slog.Value { return slog.StringValue(redacted) }
