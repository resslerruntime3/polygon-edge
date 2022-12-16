package polybft

import (
	"fmt"

	bolt "go.etcd.io/bbolt"
)

/*
epochs/
|--> epochNumber
	|--> hash -> []*MessageSignatures (json marshalled)

validatorSnapshots/
|--> epochNumber -> *AccountSet (json marshalled)
*/

type EpochStore struct {
	db *bolt.DB
}

// insertValidatorSnapshot inserts a validator snapshot for the given epoch to its bucket in db
func (s *EpochStore) insertValidatorSnapshot(epoch uint64, validatorSnapshot AccountSet) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := validatorSnapshot.Marshal()
		if err != nil {
			return err
		}

		bucket := tx.Bucket(validatorSnapshotsBucket)

		return bucket.Put(itob(epoch), raw)
	})
}

// getValidatorSnapshot queries the validator snapshot for given epoch from db
func (s *EpochStore) getValidatorSnapshot(epoch uint64) (AccountSet, error) {
	var validatorSnapshot AccountSet

	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(validatorSnapshotsBucket)
		v := bucket.Get(itob(epoch))
		if v != nil {
			return validatorSnapshot.Unmarshal(v)
		}

		return nil
	})

	return validatorSnapshot, err
}

// insertEpoch inserts a new epoch to db with its meta data
func (s *EpochStore) insertEpoch(epoch uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		epochBucket, err := tx.Bucket(epochsBucket).CreateBucketIfNotExists(itob(epoch))
		if err != nil {
			return err
		}
		_, err = epochBucket.CreateBucketIfNotExists(messageVotesBucket)

		return err
	})
}

// isEpochInserted checks if given epoch is present in db
func (s *EpochStore) isEpochInserted(epoch uint64) bool {
	return s.db.View(func(tx *bolt.Tx) error {
		_, err := getEpochBucket(tx, epoch)

		return err
	}) == nil
}

// getEpochBucket returns bucket from db associated with given epoch
func getEpochBucket(tx *bolt.Tx, epoch uint64) (*bolt.Bucket, error) {
	epochBucket := tx.Bucket(epochsBucket).Bucket(itob(epoch))
	if epochBucket == nil {
		return nil, fmt.Errorf("could not find bucket for epoch: %v", epoch)
	}

	return epochBucket, nil
}

// cleanEpochsFromDB cleans epoch buckets from db
func (s *EpochStore) cleanEpochsFromDB() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket(epochsBucket); err != nil {
			return err
		}
		_, err := tx.CreateBucket(epochsBucket)

		return err
	})
}

// cleanValidatorSnapshotsFromDB cleans the validator snapshots bucket if a limit is reached,
// but it leaves the latest (n) number of snapshots
func (s *EpochStore) cleanValidatorSnapshotsFromDB(epoch uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(validatorSnapshotsBucket)

		// paired list
		keys := make([][]byte, 0)
		values := make([][]byte, 0)
		if numberOfSnapshotsToLeaveInDB > 0 { // TODO this is always true?!
			for i := 0; i < numberOfSnapshotsToLeaveInDB; i++ { // exclude the last inserted we already appended
				key := itob(epoch)
				value := bucket.Get(key)
				if value == nil {
					continue
				}
				keys = append(keys, key)
				values = append(values, value)
				epoch--
			}
		}

		// removing an entire bucket is much faster than removing all keys
		// look at thread https://github.com/boltdb/bolt/issues/667
		err := tx.DeleteBucket(validatorSnapshotsBucket)
		if err != nil {
			return err
		}

		bucket, err = tx.CreateBucket(validatorSnapshotsBucket)
		if err != nil {
			return err
		}

		// we start the loop in reverse so that the oldest of snapshots get inserted first in db
		for i := len(keys) - 1; i >= 0; i-- {
			if err := bucket.Put(keys[i], values[i]); err != nil {
				return err
			}
		}

		return nil
	})
}

// removeAllValidatorSnapshots drops a validator snapshot bucket and re-creates it in bolt database
func (s *EpochStore) removeAllValidatorSnapshots() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		// removing an entire bucket is much faster than removing all keys
		// look at thread https://github.com/boltdb/bolt/issues/667
		err := tx.DeleteBucket(validatorSnapshotsBucket)
		if err != nil {
			return err
		}

		_, err = tx.CreateBucket(validatorSnapshotsBucket)
		if err != nil {
			return err
		}

		return nil
	})
}
