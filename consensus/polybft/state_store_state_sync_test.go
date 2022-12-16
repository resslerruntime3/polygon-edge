package polybft

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_Insert_And_Get_MessageVotes(t *testing.T) {
	t.Parallel()

	state := newTestState(t)
	epoch := uint64(1)
	assert.NoError(t, state.EpochStore.insertEpoch(epoch))

	hash := []byte{1, 2}
	_, err := state.StateSyncStore.insertMessageVote(1, hash, &MessageSignature{
		From:      "NODE_1",
		Signature: []byte{1, 2},
	})

	assert.NoError(t, err)

	votes, err := state.StateSyncStore.getMessageVotes(epoch, hash)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(votes))
	assert.Equal(t, "NODE_1", votes[0].From)
	assert.True(t, bytes.Equal([]byte{1, 2}, votes[0].Signature))
}

func TestState_InsertVoteConcurrent(t *testing.T) {
	t.Parallel()

	state := newTestState(t)
	epoch := uint64(1)
	assert.NoError(t, state.EpochStore.insertEpoch(epoch))

	hash := []byte{1, 2}

	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			_, _ = state.StateSyncStore.insertMessageVote(epoch, hash, &MessageSignature{
				From:      fmt.Sprintf("NODE_%d", i),
				Signature: []byte{1, 2},
			})
		}(i)
	}

	wg.Wait()

	signatures, err := state.StateSyncStore.getMessageVotes(epoch, hash)
	assert.NoError(t, err)
	assert.Len(t, signatures, 100)
}

func TestState_Insert_And_Cleanup(t *testing.T) {
	t.Parallel()

	state := newTestState(t)
	hash1 := []byte{1, 2}

	for i := uint64(1); i < 1001; i++ {
		epoch := i
		err := state.EpochStore.insertEpoch(epoch)

		assert.NoError(t, err)

		_, _ = state.StateSyncStore.insertMessageVote(epoch, hash1, &MessageSignature{
			From:      "NODE_1",
			Signature: []byte{1, 2},
		})
	}

	// BucketN returns number of all buckets inside root bucket (including nested buckets) + the root itself
	// Since we inserted 1000 epochs we expect to have 2000 buckets inside epochs root bucket
	// (1000 buckets for epochs + each epoch has 1 nested bucket for message votes)
	assert.Equal(t, 2000, state.epochsDBStats().BucketN-1)

	err := state.EpochStore.cleanEpochsFromDB()
	assert.NoError(t, err)

	assert.Equal(t, 0, state.epochsDBStats().BucketN-1)

	// there should be no votes for given epoch since we cleaned the db
	votes, _ := state.StateSyncStore.getMessageVotes(1, hash1)
	assert.Nil(t, votes)

	for i := uint64(1001); i < 2001; i++ {
		epoch := i
		err := state.EpochStore.insertEpoch(epoch)
		assert.NoError(t, err)

		_, _ = state.StateSyncStore.insertMessageVote(epoch, hash1, &MessageSignature{
			From:      "NODE_1",
			Signature: []byte{1, 2},
		})
	}

	assert.Equal(t, 2000, state.epochsDBStats().BucketN-1)

	votes, _ = state.StateSyncStore.getMessageVotes(2000, hash1)
	assert.Equal(t, 1, len(votes))
}

func TestState_getStateSyncEventsForCommitment_NotEnoughEvents(t *testing.T) {
	t.Parallel()

	state := newTestState(t)

	for i := 0; i < stateSyncMainBundleSize-2; i++ {
		assert.NoError(t, state.StateSyncStore.insertStateSyncEvent(&StateSyncEvent{
			ID:   uint64(i),
			Data: []byte{1, 2},
		}))
	}

	_, err := state.StateSyncStore.getStateSyncEventsForCommitment(0, stateSyncMainBundleSize-1)
	assert.ErrorIs(t, err, errNotEnoughStateSyncs)
}

