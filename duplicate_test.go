package main

import "testing"

func TestDetectDuplicateGroups(t *testing.T) {
	projects := []ProjectCandidate{
		{ID: "1", Name: "tienda"},
		{ID: "2", Name: "tienda-copia"},
		{ID: "3", Name: "tienda-final"},
		{ID: "4", Name: "tienda-final-ahora-si"},
	}
	groups := detectDuplicateGroups(projects)
	if len(groups) != 1 {
		t.Fatalf("expected one group, got %d", len(groups))
	}
	group := groups[0]
	if group.Key != "tienda" || group.Title != "Tienda" || len(group.Variants) != 4 {
		t.Fatalf("unexpected group: %#v", group)
	}
	wanted := []string{"main", "previous version", "stable version", "variant 1"}
	for index, label := range wanted {
		if group.Variants[index].Label != label {
			t.Errorf("variant %d: expected %q, got %q", index, label, group.Variants[index].Label)
		}
	}
}

func TestDuplicatesJoinTransitivelyByNameAndRemote(t *testing.T) {
	remote := "https://github.com/acme/shared.git"
	groups := detectDuplicateGroups([]ProjectCandidate{
		{ID: "1", Name: "uno", GitRemote: &remote},
		{ID: "2", Name: "dos", GitRemote: &remote},
		{ID: "3", Name: "dos-copia"},
	})
	if len(groups) != 1 || len(groups[0].Variants) != 3 {
		t.Fatalf("expected one transitive group of three: %#v", groups)
	}
}
