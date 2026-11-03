package store

import (
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/project"
)

func TestProjectWithoutCreationProfileAndImmutableCreation(t *testing.T) {
	s, _ := postgresFixture(t)
	if err := s.UpsertUser(t.Context(), User{ID: 1, Login: "soda-tester", Name: "Synthetic account"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO projects(id,name,repository_id,owner_id,repository,ip,ready) VALUES('existing','Existing',7,1,'soda-tester/existing','10.0.0.2',TRUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `INSERT INTO memberships(project_id,user_id,login) VALUES('existing',1,'soda-tester')`); err != nil {
		t.Fatal(err)
	}
	p, err := s.Project(t.Context(), "existing")
	if err != nil || p.Profile != nil || !p.Ready || p.IP != "10.0.0.2" {
		t.Fatal(p, err)
	}
	if login, err := s.MemberLogin(t.Context(), "existing", 1); err != nil || login != "soda-tester" {
		t.Fatal(login, err)
	}
	profile := project.Profile{ID: project.RockyHeadless, Distribution: "rocky", Version: "10.2", Interface: "headless", Architecture: "amd64", Image: "sha256:" + strings.Repeat("a", 64), Revision: strings.Repeat("b", 40)}
	p = Project{ID: "new", Name: "New", RepositoryID: 8, OwnerID: 1, Repository: "soda-tester/new", Profile: &profile}
	if err := s.CreateProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkReady(t.Context(), "new", "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.ProjectByRepository(t.Context(), 8)
	if err != nil || loaded.Profile == nil || *loaded.Profile != profile || !loaded.Ready {
		t.Fatal(loaded, err)
	}
	if _, err := s.db.ExecContext(t.Context(), `UPDATE projects SET creation_profile=NULL WHERE id='new'`); err == nil {
		t.Fatal("creation identity was mutable")
	}
}
