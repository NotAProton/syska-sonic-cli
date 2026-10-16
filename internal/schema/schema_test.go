package schema

import (
	"strings"
	"testing"
)

// TestVocabularyConsistency enforces the cross-CLI rulebook mechanically:
// every command declares --json (except bare version), mutating commands
// declare --dry-run and --yes, listings declare --limit/--offset, all flag
// references exist in the registry, and ops are unique.
func TestVocabularyConsistency(t *testing.T) {
	bad := Validate()
	if len(bad) != 0 {
		t.Fatalf("schema violations:\n%s", strings.Join(bad, "\n"))
	}
}

func TestAllFlagReferencesResolve(t *testing.T) {
	for _, c := range Commands {
		for _, f := range c.Flags {
			if _, ok := Flags[f.Name]; !ok {
				t.Errorf("command %s references missing flag %q", c.Name, f.Name)
			}
		}
	}
}

func TestOpsAreUniqueAndNamespaced(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Commands {
		if seen[c.Op] {
			t.Errorf("duplicate op %q", c.Op)
		}
		seen[c.Op] = true
		if !strings.HasPrefix(c.Op, c.Group+".") {
			t.Errorf("op %q does not match group namespace %q", c.Op, c.Group)
		}
	}
}

func TestListsAreBounded(t *testing.T) {
	for _, c := range Commands {
		if isListing(c.Name) {
			if !has(c.Flags, "limit") || !has(c.Flags, "offset") {
				t.Errorf("%s is a listing without --limit/--offset", c.Name)
			}
		}
	}
}

func TestMutatingDeviceOpsHaveDryRunAndYes(t *testing.T) {
	for _, c := range Commands {
		if c.Group == "device" && c.Mutating {
			if !has(c.Flags, "dry-run") {
				t.Errorf("%s mutating device op without --dry-run", c.Name)
			}
			if !has(c.Flags, "yes") {
				t.Errorf("%s mutating device op without --yes", c.Name)
			}
		}
	}
}

func TestDeviceCommandsHaveExamples(t *testing.T) {
	for _, c := range Commands {
		if c.Group == "meta" {
			continue
		}
		if len(c.Examples) == 0 {
			t.Errorf("%s has no examples", c.Name)
		}
	}
}
