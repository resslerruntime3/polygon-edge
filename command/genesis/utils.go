package genesis

import (
	"encoding/hex"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/command"
	"github.com/0xPolygon/polygon-edge/consensus/polybft"
	"github.com/0xPolygon/polygon-edge/consensus/polybft/wallet"
	"github.com/0xPolygon/polygon-edge/secrets"
	"github.com/0xPolygon/polygon-edge/secrets/helper"
	"github.com/0xPolygon/polygon-edge/secrets/local"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
)

const (
	StatError   = "StatError"
	ExistsError = "ExistsError"
)

type validatorDataPaths struct {
	paths      []string
	pathPrefix string
}

// GenesisGenError is a specific error type for generating genesis
type GenesisGenError struct {
	message   string
	errorType string
}

// GetMessage returns the message of the genesis generation error
func (g *GenesisGenError) GetMessage() string {
	return g.message
}

// GetType returns the type of the genesis generation error
func (g *GenesisGenError) GetType() string {
	return g.errorType
}

type premineInfo struct {
	address types.Address
	balance *big.Int
}

// verifyGenesisExistence checks if the genesis file at the specified path is present
func verifyGenesisExistence(genesisPath string) *GenesisGenError {
	_, err := os.Stat(genesisPath)
	if err != nil && !os.IsNotExist(err) {
		return &GenesisGenError{
			message:   fmt.Sprintf("failed to stat (%s): %v", genesisPath, err),
			errorType: StatError,
		}
	}

	if !os.IsNotExist(err) {
		return &GenesisGenError{
			message:   fmt.Sprintf("genesis file at path (%s) already exists", genesisPath),
			errorType: ExistsError,
		}
	}

	return nil
}

// fillPremineMap fills the premine map for the genesis.json file with passed in balances and accounts
func fillPremineMap(premineMap map[types.Address]*chain.GenesisAccount, premineInfos []*premineInfo) {
	for _, premine := range premineInfos {
		premineMap[premine.address] = &chain.GenesisAccount{
			Balance: premine.balance,
		}
	}
}

// parsePremineInfo parses provided premine information and returns premine address and premine balance
func parsePremineInfo(premineInfoRaw string) (*premineInfo, error) {
	address := types.ZeroAddress
	val := command.DefaultPremineBalance

	if delimiterIdx := strings.Index(premineInfoRaw, ":"); delimiterIdx != -1 {
		// <addr>:<balance>
		address, val = types.StringToAddress(premineInfoRaw[:delimiterIdx]), premineInfoRaw[delimiterIdx+1:]
	} else {
		// <addr>
		address = types.StringToAddress(premineInfoRaw)
	}

	amount, err := types.ParseUint256orHex(&val)
	if err != nil {
		return nil, fmt.Errorf("failed to parse amount %s: %w", val, err)
	}

	return &premineInfo{address: address, balance: amount}, nil
}

// matchValidatorDataDirs uses regexp to match validator directories which must have the last
// three digits: non-digit, hyphen(-), digit.
// Found paths are appended to validatorDataPaths.paths
func (v *validatorDataPaths) matchValidatorDataDirs(path string, info fs.FileInfo, _ error) error {
	if info.IsDir() && strings.HasPrefix(info.Name(), filepath.Base(v.pathPrefix)) {
		// match paths that have a number as the last digit /tmp/data-1, /tmp/test-data-2, etc.
		// matches any string that has last 3 chars: not-digit, - , digit
		match, _ := regexp.MatchString(`^.*\D-\d{1}$`, strings.TrimSpace(path))
		if match {
			v.paths = append(v.paths, path)
		}
	}

	return nil
}

// ReadValidatorsByRegexp trys to find validator secrets when local secrets manager is used.
// Validator folders must end with a hyphen (-) and a digit ( data-1, test-data-2, etc. ).
//
// Firstly the folder of genesis.json file will be searched for validator data folders.
// If validators data is not in the same folder with the genesis.json file, it will try to find
// validator data folders from the prefix, if it is defined as absolute path
func ReadValidatorsByRegexp(dir, prefix string) ([]*polybft.Validator, error) {
	if dir == "" {
		dir = "."
	}

	validatorPaths := validatorDataPaths{
		pathPrefix: prefix,
	}

	// check the root folder of genesis file for validator data
	if err := filepath.Walk(dir, validatorPaths.matchValidatorDataDirs); err != nil {
		return nil, fmt.Errorf("could not find validator on the designated path error=%w", err)
	}

	// check the absolute path of validator prefix
	// used to match when validator secrets folders are not in the same root as genesis.json file
	if err := filepath.Walk(path.Dir(prefix), validatorPaths.matchValidatorDataDirs); err != nil {
		return nil, fmt.Errorf("could not find validator on the designated path error=%w", err)
	}

	// we must sort files by number after the prefix not by name string
	// the last character is always a digit
	sort.Slice(validatorPaths.paths, func(i, j int) bool {
		num1, _ := strconv.Atoi(validatorPaths.paths[i][len(validatorPaths.paths[i])-1:])
		num2, _ := strconv.Atoi(validatorPaths.paths[j][len(validatorPaths.paths[j])-1:])

		return num1 < num2
	})

	validators := make([]*polybft.Validator, len(validatorPaths.paths))

	for i, valPath := range validatorPaths.paths {
		account, nodeID, err := getSecrets(valPath)
		if err != nil {
			return nil, err
		}

		validator := &polybft.Validator{
			Address: types.Address(account.Ecdsa.Address()),
			BlsKey:  hex.EncodeToString(account.Bls.PublicKey().Marshal()),
			NodeID:  nodeID,
		}
		validators[i] = validator
	}

	return validators, nil
}

func getSecrets(directory string) (*wallet.Account, string, error) {
	baseConfig := &secrets.SecretsManagerParams{
		Logger: hclog.NewNullLogger(),
		Extra: map[string]interface{}{
			secrets.Path: directory,
		},
	}

	localManager, err := local.SecretsManagerFactory(nil, baseConfig)
	if err != nil {
		return nil, "", fmt.Errorf("unable to instantiate local secrets manager, %w", err)
	}

	nodeID, err := helper.LoadNodeID(localManager)
	if err != nil {
		return nil, "", err
	}

	account, err := wallet.NewAccountFromSecret(localManager)
	if err != nil {
		return nil, "", err
	}

	return account, nodeID, nil
}
