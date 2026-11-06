package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func readCollaborationQuery(w http.ResponseWriter, r *http.Request, subject collaboration.SubjectKind) (factoryservice.CollaborationQuery, bool) {
	noun, detail := "issue", "An issue number without query parameters is required."
	if subject == collaboration.SubjectPull {
		noun, detail = "pull", "A PR number without query parameters is required."
	}
	query := factoryservice.CollaborationQuery{Limit: 20}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, 400, "invalid_request", "Invalid "+noun+" query.")
		return query, false
	}
	for key, items := range values {
		if len(items) != 1 || key != "cursor" && key != "limit" && key != "state" {
			writeError(w, 400, "invalid_request", "Only one cursor, limit and state are accepted.")
			return query, false
		}
	}
	query.Cursor, query.State = values.Get("cursor"), values.Get("state")
	if raw, exists := values["limit"]; exists {
		query.Limit, err = strconv.Atoi(raw[0])
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid "+noun+" limit.")
			return query, false
		}
	}
	if raw := r.PathValue("number"); raw != "" {
		number, ok := positiveID(raw)
		if !ok || r.URL.RawQuery != "" || r.URL.ForceQuery {
			writeError(w, 400, "invalid_request", detail)
			return query, false
		}
		query.Number, query.Limit = number, 1
	}
	return query, true
}
