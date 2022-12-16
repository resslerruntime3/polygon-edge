package polybft

import (
	"fmt"
	"os"
	"path"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/umbracle/ethgo"
)

func newTestState(t *testing.T) *State {
	t.Helper()

	dir := fmt.Sprintf("/tmp/consensus-temp_%v", time.Now().Format(time.RFC3339Nano))
	err := os.Mkdir(dir, 0777)

	if err != nil {
		t.Fatal(err)
	}

	state, err := newState(path.Join(dir, "my.db"), hclog.NewNullLogger(), make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	})

	return state
}

func createTestStateSyncs(numberOfEvents, startIndex uint64) []*StateSyncEvent {
	stateSyncEvents := make([]*StateSyncEvent, 0)
	for i := startIndex; i < numberOfEvents+startIndex; i++ {
		stateSyncEvents = append(stateSyncEvents, &StateSyncEvent{
			ID:       i,
			Sender:   ethgo.ZeroAddress,
			Receiver: ethgo.ZeroAddress,
			Data:     []byte{0, 1},
		})
	}

	return stateSyncEvents
}

func createTestCommitmentMessage(fromIndex uint64) (*CommitmentMessageSigned, error) {
	tree, err := NewMerkleTree([][]byte{
		{0, 1},
		{2, 3},
		{4, 5},
	})
	if err != nil {
		return nil, err
	}

	msg := &CommitmentMessage{
		MerkleRootHash: tree.Hash(),
		FromIndex:      fromIndex,
		ToIndex:        fromIndex + stateSyncMainBundleSize - 1,
		BundleSize:     2,
	}

	return &CommitmentMessageSigned{
		Message:      msg,
		AggSignature: Signature{},
	}, nil
}
