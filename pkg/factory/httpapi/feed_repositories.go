package httpapi

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/levitateos/factory/pkg/collaboration"
	factoryservice "github.com/levitateos/factory/pkg/factory/service"
)

func (s Feed) repositories(w http.ResponseWriter, r *http.Request) {
	query := factoryservice.RepositoryQuery{Limit: 50}
	values, err := url.ParseQuery(r.URL.RawQuery)
	valid := err == nil
	for key, fields := range values {
		if len(fields) != 1 {
			valid = false
			continue
		}
		switch key {
		case "after":
			n, err := strconv.ParseInt(fields[0], 10, 64)
			if err != nil || n < 1 || strconv.FormatInt(n, 10) != fields[0] {
				valid = false
			}
			query.After = collaboration.RepositoryID(n)
		case "limit":
			n, err := strconv.Atoi(fields[0])
			if err != nil || n < 1 || n > 100 {
				valid = false
			}
			query.Limit = n
		default:
			valid = false
		}
	}
	if !valid {
		writeError(w, http.StatusBadRequest, "invalid_request", "Repository catalogue query is invalid.")
		return
	}
	catalogue := factoryservice.Repositories{Store: s.Service.Store, Visibility: s.Visibility}
	page, err := catalogue.Catalogue(r.Context(), s.Actor, query)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "catalogue_unavailable", "Factory repositories are unavailable.")
		return
	}
	if !s.current(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, struct {
		factoryservice.RepositoryCatalogue
		Host  collaboration.HostID `json:"host"`
		Actor int64                `json:"actor,string"`
	}{page, s.Host, int64(s.Actor)})
}
