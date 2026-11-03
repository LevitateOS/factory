package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/levitateos/sodaos/internal/collaboration"
)

type (
	retainedCollaborationColumn struct{ table, name string }
	retainedCollaborationRefs   struct{ repositories, actors map[int64]string }
)

func quoteCollaborationIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func retainedCollaborationColumns(ctx context.Context, tx *sql.Tx) ([]retainedCollaborationColumn, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.table_name,c.column_name,c.data_type
 FROM information_schema.columns c JOIN information_schema.tables t ON t.table_schema=c.table_schema AND t.table_name=c.table_name
 WHERE c.table_schema='public' AND t.table_type='BASE TABLE' AND left(c.table_name,14)<>'collaboration_'
 ORDER BY c.table_name,c.ordinal_position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []retainedCollaborationColumn
	for rows.Next() {
		var table, name, kind string
		if err := rows.Scan(&table, &name, &kind); err != nil {
			return nil, err
		}
		if retainedIdentityKind(name) != "" || table == "users" && name == "id" || kind == "jsonb" || kind == "json" || table == "factory_commands" && (name == "target" || name == "payload" || name == "outcome") {
			columns = append(columns, retainedCollaborationColumn{table, name})
		}
	}
	return columns, rows.Err()
}

func lockRetainedCollaborationTables(ctx context.Context, tx *sql.Tx, columns []retainedCollaborationColumn) error {
	previous := ""
	for _, column := range columns {
		if column.table == previous {
			continue
		}
		previous = column.table
		// Composition calls this before reopening dispatch. The lock also keeps
		// the captured inventory stable against an in-flight legacy writer.
		if _, err := tx.ExecContext(ctx, `LOCK TABLE public.`+quoteCollaborationIdentifier(column.table)+` IN SHARE MODE`); err != nil {
			return err
		}
	}
	return nil
}

func readRetainedCollaborationRefs(ctx context.Context, tx *sql.Tx, columns []retainedCollaborationColumn) (retainedCollaborationRefs, error) {
	refs := retainedCollaborationRefs{repositories: map[int64]string{}, actors: map[int64]string{}}
	for _, column := range columns {
		name := column.name
		if column.table == "users" && name == "id" {
			name = "actor_id"
		}
		quoted := quoteCollaborationIdentifier(column.name)
		rows, err := tx.QueryContext(ctx, `SELECT to_jsonb(`+quoted+`)::text FROM public.`+quoteCollaborationIdentifier(column.table)+` WHERE `+quoted+` IS NOT NULL`)
		if err != nil {
			return refs, err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return refs, err
			}
			var value any
			decoder := json.NewDecoder(strings.NewReader(raw))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				rows.Close()
				return refs, fmt.Errorf("read retained identity metadata in %s.%s: %w", column.table, column.name, err)
			}
			if column.table == "factory_commands" && (name == "payload" || name == "outcome") {
				text, _ := value.(string)
				if strings.TrimSpace(text) == "" {
					continue
				}
				if !json.Valid([]byte(text)) && !strings.HasPrefix(strings.TrimSpace(text), "{") && !strings.HasPrefix(strings.TrimSpace(text), "[") {
					continue
				}
				decoder = json.NewDecoder(strings.NewReader(text))
				decoder.UseNumber()
				if err := decoder.Decode(&value); err != nil {
					rows.Close()
					return refs, fmt.Errorf("%w: retained command JSON is unreadable", collaboration.ErrIdentityConflict)
				}
			}
			refs.collect(name, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return refs, err
		}
	}
	// Labels are optional historical metadata. Import never changes their source
	// rows, infers a permission or asserts that a remote resource still exists.
	for _, q := range []struct {
		query string
		dest  map[int64]string
	}{
		{`SELECT id,login FROM users`, refs.actors}, {`SELECT repository_id,repository FROM projects`, refs.repositories},
	} {
		rows, err := tx.QueryContext(ctx, q.query)
		if err != nil {
			return refs, err
		}
		for rows.Next() {
			var id int64
			var label string
			if err := rows.Scan(&id, &label); err != nil {
				rows.Close()
				return refs, err
			}
			if id > 0 && collaboration.ValidText(label, 512) {
				q.dest[id] = label
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return refs, err
		}
	}
	return refs, nil
}

func retainedIdentityKind(field string) string {
	field = strings.ToLower(field)
	if field == "repository" || field == "repository_id" || field == "repositories" || field == "repositoryid" || strings.HasSuffix(field, "_repository") || strings.HasSuffix(field, "_repository_id") {
		return "repository"
	}
	switch field {
	case "actor", "actor_id", "actors", "actorid", "user", "users", "user_id", "owner", "owners", "owner_id", "author", "authors", "author_id", "subscriber", "subscriber_id", "administrator", "administrator_id", "creator_id", "poster_id", "reviewer_id", "withdrawer", "principal", "decision_owner", "environment_administrator", "environment_owner", "project_administrator":
		return "actor"
	}
	for _, suffix := range []string{"_actor", "_actor_id", "_actors", "_author", "_author_id", "_user_id", "_owner_id"} {
		if strings.HasSuffix(field, suffix) {
			return "actor"
		}
	}
	return ""
}

func (refs retainedCollaborationRefs) collect(field string, value any) {
	kind := retainedIdentityKind(field)
	if field == "target" {
		if text, ok := value.(string); ok {
			parts := strings.Split(text, "/")
			if len(parts) >= 2 && parts[0] == "repository" {
				refs.add("repository", parts[1])
			}
		}
	}
	switch value := value.(type) {
	case map[string]any:
		if kind != "" {
			refs.add(kind, value["id"])
		}
		for key, child := range value {
			if kind != "" && (field == "repositories" || field == "actors" || field == "users") {
				refs.add(kind, key)
			}
			refs.collect(key, child)
		}
	case []any:
		for _, child := range value {
			refs.collect(field, child)
		}
	default:
		if kind != "" {
			refs.add(kind, value)
		}
	}
}

func (refs retainedCollaborationRefs) add(kind string, value any) {
	var text string
	switch value := value.(type) {
	case string:
		text = value
	case json.Number:
		text = value.String()
	default:
		return
	}
	if kind == "actor" {
		text = strings.TrimPrefix(text, "native:")
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id < 1 {
		return
	}
	if kind == "repository" {
		refs.repositories[id] = "repository " + strconv.FormatInt(id, 10)
	} else {
		refs.actors[id] = "actor " + strconv.FormatInt(id, 10)
	}
}
