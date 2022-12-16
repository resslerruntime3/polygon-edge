package polybft

import (
	"encoding/json"
	"fmt"

	bolt "go.etcd.io/bbolt"
)

/*
state sync events/
|--> stateSyncEvent.Id -> *StateSyncEvent (json marshalled)

commitments/
|--> commitment.Message.ToIndex -> *CommitmentMessageSigned (json marshalled)

bundles/
|--> bundle.StateSyncs[0].Id -> *BundleProof (json marshalled)
*/

type StateSyncStore struct {
	db *bolt.DB
}

// insertStateSyncEvent inserts a new state sync event to state event bucket in db
func (s *StateSyncStore) insertStateSyncEvent(event *StateSyncEvent) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}

		bucket := tx.Bucket(syncStateEventsBucket)

		return bucket.Put(itob(event.ID), raw)
	})
}

// getStateSyncEventsForCommitment returns state sync events for commitment
// if there is an event with index that can not be found in db in given range, an error is returned
func (s *StateSyncStore) getStateSyncEventsForCommitment(fromIndex, toIndex uint64) ([]*StateSyncEvent, error) {
	var events []*StateSyncEvent

	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(syncStateEventsBucket)
		for i := fromIndex; i <= toIndex; i++ {
			v := bucket.Get(itob(i))
			if v == nil {
				return errNotEnoughStateSyncs
			}

			var event *StateSyncEvent
			if err := json.Unmarshal(v, &event); err != nil {
				return err
			}

			events = append(events, event)
		}

		return nil
	})

	return events, err
}

// insertCommitmentMessage inserts signed commitment to db
func (s *StateSyncStore) insertCommitmentMessage(commitment *CommitmentMessageSigned) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		raw, err := json.Marshal(commitment)
		if err != nil {
			return err
		}

		if err := tx.Bucket(commitmentsBucket).Put(itob(commitment.Message.ToIndex), raw); err != nil {
			return err
		}

		return nil
	})
}

// getCommitmentMessage queries the signed commitment from the db
func (s *StateSyncStore) getCommitmentMessage(toIndex uint64) (*CommitmentMessageSigned, error) {
	var commitment *CommitmentMessageSigned

	err := s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(commitmentsBucket).Get(itob(toIndex))
		if raw == nil {
			return nil
		}

		return json.Unmarshal(raw, &commitment)
	})

	return commitment, err
}

// cleanCommitments cleans all commitments that are older than the provided fromIndex, alongside their proofs
func (s *StateSyncStore) cleanCommitments(stateSyncExecutionIndex uint64) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		commitmentsBucket := tx.Bucket(commitmentsBucket)
		commitmentsCursor := commitmentsBucket.Cursor()
		for k, _ := commitmentsCursor.First(); k != nil; k, _ = commitmentsCursor.Next() {
			if itou(k) >= stateSyncExecutionIndex {
				// reached a commitment that is not executed
				break
			}

			if err := commitmentsBucket.Delete(k); err != nil {
				return err
			}
		}

		bundlesBucket := tx.Bucket(bundlesBucket)
		bundlesCursor := bundlesBucket.Cursor()
		for k, _ := bundlesCursor.First(); k != nil; k, _ = bundlesCursor.Next() {
			if itou(k) >= stateSyncExecutionIndex {
				// reached a bundle that is not executed
				break
			}

			if err := bundlesBucket.Delete(k); err != nil {
				return err
			}
		}

		return nil
	})
}

// insertBundles inserts the provided bundles to db
func (s *StateSyncStore) insertBundles(bundles []*BundleProof) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bundlesBucket := tx.Bucket(bundlesBucket)
		for _, b := range bundles {
			raw, err := json.Marshal(b)
			if err != nil {
				return err
			}

			if err := bundlesBucket.Put(itob(b.ID()), raw); err != nil {
				return err
			}
		}

		return nil
	})
}

// getBundles gets bundles that are not executed
func (s *StateSyncStore) getBundles(stateSyncExecutionIndex, maxNumberOfBundles uint64) ([]*BundleProof, error) {
	var bundles []*BundleProof

	err := s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bundlesBucket).Cursor()
		processed := uint64(0)
		for k, v := c.First(); k != nil && processed < maxNumberOfBundles; k, v = c.Next() {
			if itou(k) >= stateSyncExecutionIndex {
				var bundle *BundleProof
				if err := json.Unmarshal(v, &bundle); err != nil {
					return err
				}

				bundles = append(bundles, bundle)
				processed++
			}
		}

		return nil
	})

	return bundles, err
}

// insertMessageVote inserts given vote to signatures bucket of given epoch
func (s *StateSyncStore) insertMessageVote(epoch uint64, key []byte, vote *MessageSignature) (int, error) {
	var numSignatures int

	err := s.db.Update(func(tx *bolt.Tx) error {
		signatures, err := s.getMessageVotesLocked(tx, epoch, key)
		if err != nil {
			return err
		}

		// check if the signature has already being included
		for _, sigs := range signatures {
			if sigs.From == vote.From {
				numSignatures = len(signatures)

				return nil
			}
		}

		if signatures == nil {
			signatures = []*MessageSignature{vote}
		} else {
			signatures = append(signatures, vote)
		}
		numSignatures = len(signatures)

		raw, err := json.Marshal(signatures)
		if err != nil {
			return err
		}

		bucket, err := getNestedBucketInEpoch(tx, epoch, messageVotesBucket)
		if err != nil {
			return err
		}

		if err := bucket.Put(key, raw); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return 0, err
	}

	return numSignatures, nil
}

// getMessageVotes gets all signatures from db associated with given epoch and hash
func (s *StateSyncStore) getMessageVotes(epoch uint64, hash []byte) ([]*MessageSignature, error) {
	var signatures []*MessageSignature

	err := s.db.View(func(tx *bolt.Tx) error {
		res, err := s.getMessageVotesLocked(tx, epoch, hash)
		if err != nil {
			return err
		}
		signatures = res

		return nil
	})

	if err != nil {
		return nil, err
	}

	return signatures, nil
}

// getMessageVotesLocked gets all signatures from db associated with given epoch and hash
func (s *StateSyncStore) getMessageVotesLocked(tx *bolt.Tx, epoch uint64, hash []byte) ([]*MessageSignature, error) {
	bucket, err := getNestedBucketInEpoch(tx, epoch, messageVotesBucket)
	if err != nil {
		return nil, err
	}

	v := bucket.Get(hash)
	if v == nil {
		return nil, nil
	}

	var signatures []*MessageSignature
	if err := json.Unmarshal(v, &signatures); err != nil {
		return nil, err
	}

	return signatures, nil
}

// getNestedBucketInEpoch returns a nested (child) bucket from db associated with given epoch
func getNestedBucketInEpoch(tx *bolt.Tx, epoch uint64, bucketKey []byte) (*bolt.Bucket, error) {
	epochBucket, err := getEpochBucket(tx, epoch)
	if err != nil {
		return nil, err
	}

	bucket := epochBucket.Bucket(bucketKey)

	if epochBucket == nil {
		return nil, fmt.Errorf("could not find %v bucket for epoch: %v", string(bucketKey), epoch)
	}

	return bucket, nil
}
