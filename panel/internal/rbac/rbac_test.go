package rbac

import "testing"

func TestHas(t *testing.T) {
	admin := []string{"*"}
	nodeAdmin := []string{"node.read", "node.create", "node.delete"}
	nobody := []string{}

	if !Has(admin, NodeCreate) {
		t.Error("wildcard grant must satisfy any permission")
	}
	if !Has(nodeAdmin, NodeRead) {
		t.Error("exact grant must satisfy the permission")
	}
	if Has(nodeAdmin, UserRead) {
		t.Error("unrelated grant must not satisfy the permission")
	}
	if Has(nobody, ServerPower) {
		t.Error("empty grants must satisfy nothing")
	}
	if Has(nil, ConsoleRead) {
		t.Error("nil grants must satisfy nothing")
	}
}

func TestValid(t *testing.T) {
	if !Valid("console.read") {
		t.Error("catalog permission must be valid")
	}
	if !Valid("*") {
		t.Error("wildcard must be valid")
	}
	if Valid("console.delete") || Valid("") {
		t.Error("unknown permissions must be invalid")
	}
}

func TestAllCoversConstants(t *testing.T) {
	all := All()
	if len(all) < 20 {
		t.Fatalf("catalog suspiciously small: %d entries", len(all))
	}
	seen := map[string]bool{}
	for _, p := range all {
		if seen[p] {
			t.Fatalf("duplicate permission %q in catalog", p)
		}
		seen[p] = true
	}
}
