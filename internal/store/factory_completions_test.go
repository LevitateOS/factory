package store

import (
	"fmt"
	"strings"
	"testing"
)

// FW30 freezes the D13 completion storage contract: unique events
// ordered by repository generation and append-only cycles. FW31 adds
// persistence and the atomic fence against these tables.
func TestFactoryCompletionSchemaRegistered(t *testing.T) {
	if SchemaVersion() < 60 {
		t.Fatalf("D13 schema version: %d", SchemaVersion())
	}
	joined := strings.Join(schemaStatements, "\n")
	for _, want := range []string{
		FactoryCompletionsDDL, FactoryReassessmentsDDL,
		FactoryCompletionTriggers, FactoryCompletionDeleteTrigger,
		FactoryReassessmentDeleteTrigger,
		fmt.Sprintf("INSERT INTO schema_version(version) VALUES(%d)", SchemaVersion()),
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("completion schema statement missing: %.60s", want)
		}
	}
	if TableFactoryCompletions != "factory_completions" || TableFactoryReassessments != "factory_reassessments" {
		t.Fatal("completion table owners renamed")
	}
	for _, stmt := range []string{FactoryCompletionsDDL, FactoryReassessmentsDDL} {
		if !strings.Contains(stmt, "JSONB NOT NULL") || !strings.Contains(stmt, TableFactoryCompletions) && !strings.Contains(stmt, TableFactoryReassessments) {
			t.Fatal("completion table outside its storage contract")
		}
	}
	if !strings.Contains(FactoryCompletionsDDL, "UNIQUE(publication,occurrence)") {
		t.Fatal("completion event unique key missing")
	}
	if !strings.Contains(FactoryCompletionsDDL, "PRIMARY KEY(repository,generation)") {
		t.Fatal("completion generation ordering missing")
	}
}
