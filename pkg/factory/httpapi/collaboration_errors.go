package httpapi

import (
	"errors"
	"net/http"
	"strconv"
)

// Optional host observation; projection remains Go-owned and credential-free.
// Static messages never expose provider response prose, URLs or request bodies.
func collaborationReadFailure(w http.ResponseWriter, err error) bool {
	var observed interface {
		CollaborationReadFailure() (int, *uint64, *uint64)
	}
	if !errors.As(err, &observed) {
		return false
	}
	status, retry, reset := observed.CollaborationReadFailure()
	code, message := "provider_unavailable", "Collaboration provider is unavailable."
	switch status {
	case 400:
		code, message = "invalid_request", "Invalid collaboration read."
	case 401:
		code, message = "provider_sign_in_required", "Sign in to the collaboration provider again."
	case 403:
		code, message = "provider_forbidden", "The collaboration provider refused this read."
	case 404:
		code, message = "resource_not_found", "The collaboration resource is not visible."
	case 409:
		code, message = "provider_resource_changed", "The collaboration resource changed. Reload it."
	case 413:
		code, message = "provider_read_too_large", "The collaboration read exceeds its bound."
	case 429:
		code, message = "provider_rate_limited", "The collaboration provider rate limit was reached."
	case 502:
		code, message = "invalid_provider_observation", "The collaboration observation is unavailable."
	case 503, 504:
	default:
		status = 503
	}
	if retry != nil {
		w.Header().Set("Retry-After", strconv.FormatUint(*retry, 10))
	}
	if reset != nil {
		w.Header().Set("X-RateLimit-Reset", strconv.FormatUint(*reset, 10))
	}
	// Native transports retain JSON even when they do not expose HTTP headers.
	// Keep the same safe rate observations available to both shared hosts.
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retry_after": retry, "reset": reset}})
	return true
}