func TestState_getStateSyncEventsForCommitment(t *testing.T) {
	t.Parallel()

	state := newTestState(t)

	for i := 0; i < stateSyncMainBundleSize; i++ {
		assert.NoError(t, state.StateSyncStore.insertStateSyncEvent(&StateSyncEvent{
			ID:   uint64(i),
			Data: []byte{1, 2},
		}))
	}

	events, err := state.StateSyncStore.getStateSyncEventsForCommitment(0, stateSyncMainBundleSize-1)
	assert.NoError(t, err)
	assert.Equal(t, stateSyncMainBundleSize, len(events))
}

func TestState_insertCommitmentMessage(t *testing.T) {
	t.Parallel()

	commitment, err := createTestCommitmentMessage(11)
	require.NoError(t, err)

	state := newTestState(t)
	assert.NoError(t, state.StateSyncStore.insertCommitmentMessage(commitment))

	commitmentFromDB, err := state.StateSyncStore.getCommitmentMessage(commitment.Message.ToIndex)

	assert.NoError(t, err)
	assert.NotNil(t, commitmentFromDB)
	assert.Equal(t, commitment, commitmentFromDB)
}

func TestState_cleanCommitments(t *testing.T) {
	t.Parallel()

	const (
		numberOfCommitments = 10
		numberOfBundles     = 10
	)

	lastCommitmentToIndex := uint64(numberOfCommitments*stateSyncMainBundleSize - stateSyncMainBundleSize - 1)

	state := newTestState(t)
	insertTestCommitments(t, state, 1, numberOfCommitments)
	insertTestBundles(t, state, numberOfBundles)

	assert.NoError(t, state.StateSyncStore.cleanCommitments(lastCommitmentToIndex))

	commitment, err := state.StateSyncStore.getCommitmentMessage(lastCommitmentToIndex)
	assert.NoError(t, err)
	assert.Equal(t, lastCommitmentToIndex, commitment.Message.ToIndex)

	for i := uint64(1); i < numberOfCommitments; i++ {
		c, err := state.StateSyncStore.getCommitmentMessage(i*stateSyncMainBundleSize + lastCommitmentToIndex - 1)
		assert.NoError(t, err)
		assert.Nil(t, c)
	}

	bundles, err := state.StateSyncStore.getBundles(0, maxBundlesPerSprint)
	assert.NoError(t, err)
	assert.Nil(t, bundles)
}

func TestState_insertAndGetBundles(t *testing.T) {
	t.Parallel()

	const numberOfBundles = 10

	state := newTestState(t)
	commitment, err := createTestCommitmentMessage(0)
	require.NoError(t, err)
	require.NoError(t, state.StateSyncStore.insertCommitmentMessage(commitment))

	insertTestBundles(t, state, numberOfBundles)

	bundlesFromDB, err := state.StateSyncStore.getBundles(0, maxBundlesPerSprint)

	assert.NoError(t, err)
	assert.Equal(t, numberOfBundles, len(bundlesFromDB))
	assert.Equal(t, uint64(0), bundlesFromDB[0].ID())
	assert.Equal(t, stateSyncBundleSize, len(bundlesFromDB[0].StateSyncs))
	assert.NotNil(t, bundlesFromDB[0].Proof)
}

func insertTestCommitments(t *testing.T, state *State, epoch, numberOfCommitments uint64) {
	t.Helper()

	for i := uint64(0); i <= numberOfCommitments; i++ {
		commitment, err := createTestCommitmentMessage(i * stateSyncMainBundleSize)
		require.NoError(t, err)
		require.NoError(t, state.StateSyncStore.insertCommitmentMessage(commitment))
	}
}

func insertTestBundles(t *testing.T, state *State, numberOfBundles uint64) {
	t.Helper()

	bundles := make([]*BundleProof, numberOfBundles)

	for i := uint64(0); i < numberOfBundles; i++ {
		bundle := &BundleProof{
			Proof:      []types.Hash{types.BytesToHash(generateRandomBytes(t))},
			StateSyncs: createTestStateSyncs(stateSyncBundleSize, i*stateSyncBundleSize),
		}
		bundles[i] = bundle
	}

	require.NoError(t, state.StateSyncStore.insertBundles(bundles))
}
