package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/levitateos/sodaos/internal/factory"
)

func TestLibraryDecisionAuthorityAndCreationAtomicity(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	cap := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	decision := func(choice string) factory.LibraryItem {
		raw, _ := json.Marshal(map[string]any{"title": "Task visibility", "summary": "The task policy was recorded.", "scope": "task access", "decision": choice, "rationale": "Product request"})
		submission := testMCPSubmissionRow(cap, factory.MCPToolRecordDecision, raw)
		receipt, err := s.RecordWorkerPost(ctx, submission)
		if err != nil {
			t.Fatal(err)
		}
		item, err := s.LibraryRead(ctx, cap.Repository, receipt.Payload.Communication.LibraryItemID, false)
		if err != nil {
			t.Fatal(err)
		}
		return item.Item
	}
	decision("Owners only")
	decision("All team members")
	before, _ := s.LibrarySearch(ctx, cap.Repository, "", "", "", false, false)
	raw := []byte(`{"title":"Unscoped learning","summary":"Missing source evidence","scope":"all tasks"}`)
	submission := testMCPSubmissionRow(cap, factory.MCPToolLearned, raw)
	if _, err := s.RecordWorkerPost(ctx, submission); err == nil {
		t.Fatal("invalid learning accepted")
	}
	after, _ := s.LibrarySearch(ctx, cap.Repository, "", "", "", false, false)
	if len(before.Items) != len(after.Items) {
		t.Fatal("rejected learning leaked library item")
	}
}

