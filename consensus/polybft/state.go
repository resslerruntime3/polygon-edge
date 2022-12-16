package polybft

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	bolt "go.etcd.io/bbolt"

	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
)

var (
	// ABI
	stateTransferEventABI = abi.MustNewEvent("event StateSynced(uint256 indexed id, address indexed sender, address indexed receiver, bytes data)")   //nolint:lll
	exitEventABI          = abi.MustNewEvent("event L2StateSynced(uint256 indexed id, address indexed sender, address indexed receiver, bytes data)") //nolint:lll
	ExitEventABIType      = abi.MustNewType("tuple(uint256 id, address sender, address receiver, bytes data)")
)

const (
	// validatorSnapshotLimit defines a maximum number of validator snapshots
	// that can be stored in cache (both memory and db)
	validatorSnapshotLimit = 100
	// numberOfSnapshotsToLeaveInMemory defines a number of validator snapshots to leave in memory
	numberOfSnapshotsToLeaveInMemory = 2
	// numberOfSnapshotsToLeaveInMemory defines a number of validator snapshots to leave in db
	numberOfSnapshotsToLeaveInDB = 10
	// number of stateSyncEvents to be processed before a commitment message can be created and gossiped
	stateSyncMainBundleSize = 10
	// number of stateSyncEvents to be grouped into one StateTransaction
	stateSyncBundleSize = 1
)

type exitEventNotFoundError struct {
	exitID uint64
	epoch  uint64
}

func (e *exitEventNotFoundError) Error() string {
	return fmt.Sprintf("could not find any exit event that has an id: %v and epoch: %v", e.exitID, e.epoch)
}

// TODO: remove and refactor to use types.StateSyncEvent
// StateSyncEvent is a bridge event from the rootchain
type StateSyncEvent types.StateSyncEvent

// newStateSyncEvent creates an instance of pending state sync event.
func newStateSyncEvent(
	id uint64,
	sender ethgo.Address,
	target ethgo.Address,
	data []byte,
) *StateSyncEvent {
	return &StateSyncEvent{
		ID:       id,
		Sender:   sender,
		Receiver: target,
		Data:     data,
	}
}

func (s *StateSyncEvent) String() string {
	return fmt.Sprintf("Id=%d, Sender=%v, Target=%v", s.ID, s.Sender, s.Receiver)
}

func decodeStateSyncEvent(log *ethgo.Log) (*StateSyncEvent, error) {
	raw, err := stateTransferEventABI.ParseLog(log)
	if err != nil {
		return nil, err
	}

	eventGeneric, err := decodeEventData(raw, log,
		func(id *big.Int, sender, receiver ethgo.Address, data []byte) interface{} {
			return newStateSyncEvent(id.Uint64(), sender, receiver, data)
		})
	if err != nil {
		return nil, err
	}

	stateSyncEvent, ok := eventGeneric.(*StateSyncEvent)
	if !ok {
		return nil, errors.New("failed to convert event to StateSyncEvent instance")
	}

	return stateSyncEvent, nil
}

func decodeExitEvent(log *ethgo.Log, epoch, block uint64) (*ExitEvent, error) {
	if !exitEventABI.Match(log) {
		// valid case, not an exit event
		return nil, nil
	}

	raw, err := exitEventABI.Inputs.ParseLog(log)
	if err != nil {
		return nil, err
	}

	eventGeneric, err := decodeEventData(raw, log,
		func(id *big.Int, sender, receiver ethgo.Address, data []byte) interface{} {
			return &ExitEvent{ID: id.Uint64(),
				Sender:      sender,
				Receiver:    receiver,
				Data:        data,
				EpochNumber: epoch,
				BlockNumber: block}
		})
	if err != nil {
		return nil, err
	}

	exitEvent, ok := eventGeneric.(*ExitEvent)
	if !ok {
		return nil, errors.New("failed to convert event to ExitEvent instance")
	}

	return exitEvent, err
}

// decodeEventData decodes provided map of event metadata and
// creates a generic instance which is returned by eventCreator callback
func decodeEventData(eventDataMap map[string]interface{}, log *ethgo.Log,
	eventCreator func(*big.Int, ethgo.Address, ethgo.Address, []byte) interface{}) (interface{}, error) {
	id, ok := eventDataMap["id"].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("failed to decode id field of log: %+v", log)
	}

	sender, ok := eventDataMap["sender"].(ethgo.Address)
	if !ok {
		return nil, fmt.Errorf("failed to decode sender field of log: %+v", log)
	}

	receiver, ok := eventDataMap["receiver"].(ethgo.Address)
	if !ok {
		return nil, fmt.Errorf("failed to decode receiver field of log: %+v", log)
	}

	data, ok := eventDataMap["data"].([]byte)
	if !ok {
		return nil, fmt.Errorf("failed to decode data field of log: %+v", log)
	}

	return eventCreator(id, sender, receiver, data), nil
}

