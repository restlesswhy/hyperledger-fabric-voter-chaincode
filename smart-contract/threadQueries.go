/*
SPDX-License-Identifier: Apache-2.0
*/

package auction

import (
	"encoding/json"
	"fmt"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// Зегрузть сущность из блокчейна.
func (s *SmartContract) QueryThread(ctx contractapi.TransactionContextInterface, threadID string) (*Thread, error) {

	threadJSON, err := ctx.GetStub().GetState(threadID)
	if err != nil {
		return nil, fmt.Errorf("failed to get thread object %v: %v", threadID, err)
	}
	if threadJSON == nil {
		return nil, fmt.Errorf("thread does not exist")
	}

	var auction *Thread
	err = json.Unmarshal(threadJSON, &auction)
	if err != nil {
		return nil, err
	}

	return auction, nil
}

func (s *SmartContract) QueryAnonThread(ctx contractapi.TransactionContextInterface, threadID string) (*AnonThread, error) {

	threadJSON, err := ctx.GetStub().GetState(threadID)
	if err != nil {
		return nil, fmt.Errorf("failed to get thread object %v: %v", threadID, err)
	}
	if threadJSON == nil {
		return nil, fmt.Errorf("thread does not exist")
	}

	var thread *AnonThread
	err = json.Unmarshal(threadJSON, &thread)
	if err != nil {
		return nil, err
	}

	return thread, nil
}