package store

import "context"

type Member struct {
	UserID int64
	Login  string
}

// Members is a bounded scan of project membership for the administrator
// listing, mirroring the SpaceProjects cap: callers must authorize every
// returned row and must not treat the cap as an exact roster.
func (s *Store) Members(ctx context.Context, project string) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id,login FROM memberships WHERE project_id=$1 ORDER BY login LIMIT 129`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []Member{}
	for rows.Next() {
		var member Member
		if err = rows.Scan(&member.UserID, &member.Login); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}