// TestRecordDecisionSeparatesOwnerFromLogger proves the R32.5
// logging/authority distinction against real PostgreSQL: a worker
// records its own choice by leaving the owner blank, or references a
// choice made by an authorized owner; the revision actor always stays
// the logging worker either way.
func TestRecordDecisionSeparatesOwnerFromLogger(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	worker := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	record := func(payload map[string]any) (factory.LibraryItem, factory.LibraryRevision, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmissionRow(worker, factory.MCPToolRecordDecision, raw)
		receipt, err := s.RecordWorkerPost(ctx, submission)
		if err != nil {
			return factory.LibraryItem{}, factory.LibraryRevision{}, err
		}
		read, err := s.LibraryRead(ctx, worker.Repository, receipt.Payload.Communication.LibraryItemID, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(read.History) != 1 {
			t.Fatalf("decision history=%d", len(read.History))
		}
		return read.Item, read.History[0], nil
	}
	base := map[string]any{"title": "Task visibility", "summary": "The task policy was recorded.", "scope": "task access", "decision": "Owners only", "rationale": "Product request"}
	own, revision, err := record(base)
	if err != nil {
		t.Fatalf("own choice: %v", err)
	}
	if own.OwnerRole != string(worker.Profile) {
		t.Fatalf("own choice owner=%q, want logger %q", own.OwnerRole, worker.Profile)
	}
	if revision.Actor != "worker:"+worker.AssignmentID {
		t.Fatalf("own choice actor=%q", revision.Actor)
	}
	referenced := map[string]any{"title": "Task visibility", "summary": "Controller approved owners-only.", "scope": "task access", "decision": "Owners only", "rationale": "Controller approval", "decision_owner": "controller"}
	other, orevision, err := record(referenced)
	if err != nil {
		t.Fatalf("referenced choice: %v", err)
	}
	if other.OwnerRole != "controller" {
		t.Fatalf("referenced owner=%q, want controller", other.OwnerRole)
	}
	if orevision.Actor != "worker:"+worker.AssignmentID {
		t.Fatalf("referenced actor=%q", orevision.Actor)
	}
	if other.ID == own.ID {
		t.Fatal("referenced choice reused the logger's identity")
	}
	for _, bad := range []struct {
		name    string
		payload map[string]any
	}{
		{"blank owner", map[string]any{"title": "T", "summary": "S", "scope": "task access", "decision": "D", "rationale": "R", "decision_owner": "   "}},
		{"long owner", map[string]any{"title": "T", "summary": "S", "scope": "task access", "decision": "D", "rationale": "R", "decision_owner": strings.Repeat("o", 161)}},
	} {
		if _, _, err := record(bad.payload); err == nil {
			t.Fatalf("%s admitted", bad.name)
		}
	}
	learned := map[string]any{"title": "T", "summary": "S", "scope": "task access", "evidence_refs": []string{"src/x.go:1"}, "decision_owner": "controller"}
	raw, _ := json.Marshal(learned)
	learnedSubmission := testMCPSubmissionRow(worker, factory.MCPToolLearned, raw)
	if _, err := s.RecordWorkerPost(ctx, learnedSubmission); err == nil {
		t.Fatal("learned post named a decision owner")
	}
}

// TestLearnedAmendAndChallengeCAS proves the R32.4 knowledge
// lifecycle against real PostgreSQL: a learned entry amends under
// compare-and-swap with full revision history, a stale expected
// revision refuses instead of overwriting, a challenge marks the
// entry contested without touching its body, and a later amendment
// restores observed. One identity spans work, feed and retrieval.
func TestLearnedAmendAndChallengeCAS(t *testing.T) {
	s, _ := postgresFixture(t)
	ctx := context.Background()
	worker := activateMCPCapability(t, ctx, s, testMCPCapabilityRow())
	submit := func(tool factory.MCPTool, payload map[string]any) (FeedPostDetail, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		submission := testMCPSubmissionRow(worker, tool, raw)
		return s.RecordWorkerPost(ctx, submission)
	}
	learned := map[string]any{"title": "Task ownership", "summary": "Keep ownership checks at the boundary.", "scope": "task access", "evidence_refs": []string{"src/access.go:12"}}
	receipt, err := submit(factory.MCPToolLearned, learned)
	if err != nil {
		t.Fatalf("learn: %v", err)
	}
	id := receipt.Payload.Communication.LibraryItemID
	read := func() factory.LibraryItem {
		out, err := s.LibraryRead(ctx, worker.Repository, id, false)
		if err != nil {
			t.Fatal(err)
		}
		return out.Item
	}
	if item := read(); item.Revision != 1 || item.State != "observed" {
		t.Fatalf("created: %+v", item)
	}
	amend := map[string]any{"title": "Task ownership", "summary": "Keep ownership checks at the boundary and in review.", "scope": "task access", "evidence_refs": []string{"src/access.go:12", "src/review.go:3"}, "revises_item": id, "expected_revision": 1}
	areceipt, err := submit(factory.MCPToolLearned, amend)
	if err != nil {
		t.Fatalf("amend: %v", err)
	}
	if areceipt.Payload.Communication.LibraryItemID != id || areceipt.Record.ID == "" {
		t.Fatalf("amend lost identity: %+v", areceipt)
	}
	if item := read(); item.Revision != 2 || item.State != "observed" || item.SourcePost != areceipt.Record.ID || !strings.Contains(item.Body, "and in review") {
		t.Fatalf("amended: %+v", item)
	}
	if _, err := submit(factory.MCPToolLearned, amend); err == nil {
		t.Fatal("stale amendment overwrote")
	}
	challenge := map[string]any{"title": "Ownership check disputed", "summary": "Review already covers this.", "scope": "task access", "evidence_refs": []string{"src/review.go:3"}, "revises_item": id, "expected_revision": 2, "challenge": true}
	if _, err := submit(factory.MCPToolLearned, challenge); err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if item := read(); item.Revision != 3 || item.State != "contested" || !strings.Contains(item.Body, "and in review") {
		t.Fatalf("challenged: %+v", item)
	}
	restore := map[string]any{"title": "Task ownership", "summary": "Boundary plus review, confirmed.", "scope": "task access", "evidence_refs": []string{"src/access.go:12"}, "revises_item": id, "expected_revision": 3}
	if _, err := submit(factory.MCPToolLearned, restore); err != nil {
		t.Fatalf("restore: %v", err)
	}
	out, err := s.LibraryRead(ctx, worker.Repository, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Item.Revision != 4 || out.Item.State != "observed" || len(out.History) != 4 {
		t.Fatalf("restored: %+v history=%d", out.Item, len(out.History))
	}
	badID := map[string]any{"title": "T", "summary": "S", "scope": "task access", "evidence_refs": []string{"src/x.go:1"}, "revises_item": strings.Repeat("f", 32), "expected_revision": 1}
	if _, err := submit(factory.MCPToolLearned, badID); err == nil {
		t.Fatal("unknown entry amended")
	}
	noRev := map[string]any{"title": "T", "summary": "S", "scope": "task access", "evidence_refs": []string{"src/x.go:1"}, "revises_item": id}
	if _, err := submit(factory.MCPToolLearned, noRev); err == nil {
		t.Fatal("amendment without revision admitted")
	}
	bare := map[string]any{"title": "T", "summary": "S", "scope": "task access", "evidence_refs": []string{"src/x.go:1"}, "challenge": true}
	if _, err := submit(factory.MCPToolLearned, bare); err == nil {
		t.Fatal("challenge without target admitted")
	}
	flag := map[string]any{"title": "T", "summary": "S", "severity": "info", "revises_item": id, "expected_revision": 4}
	if _, err := submit(factory.MCPToolFlag, flag); err == nil {
		t.Fatal("non-learned amendment admitted")
	}
}
