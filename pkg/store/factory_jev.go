package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/levitateos/factory/pkg/factory"
)

// BeginJEVAsk records one JEV decision in the command journal, keyed by the
// digest of everything sent, or returns the row a previous pass recorded. A
// finished row is a paid answer (or a final refusal) and is never asked again.
func (s *Store) BeginJEVAsk(ctx context.Context, cmd factory.Command) (factory.Command, error) {
	if !factory.ValidID(cmd.ID) || cmd.Type != factory.CommandJEVAsk || len(cmd.Payload) > 8192 {
		return factory.Command{}, errors.New("invalid JEV journal row")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO factory_commands(id,type,target,principal,digest,payload,created)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, cmd.ID, cmd.Type, cmd.Target, cmd.Principal, cmd.Digest, cmd.Payload, cmd.Created); err != nil {
		return factory.Command{}, err
	}
	saved, err := s.FactoryCommand(ctx, cmd.ID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (saved.Type != cmd.Type || saved.Digest != cmd.Digest) {
		return factory.Command{}, errors.New("JEV journal row does not match its decision")
	}
	return saved, err
}

// NoteJEVAsk replaces the outcome of an unfinished JEV decision. A non-empty
// finished settles it for good.
func (s *Store) NoteJEVAsk(ctx context.Context, id, outcome, finished string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE factory_commands SET outcome=$2, finished=$3 WHERE id=$1 AND type=$4 AND finished=''`, id, outcome, finished, factory.CommandJEVAsk)
	return err
}
