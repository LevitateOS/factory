// Package collaboration owns provider-neutral collaboration records. An identity
// observation is not a permission, execution grant or credential.
package collaboration

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type HostID string
type ProfileID string
type RuntimeID string
type ConnectionID string
type RepositoryID int64
type ActorID int64
type TeamID int64
type Provider string
type RuntimeKind string

const (
	Fountain  Provider    = "fountain"
	GitHub    Provider    = "github"
	Native    RuntimeKind = "native"
	Appliance RuntimeKind = "appliance"
)

var (
	ErrIdentityConflict = errors.New("collaboration identity cannot be rebound")
	ErrUnboundLedger    = errors.New("retained Fountain ledger must be adopted before identity allocation")
)

type Host struct {
	ID       HostID   `json:"id"`
	Provider Provider `json:"provider"`
	APIBase  string   `json:"api_base"`
}

type Profile struct {
	ID   ProfileID `json:"id"`
	Name string    `json:"name"`
}

// ID is a local ledger surrogate; ExternalID is opaque and scoped to Host.
// Name is presentation metadata, never an identity key.
type RepositoryRef struct {
	ID         RepositoryID `json:"id,string"`
	Host       HostID       `json:"host"`
	ExternalID string       `json:"external_id"`
	Name       string       `json:"name"`
}

type ActorRef struct {
	ID         ActorID `json:"id,string"`
	Host       HostID  `json:"host"`
	ExternalID string  `json:"external_id"`
	Login      string  `json:"login"`
}

// Teams have their own canonical identity space. ExternalID is opaque and scoped
// to Host; a team is never a negative actor or a login/organization slug key.
// Observing identity and display metadata grants no membership or permission.
type TeamRef struct {
	ID         TeamID `json:"id,string"`
	Host       HostID `json:"host"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

// Connection identifies an account binding, not the token kept by native custody.
// A different account gets a different connection. Refresh keeps this identity.
type ConnectionRef struct {
	ID      ConnectionID `json:"id"`
	Profile ProfileID    `json:"profile"`
	Host    HostID       `json:"host"`
	Actor   ActorID      `json:"actor,string"`
}

type RuntimeRef struct {
	ID      RuntimeID   `json:"id"`
	Profile ProfileID   `json:"profile"`
	Kind    RuntimeKind `json:"kind"`
	Name    string      `json:"name"`
}

func NewID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}

func ValidID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func ValidText(text string, limit int) bool {
	return utf8.ValidString(text) && strings.TrimSpace(text) != "" &&
		utf8.RuneCountInString(text) <= limit && !strings.ContainsFunc(text, unicode.IsControl)
}

// CanonicalAPIBase normalizes the configured instance locator. HostID remains
// stable when this locator changes. A URL never identifies a repository/account.
func CanonicalAPIBase(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || !ValidText(raw, 2048) || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("invalid collaboration API base")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid collaboration API port")
		}
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = strings.TrimRight(u.RawPath, "/")
	return u.String(), nil
}
