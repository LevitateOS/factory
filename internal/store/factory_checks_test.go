package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func checkAssessmentFixture() factory.CheckAssessment {
	checks := []string{"st11-build", "st11-review-gate"}
	return factory.CheckAssessment{
		Results: []factory.CheckResult{
			{Context: "st11-build", State: "success", Passed: true},
			{Context: "st11-review-gate", State: "success", Passed: true},
		},
		Checks:           checks,
		Repository:       7,
		PRNumber:         3,
		PRID:             11,
		IssueID:          13,
		PolicyRevision:   4,
		NativeRev:        41,
		AssessedUnix:     1700000000,
		ObservedContexts: 2,
		HeadRef:          "refs/heads/soda/factory/candidate",
		BaseRef:          "refs/heads/main",
		HeadOID:          strings.Repeat("a", 40),
		BaseOID:          strings.Repeat("b", 40),
		ChecksDigest:     factory.ChecksDigest(checks),
		Verdict:          factory.CheckPass,
		Reason:           factory.CheckReasonPass,
	}
}

func TestRecordCheckAssessmentRoundTrip(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	if _, err := s.CheckAssessment(ctx, 7, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent assessment: %v", err)
	}
	first, err := s.RecordCheckAssessment(ctx, checkAssessmentFixture())
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 {
		t.Fatalf("revision: %+v", first)
	}
	stored, err := s.CheckAssessment(ctx, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, first) {
		t.Fatalf("round trip: %+v", stored)
	}
	// A later head's verdict replaces the earlier one and counts records.
	next := checkAssessmentFixture()
	next.HeadOID = strings.Repeat("c", 40)
	next.NativeRev = 42
	second, err := s.RecordCheckAssessment(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.HeadOID != strings.Repeat("c", 40) {
		t.Fatalf("replacement: %+v", second)
	}
	stored, err = s.CheckAssessment(ctx, 7, 3)
	if err != nil || !reflect.DeepEqual(stored, second) {
		t.Fatalf("latest: %+v %v", stored, err)
	}
	if _, err := s.CheckAssessment(ctx, 7, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other PR: %v", err)
	}
}

func TestRecordCheckAssessmentRejectsMalformed(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	bad := checkAssessmentFixture()
	bad.Verdict = "maybe"
	if _, err := s.RecordCheckAssessment(ctx, bad); err == nil {
		t.Fatal("malformed assessment recorded")
	}
	replayed := checkAssessmentFixture()
	replayed.Revision = 1
	if _, err := s.RecordCheckAssessment(ctx, replayed); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("replayed revision: %v", err)
	}
}
