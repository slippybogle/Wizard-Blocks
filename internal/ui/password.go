package ui

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// The optional settings password set from the UI is stored only as a salted
// PBKDF2-SHA256 hash in <data_dir>/ui-password-<coin>.json.

const (
	pwIterations = 210000
	pwMinLen     = 8
	pwMaxLen     = 256
)

type pwHash struct {
	Alg  string `json:"alg"`
	Iter int    `json:"iter"`
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

func hashPassword(pw string) (*pwHash, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	k, err := pbkdf2.Key(sha256.New, pw, salt, pwIterations, 32)
	if err != nil {
		return nil, err
	}
	return &pwHash{Alg: "pbkdf2-sha256", Iter: pwIterations, Salt: hex.EncodeToString(salt), Hash: hex.EncodeToString(k)}, nil
}

func (h *pwHash) verify(pw string) bool {
	salt, err1 := hex.DecodeString(h.Salt)
	want, err2 := hex.DecodeString(h.Hash)
	if err1 != nil || err2 != nil || h.Alg != "pbkdf2-sha256" || h.Iter < 1 || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, h.Iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

func loadPwHash(path string) (*pwHash, error) {
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var h pwHash
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if h.Alg != "pbkdf2-sha256" || h.Hash == "" {
		return nil, fmt.Errorf("%s: unsupported password hash", path)
	}
	return &h, nil
}

func savePwHash(path string, h *pwHash) error {
	b, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
