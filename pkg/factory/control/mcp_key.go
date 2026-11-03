package control

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

// LoadFactoryMCPKey shares one persistent signing key between dispatch and
// the private MCP listener. Only the derived run secret is delivered to a
// worker; this key stays in the backend's private data directory.
func LoadFactoryMCPKey(publicationRoot string) ([]byte, error) {
	if publicationRoot == "" {
		publicationRoot = "/var/lib/soda/dashboard/publication"
	}
	path := filepath.Join(filepath.Dir(publicationRoot), "factory-mcp.key")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, writeErr := file.Write(key)
			closeErr := file.Close()
			if writeErr != nil {
				return nil, writeErr
			}
			if closeErr != nil {
				return nil, closeErr
			}
		} else if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() != 32 {
		return nil, errors.New("factory MCP signing key is unavailable or not private")
	}
	return os.ReadFile(path)
}
