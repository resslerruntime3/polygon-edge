package relayer

import (
	"encoding/binary"
	"fmt"
	"strconv"

	"github.com/0xPolygon/polygon-edge/contracts"
	"github.com/0xPolygon/polygon-edge/txrelayer"
	"github.com/0xPolygon/polygon-edge/types"
	hcf "github.com/hashicorp/go-hclog"
	"github.com/umbracle/ethgo"
	"github.com/umbracle/ethgo/abi"
	"github.com/umbracle/ethgo/jsonrpc"
)

var commitEvent = abi.MustNewEvent(`event NewBundleCommit(uint256 startId, uint256 endId, bytes32 root)`)

type Relayer struct {
	dataDir           string
	rpcEndpoint       string
	stateReceiverAddr ethgo.Address
	logger            hcf.Logger
	client            *jsonrpc.Client
	txRelayer         txrelayer.TxRelayer
	key               ethgo.Key
}

func NewRelayer(
	dataDir string,
	rpcEndpoint string,
	stateReceiverAddr ethgo.Address,
	logger hcf.Logger,
	key ethgo.Key,
) *Relayer {
	// create the JSON RPC client
	client, err := jsonrpc.NewClient(rpcEndpoint)
	if err != nil {
		logger.Error("Failed to create the JSON RPC client", "err", err)

		return nil
	}

	txRelayer, err := txrelayer.NewTxRelayer(txrelayer.WithClient(client))

	return &Relayer{
		dataDir:           dataDir,
		rpcEndpoint:       rpcEndpoint,
		stateReceiverAddr: stateReceiverAddr,
		logger:            logger,
		client:            client,
		txRelayer:         txRelayer,
		key:               key,
	}
}

func (r *Relayer) Start() error {
	et := eventTracker{
		dataDir:           r.dataDir,
		rpcEndpoint:       r.rpcEndpoint,
		stateReceiverAddr: r.stateReceiverAddr,
		subscriber:        r,
		logger:            r.logger,
	}

	return et.start()
}

func (r *Relayer) AddLog(log *ethgo.Log) {
	r.logger.Info("Received a log", "log", log)

	if commitEvent.Match(log) {
		vals, err := commitEvent.ParseLog(log)
		if err != nil {
			panic(err)
		}

		var startID uint64
		if sid, ok := vals["startId"].([]byte); ok {
			startID = binary.LittleEndian.Uint64(sid)
		}

		var endID uint64
		if eid, ok := vals["endId"].([]byte); ok {
			endID = binary.LittleEndian.Uint64(eid)
		}

		fmt.Printf("Commit: Block %d StartID %d EndID %d\n", log.BlockNumber, startID, endID)

		for i := startID; i < endID; i++ {
			// query the state sync proof
			stateSyncProof, err := r.queryStateSyncProof(strconv.Itoa(int(i)))
			if err != nil {
				r.logger.Error("Failed to query state sync proof", "err", err)
			}

			if err := r.executeStateSync(stateSyncProof); err != nil {
				r.logger.Error("Failed to execute state sync", "err", err)
			}
		}
	}
}

// queryStateSyncProof queries the state sync proof
func (r *Relayer) queryStateSyncProof(stateSyncID string) (*types.StateSyncProof, error) {
	// retrieve state sync proof
	var stateSyncProof types.StateSyncProof

	err := r.client.Call("bridge_getStateSyncProof", &stateSyncProof, stateSyncID)
	if err != nil {
		return nil, err
	}

	r.logger.Debug("state sync proof:", stateSyncProof)

	return &stateSyncProof, nil
}

// executeStateSync executes the state sync
func (r *Relayer) executeStateSync(stateSyncProof *types.StateSyncProof) error {
	input, err := types.ExecuteStateSyncABIMethod.Encode(
		[2]interface{}{stateSyncProof.Proof, stateSyncProof.StateSync.ToMap()},
	)
	if err != nil {
		return err
	}

	// execute the state sync
	txn := &ethgo.Transaction{
		From:     r.key.Address(),
		To:       (*ethgo.Address)(&contracts.StateReceiverContract),
		GasPrice: 0,
		Gas:      types.StateTransactionGasLimit,
		Input:    input,
	}

	_, err = r.txRelayer.SendTransaction(txn, r.key)

	return err
}