// convertLog converts types.Log to ethgo.Log
func convertLog(log *types.Log) *ethgo.Log {
	l := &ethgo.Log{
		Address: ethgo.Address(log.Address),
		Data:    log.Data,
		Topics:  make([]ethgo.Hash, len(log.Topics)),
	}

	for i, topic := range log.Topics {
		l.Topics[i] = ethgo.Hash(topic)
	}

	return l
}

// ExitEvent is an event emitted by Exit contract
type ExitEvent struct {
	// ID is the decoded 'index' field from the event
	ID uint64 `abi:"id"`
	// Sender is the decoded 'sender' field from the event
	Sender ethgo.Address `abi:"sender"`
	// Receiver is the decoded 'receiver' field from the event
	Receiver ethgo.Address `abi:"receiver"`
	// Data is the decoded 'data' field from the event
	Data []byte `abi:"data"`
	// EpochNumber is the epoch number in which exit event was added
	EpochNumber uint64 `abi:"-"`
	// BlockNumber is the block in which exit event was added
	BlockNumber uint64 `abi:"-"`
}

// MessageSignature encapsulates sender identifier and its signature
type MessageSignature struct {
	// Signer of the vote
	From string
	// Signature of the message
	Signature []byte
}

// TransportMessage represents the payload which is gossiped across the network
type TransportMessage struct {
	// Hash is encoded data
	Hash []byte
	// Message signature
	Signature []byte
	// Node identifier
	NodeID string
	// Number of epoch
	EpochNumber uint64
}

var (
	// bucket to store rootchain bridge events
	syncStateEventsBucket = []byte("stateSyncEvents")
	// bucket to store exit contract events
	exitEventsBucket = []byte("exitEvent")
	// bucket to store commitments
	commitmentsBucket = []byte("commitments")
	// bucket to store bundles
	bundlesBucket = []byte("bundles")
	// bucket to store epochs and all its nested buckets (message votes and message pool events)
	epochsBucket = []byte("epochs")
	// bucket to store message votes (signatures)
	messageVotesBucket = []byte("votes")
	// bucket to store validator snapshots
	validatorSnapshotsBucket = []byte("validatorSnapshots")
	// array of all parent buckets
	parentBuckets = [][]byte{syncStateEventsBucket, exitEventsBucket, commitmentsBucket, bundlesBucket,
		epochsBucket, validatorSnapshotsBucket}
	// errNotEnoughStateSyncs error message
	errNotEnoughStateSyncs = errors.New("there is either a gap or not enough sync events")
	// errCommitmentNotBuilt error message
	errCommitmentNotBuilt = errors.New("there is no built commitment to register")
	// errNotAnExitEvent error message
	errNotAnExitEvent = errors.New("log not an exit event")
)

// State represents a persistence layer which persists consensus data off-chain
type State struct {
	db     *bolt.DB
	logger hclog.Logger
	close  chan struct{}

	StateSyncStore  *StateSyncStore
	CheckpointStore *CheckpointStore
	EpochStore      *EpochStore
}

func newState(path string, logger hclog.Logger, closeCh chan struct{}) (*State, error) {
	db, err := bolt.Open(path, 0666, nil)
	if err != nil {
		return nil, err
	}

	if err = initMainDBBuckets(db); err != nil {
		return nil, err
	}

	state := &State{
		db:              db,
		logger:          logger.Named("state"),
		close:           closeCh,
		StateSyncStore:  &StateSyncStore{db: db},
		CheckpointStore: &CheckpointStore{db: db},
		EpochStore:      &EpochStore{db: db},
	}

	return state, nil
}

// initMainDBBuckets creates predefined buckets in bolt database if they don't exist already.
func initMainDBBuckets(db *bolt.DB) error {
	// init the buckets
	err := db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range parentBuckets {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
			}
		}

		return nil
	})

	return err
}

// epochsDBStats returns stats of epochs bucket in db
func (s *State) epochsDBStats() *bolt.BucketStats {
	return s.bucketStats(epochsBucket)
}

// validatorSnapshotsDBStats returns stats of validators snapshot bucket in db
func (s *State) validatorSnapshotsDBStats() *bolt.BucketStats {
	return s.bucketStats(validatorSnapshotsBucket)
}

// bucketStats returns stats for the given bucket in db
func (s *State) bucketStats(bucketName []byte) *bolt.BucketStats {
	var stats *bolt.BucketStats

	err := s.db.View(func(tx *bolt.Tx) error {
		s := tx.Bucket(bucketName).Stats()
		stats = &s

		return nil
	})

	if err != nil {
		s.logger.Error("Cannot check bucket stats", "Bucket name", string(bucketName), "Error", err)
	}

	return stats
}

func itob(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)

	return b
}

func itou(v []byte) uint64 {
	return binary.BigEndian.Uint64(v)
}
