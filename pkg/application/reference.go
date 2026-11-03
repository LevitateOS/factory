// Package application defines admitted application references and administration
// records. Canlang owns application semantics, bindings and its descriptor.
package application

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	identifier = regexp.MustCompile(`^a[0-9a-f]{24}$`)
	digest     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	image      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	team       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	name       = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)
)

// Spec is the same mechanical native transport as soda-host's application
// executor. Its digests reference admitted bytes; they do not certify them.
type Spec struct {
	ID             string `json:"id"`
	Revision       int64  `json:"revision"`
	Name           string `json:"name"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	BundleSHA256   string `json:"bundle_sha256"`
	ToolsSHA256    string `json:"tools_sha256"`
	RuntimeImage   string `json:"runtime_image"`
	Image          string `json:"image"`
	Data           string `json:"data"`
	OwnerTeam      string `json:"owner_team"`
	Origin         string `json:"origin"`
	Port           int    `json:"port"`
}

func (s Spec) Validate() error {
	if !s.validIdentity() || !s.validReferences() || !canonicalOrigin(s.Origin) {
		return errors.New("invalid admitted application references")
	}
	return nil
}

func (s Spec) validIdentity() bool {
	return identifier.MatchString(s.ID) && identifier.MatchString(s.Data) && s.Revision >= 1 && name.MatchString(s.Name)
}

func (s Spec) validReferences() bool {
	return digest.MatchString(s.ArtifactSHA256) && digest.MatchString(s.BundleSHA256) && digest.MatchString(s.ToolsSHA256) && image.MatchString(s.RuntimeImage) && image.MatchString(s.Image) && team.MatchString(s.OwnerTeam) && s.Port >= 20000 && s.Port < 30000
}

func canonicalOrigin(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.User == nil && u.Opaque == "" && originPath(u, value) && originHost(u.Hostname()) && originPort(u, value)
}

func originPath(u *url.URL, value string) bool {
	return u.Path == "" && u.RawPath == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && !strings.Contains(value, "#")
}

func originHost(host string) bool {
	if host == "" || len(host) > 253 || host == "localhost" || !strings.Contains(host, ".") || net.ParseIP(host) != nil || host != strings.ToLower(host) || strings.ContainsAny(host, " %/:\\\t\r\n") {
		return false
	}
	return dnsLabels(host)
}

func dnsLabels(host string) bool {
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if !dnsLabel(label) {
			return false
		}
		for _, c := range label {
			if !dnsCharacter(c) {
				return false
			}
		}
	}
	return true
}
func dnsLabel(label string) bool {
	return label != "" && len(label) <= 63 && !strings.HasPrefix(label, "-") && !strings.HasSuffix(label, "-")
}
func dnsCharacter(c rune) bool { return c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') }
func originPort(u *url.URL, value string) bool {
	port := u.Port()
	if port == "" {
		return u.Host == u.Hostname() && value == "https://"+u.Hostname()
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 || n == 443 || strconv.Itoa(n) != port {
		return false
	}
	return u.Host == net.JoinHostPort(u.Hostname(), port) && value == "https://"+u.Host
}
func sameIdentity(s, next Spec) bool {
	return next.ID == s.ID && next.Name == s.Name && next.Data == s.Data && next.OwnerTeam == s.OwnerTeam && next.Origin == s.Origin && next.Port == s.Port
}

// Replacement preserves the application's identity, private data and access
// grant. Only exact admitted artifact/tool/image references may change.
func (s Spec) Replacement(next Spec) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if err := next.Validate(); err != nil {
		return err
	}
	if s.Revision == int64(^uint64(0)>>1) || next.Revision != s.Revision+1 || !sameIdentity(s, next) {
		return errors.New("application replacement changed identity or revision")
	}
	return nil
}
