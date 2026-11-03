package store

import (
	"context"
)

// ProjectMaintenanceWork returns bounded global counts of active or uncertain
// managed work. The sentinel 129 means at least 129, never a complete larger
// count. Appliance readiness can finish setup; a native preparation requires an
// explicit scoped retirement receipt. Ready alone does not prove native stop;
// missing observations and interrupted/failed setup remain explicit blockers
// until their exact candidate owner records confirmed native retirement.
func (s *Store) ProjectMaintenanceWork(ctx context.Context) (factory, preparation int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM (SELECT 1 FROM factory_runs WHERE active OR NOT settled LIMIT 129) work),
		(SELECT count(*) FROM (SELECT 1 FROM project_preparations
		 WHERE NOT soda_preparation_quiescent(data,id,project_id,role) LIMIT 129) work)`).Scan(&factory, &preparation)
	return factory, preparation, err
}
