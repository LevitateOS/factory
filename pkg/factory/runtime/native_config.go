package runtime

import (
	"bytes"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/levitateos/factory/pkg/collaboration"
	"github.com/levitateos/factory/pkg/strictjson"
)

// NativeConfig consumes private inputs written by the owned Rust lifecycle.
// It neither provisions storage nor admits an account, repository or runtime.
// Secrets are private process inputs, never webview state or log fields.
type NativeConfig struct {
	Profile   collaboration.ProfileID
	Directory string
	Socket    string
	dsn       string
	ipcKey    []byte
}

func (c NativeConfig) DatabaseDSN() string { return c.dsn }
func (c NativeConfig) IPCKey() []byte      { return bytes.Clone(c.ipcKey) }
func (c NativeConfig) String() string      { return "Factory native bootstrap" }

// LoadNative validates the fixed private profile layout. Parsing never opens a
// database/listener, registers a provider or claims a coordinator ledger.
func LoadNative(path string) (NativeConfig, error) {
	var empty NativeConfig
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "backend.json" {
		return empty, errors.New("native bootstrap requires its absolute profile configuration path")
	}
	root := filepath.Dir(path)
	if err := nativePrivateDirectory(root); err != nil {
		return empty, err
	}
	raw, err := nativePrivateInput(path, 65536)
	if err != nil {
		return empty, err
	}
	var manifest struct {
		Profile collaboration.ProfileID `json:"profile_id"`
	}
	if strictjson.Decode(bytes.NewReader(raw), &manifest) != nil || !collaboration.ValidID(string(manifest.Profile)) {
		return empty, errors.New("native bootstrap configuration is invalid")
	}
	identity, err := nativePrivateInput(filepath.Join(root, "profile.id"), 32)
	if err != nil || string(identity) != string(manifest.Profile) {
		return empty, errors.New("native bootstrap profile identity differs from its owned profile")
	}
	rawDSN, err := nativePrivateInput(filepath.Join(root, "backend.dsn"), 65536)
	if err != nil {
		return empty, err
	}
	dsn := string(rawDSN)
	if dsn == "" || strings.ContainsAny(dsn, "\r\n\x00") {
		return empty, errors.New("native database input is invalid")
	}
	database, err := pgx.ParseConfig(dsn)
	if err != nil || database.Host != filepath.Join(root, "socket") || database.Port != 5432 || database.User != "factory" || database.Password == "" || database.Database != "postgres" || database.TLSConfig != nil || len(database.Fallbacks) != 0 {
		return empty, errors.New("native database must use the owned profile's authenticated Unix socket")
	}
	rawKey, err := nativePrivateInput(filepath.Join(root, "backend.key"), 64)
	if err != nil {
		return empty, err
	}
	key, err := hex.DecodeString(string(rawKey))
	if err != nil || len(rawKey) != 64 || len(key) != 32 {
		return empty, errors.New("native IPC capability input is invalid")
	}
	return NativeConfig{Profile: manifest.Profile, Directory: root, Socket: filepath.Join(root, "backend.sock"), dsn: dsn, ipcKey: key}, nil
}
