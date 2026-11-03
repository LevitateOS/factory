package store

import (
	"context"
	"database/sql"
	"testing"
)

func TestReadSchemaVersionAndOpenObserve(t *testing.T) {
	ctx := context.Background()
	s, dsn := postgresFixture(t)
	if err := s.UpsertUser(ctx, User{ID: 1, Login: "soda-tester"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, Project{ID: "p000000000000000000000001", Name: "Demo", RepositoryID: 7, OwnerID: 1, Repository: "soda-tester/demo"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	version, err := ReadSchemaVersion(ctx, dsn)
	if err != nil || version != SchemaVersion() {
		t.Fatalf("ReadSchemaVersion: %d %v", version, err)
	}
	observed, err := OpenObserve(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = observed.IntegrityCheck(ctx); err != nil {
		_ = observed.Close()
		t.Fatal(err)
	}
	user, err := observed.User(ctx, 1)
	if err != nil || user.Login != "soda-tester" {
		_ = observed.Close()
		t.Fatalf("User: %+v %v", user, err)
	}
	project, err := observed.Project(ctx, "p000000000000000000000001")
	_ = observed.Close()
	if err != nil || project.Repository != "soda-tester/demo" {
		t.Fatalf("Project: %+v %v", project, err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE schema_version SET version=$1`, SchemaVersion()-1); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = db.QueryRow(`SELECT login FROM users WHERE id=1`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenObserve(ctx, dsn); err == nil {
		t.Fatal("OpenObserve accepted a mismatched schema")
	}
	var after string
	if err = db.QueryRow(`SELECT login FROM users WHERE id=1`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatal("OpenObserve refusal mutated the database")
	}
	old, err := ReadSchemaVersion(ctx, dsn)
	if err != nil || old != SchemaVersion()-1 {
		t.Fatalf("ReadSchemaVersion after downgrade marker: %d %v", old, err)
	}
}
