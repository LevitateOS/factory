// Package testfixtures carries the unitban analyzer's self-test fixture: a
// unit test the analyzer must flag. The analyzer skips this directory, so
// scripts/check-go-unitban.sh vets a copy staged outside it.
package testfixtures

import "testing"

func TestPlantedUnit(t *testing.T) {
	t.Log("flag me")
}

func ExamplePlanted() {}
