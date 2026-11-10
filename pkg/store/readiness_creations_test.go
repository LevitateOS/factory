package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/levitateos/factory/pkg/factory"
)

func creationWork(id string, issue int64) ReadinessWork {
	return ReadinessWork{
		ID: "delivery:" + id, Delivery: id, Root: factory.DependenceRef{Repository: 7, Issue: issue},
		CreationVerified: true, CreatorID: 5, CreatorWrite: true, NativeIssueID: issue + 100, CreatedUnix: 1700000000,
		TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64),
	}
}

func TestReadinessCreationPageCapacityRollsBackCursorAndFacts(t *testing.T) {
	s, ctx := readinessTestStore(t)
	for i := 0; i < maxReadinessSources-1; i++ {
		if err := s.EnqueueReadinessWork(ctx, fmt.Sprintf("source:%d", i), "", factory.DependenceRef{Repository: 7, Issue: int64(i + 1)}); err != nil {
			t.Fatal(err)
		}
	}
	works := []ReadinessWork{creationWork("first", 91), creationWork("second", 92)}
	if err := s.EnqueueReadinessCreationPage(ctx, 7, 0, 22, works); !errors.Is(err, ErrReadinessCapacity) {
		t.Fatal("page must refuse atomically:", err)
	}
	if cursor, err := s.CreationRecoveryCursor(ctx, 7); err != nil || cursor != 0 {
		t.Fatal("capacity consumed native history:", cursor, err)
	}
	if _, err := s.ReadinessWork(ctx, works[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("first page item escaped rollback:", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM factory_readiness_sources WHERE id='source:0'`); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueReadinessCreationPage(ctx, 7, 0, 22, works); err != nil {
		t.Fatal(err)
	}
	if cursor, err := s.CreationRecoveryCursor(ctx, 7); err != nil || cursor != 22 {
		t.Fatal(cursor, err)
	}
	if err := s.EnqueueReadinessCreationPage(ctx, 7, 0, 23, works); !errors.Is(err, ErrStaleRevision) {
		t.Fatal("stale native page rewrote cursor:", err)
	}
	policy := grantTestPolicy()
	policy.Repository = 7
	if err := s.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	policy, err := s.RepositoryPolicy(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	captured := policy.Revision
	policy.Enabled = false
	if err = s.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	late := creationWork("late-policy-reply", 93)
	if err = s.EnqueueReadinessCreationPage(ctx, 7, 22, 23, []ReadinessWork{late}, captured); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatal("withdrawn policy admitted in-flight discovery:", err)
	}
	decision := factory.Acceptance{ID: factory.InitialAcceptanceID(7, "193"), Repository: 7, IssueIndex: "93", Approver: 5, NativeRev: 9, Initial: true, TitleOccurrence: factory.OriginalOccurrence, TitleDigest: late.TitleDigest, ContentDigest: late.ContentDigest}
	if err = s.AdmitAcceptanceDecision(ctx, decision, captured); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatal("withdrawn policy admitted in-flight initial acceptance:", err)
	}
	if cursor, err := s.CreationRecoveryCursor(ctx, 7); err != nil || cursor != 22 {
		t.Fatal("withdrawn discovery advanced history:", cursor, err)
	}
	if _, err = s.ReadinessWork(ctx, late.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("withdrawn discovery retained new work:", err)
	}
	if _, err = s.AcceptanceHead(ctx, 7, 93); !errors.Is(err, ErrNotFound) {
		t.Fatal("withdrawn discovery retained initial acceptance:", err)
	}
	policy, err = s.RepositoryPolicy(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled = true
	if err = s.SaveRepositoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err = s.EnqueueReadinessCreationPage(ctx, 7, 22, 23, []ReadinessWork{late}, captured); !errors.Is(err, ErrAdmissionChanged) {
		t.Fatal("re-enrollment renewed an old in-flight discovery:", err)
	}
}

func TestReadinessCreationEnrichesPartialAndRejectsOriginalRewrite(t *testing.T) {
	s, ctx := readinessTestStore(t)
	full := creationWork("original", 3)
	partial := ReadinessWork{ID: full.ID, Delivery: full.Delivery, Root: full.Root, CreatorID: 5}
	if err := s.EnqueueReadinessCreation(ctx, partial); err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueueReadinessCreationPage(ctx, 7, 0, 9, []ReadinessWork{full}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadinessWork(ctx, full.ID)
	if err != nil || !got.CreationVerified || got.NativeIssueID != 103 || got.TitleDigest != full.TitleDigest {
		t.Fatal("original native task not retained:", got, err)
	}
	full.CreatorWrite = false
	if err := s.EnqueueReadinessCreationPage(ctx, 7, 9, 10, []ReadinessWork{full}); !errors.Is(err, ErrCommandConflict) {
		t.Fatal("verified creation authority overwritten:", err)
	}
	if cursor, err := s.CreationRecoveryCursor(ctx, 7); err != nil || cursor != 9 {
		t.Fatal("conflict advanced native history:", cursor, err)
	}
	if err := s.EnqueueReadinessCreation(ctx, partial); err != nil {
		t.Fatal(err)
	}
	got, err = s.ReadinessWork(ctx, full.ID)
	if err != nil || !got.CreationVerified || !got.CreatorWrite {
		t.Fatal("partial replay downgraded original authority:", got, err)
	}
}

func TestReadinessCreationAdmissionReusesItsDurableRootAtCapacity(t *testing.T) {
	s, ctx := readinessTestStore(t)
	for i := 0; i < maxReadinessSources; i++ {
		if err := s.EnqueueReadinessCreation(ctx, creationWork(fmt.Sprintf("creation:%d", i), int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	decision := factory.Acceptance{ID: factory.InitialAcceptanceID(7, "101"), Repository: 7, IssueIndex: "1", Approver: 5, NativeRev: 9, Initial: true, TitleOccurrence: factory.OriginalOccurrence, TitleDigest: strings.Repeat("a", 64), ContentDigest: strings.Repeat("b", 64)}
	if err := s.AdmitAcceptanceDecision(ctx, decision); err != nil {
		t.Fatal("the already durable creation root must permit its own admission at capacity:", err)
	}
	head, err := s.AcceptanceHead(ctx, 7, 1)
	if err != nil || head != decision.ID {
		t.Fatal(head, err)
	}
	work, err := s.BeginReadinessWork(ctx, "delivery:creation:0", time.Now())
	if err != nil {
		t.Fatal("admission lost its durable assessment source:", err)
	}
	if work.Generation != readinessGeneration(t, s) {
		t.Fatal("creation graph did not restart at admitted generation:", work.Generation)
	}
}
