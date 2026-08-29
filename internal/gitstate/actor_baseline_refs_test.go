package gitstate

import "testing"

func TestDiscoverDescriptorsIgnoresActorQuarantineBaselinePins(t *testing.T) {
	repo := newDiscoveryRepo(t)
	store := NewStore(repo, "real-workflow")
	if err := store.SetJSON(DescriptorRecord, NewDescriptor("real-workflow", "", RecordNames{})); err != nil {
		t.Fatal(err)
	}

	quarantine, err := repo.CreateActorWorktree()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = quarantine.Remove() })
	baselineRef, err := actorBaselineRef(quarantine.Repo.Root, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := readActorBaselinePin(repo, baselineRef); err != nil || !ok {
		t.Fatalf("retained quarantine baseline pin: present=%t err=%v", ok, err)
	}

	items, err := repo.DiscoverDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Workflow != "real-workflow" || items[0].Descriptor == nil {
		t.Fatalf("discovered workflows with retained quarantine = %#v", items)
	}
}
