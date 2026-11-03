package factory

import "strings"

// OwnsAPIPath identifies Factory's private service routes. The Fountain
// frontend adapter uses the same boundary as the receiving Factory service.
// Choosing a route never grants native actor or execution authority.
func OwnsAPIPath(path string) bool {
	parts := strings.Split(path, "/")
	if len(parts) < 3 || parts[1] != "api" {
		return false
	}
	if parts[2] == "factory" {
		return true
	}
	if len(parts) >= 4 && parts[2] == "identity" && parts[3] == "jev" {
		return true
	}
	if len(parts) >= 5 && parts[2] == "repositories" && parts[3] != "" {
		return parts[4] == "factory" || parts[4] == "application-builds"
	}
	return len(parts) >= 5 && parts[2] == "environments" && parts[3] != "" && parts[4] == "preparation"
}
