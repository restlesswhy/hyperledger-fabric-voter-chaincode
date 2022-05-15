/*
SPDX-License-Identifier: Apache-2.0
*/

package auction

import (
	"encoding/json"
	"fmt"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type SmartContract struct {
	contractapi.Contract
}

// Thread data
type Thread struct {
	Category    string              `json:"category"`
	Theme       string              `json:"theme"`
	Description string              `json:"description"`
	Creator     string              `json:"creator"`
	Options     map[string][]string `json:"options"`
	WinOption   []string            `json:"win_option"`
	Status      string              `json:"status"`
}

const voteKeyType = "vote"

// CreateAuction creates on auction on the public channel. The identity that
// submits the transacion becomes the seller of the auction
func (s *SmartContract) CreateThread(ctx contractapi.TransactionContextInterface) error {

	args := ctx.GetStub().GetStringArgs()
	if len(args) < 4 {
		return fmt.Errorf("not enough arguments")
	}

	threadID := args[1]
	category := args[2]
	theme := args[3]
	description := args[4]
	options := args[5:]

	res, err := ctx.GetStub().GetState(threadID)
	if err != nil {
		return fmt.Errorf("failed to get thread %v", err)
	} else if res != nil {
		return fmt.Errorf("failed to create, thread ID already exist")
	}

	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// get org of submitting client
	clientOrgID, err := ctx.GetClientIdentity().GetMSPID()
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Create tread
	threadOptions := make(map[string][]string)

	for _, option := range options {
		threadOptions[option] = make([]string, 0)
	}

	tread := Thread{
		Category:    category,
		Theme:       theme,
		Description: description,
		Creator:     clientID,
		Options:     threadOptions,
		WinOption:   []string{},
		Status:      "open",
	}

	threadJSON, err := json.Marshal(tread)
	if err != nil {
		return err
	}

	// put auction into state
	err = ctx.GetStub().PutState(threadID, threadJSON)
	if err != nil {
		return fmt.Errorf("failed to put auction in public data: %v", err)
	}

	// set the creator of the tread as an endorser
	err = setAssetStateBasedEndorsement(ctx, threadID, clientOrgID)
	if err != nil {
		return fmt.Errorf("failed setting state based endorsement for new organization: %v", err)
	}

	err = ctx.GetStub().SetEvent(fmt.Sprintf("CreateThread %s", threadID), threadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of creating thread: %v", err)
	}

	return nil
}

// Bid is used to add a user's bid to the auction. The bid is stored in the private
// data collection on the peer of the bidder's organization. The function returns
// the transaction ID so that users can identify and query their bid
func (s *SmartContract) CreateVote(ctx contractapi.TransactionContextInterface, threadID string) (string, error) {

	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get client identity %v", err)
	}

	thread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return "", fmt.Errorf("failed to get thread from public state %v", err)
	}

	creator := thread.Creator
	if creator != clientID {
		return "", fmt.Errorf("vote of this thread can only be created by creator of thread: %v", err)
	}

	// get the implicit collection name using the voter organization ID
	collection, err := getCollectionName(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get implicit collection name: %v", err)
	}

	// the transaction ID is used as a unique index for the vote
	txID := ctx.GetStub().GetTxID()

	// create a composite key using the transaction ID
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{threadID, txID, collection})
	if err != nil {
		return "", fmt.Errorf("failed to create composite key: %v", err)
	}

	err = ctx.GetStub().PutState(voteKey, []byte("vote"))
	if err != nil {
		return "", fmt.Errorf("failed to input vote into collection: %v", err)
	}

	return txID, nil
}

// SubmitBid is used by the bidder to add the hash of that bid stored in private data to the
// auction. Note that this function alters the auction in private state, and needs
// to meet the auction endorsement policy. Transaction ID is used identify the bid
func (s *SmartContract) UseVote(ctx contractapi.TransactionContextInterface, threadID string, txID string, option string) error {

	// get the tread from public state
	tread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("failed to get auction from public state %v", err)
	}

	// tread needs to be open for users to add their vote
	Status := tread.Status
	if Status != "open" {
		return fmt.Errorf("cannot join closed or ended auction")
	}

	// get the inplicit collection name of voter org
	collection, err := getCollectionName(ctx)
	if err != nil {
		return fmt.Errorf("failed to get implicit collection name: %v", err)
	}

	// что если ключ будет привязан к никнейму подписанта
	// use the transaction ID passed as a parameter to create composite vote key
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{threadID, txID, collection})
	if err != nil {
		return fmt.Errorf("failed to create composite key: %v", err)
	}

	data, err := ctx.GetStub().GetState(voteKey)
	if err != nil {
		return fmt.Errorf("failed to get vote: %v", err)
	}
	if data == nil {
		return fmt.Errorf("vote does not exist: %s", data)
	}

	// Проверка наличия варианта в вариантах ответа
	if !contains(tread.Options, option) {
		return fmt.Errorf("failed to use vote: unexpected option")
	}

	// Получаем айди пользователя
	userID, _, err := ctx.GetClientIdentity().GetAttributeValue("hf.EnrollmentID")
	if err != nil {
		return err
	}

	// Добавляем голос к выбранному варианту
	tread.Options[option] = append(tread.Options[option], userID)

	// Переводим в джсон обновленный тред
	newThreadJSON, err := json.Marshal(tread)
	if err != nil {
		return err
	}

	err = ctx.GetStub().PutState(threadID, newThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to update auction: %v", err)
	}

	err = ctx.GetStub().SetEvent(fmt.Sprintf("UseVote %s", threadID), newThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of using vote: %v", err)
	}

	return nil
}

// EndAuction both changes the auction status to closed and calculates the winners
// of the auction
func (s *SmartContract) EndThread(ctx contractapi.TransactionContextInterface, threadID string) error {

	// get thread from public state
	thread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("failed to get thread from public state %v", err)
	}

	// get username of submitting client
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	endPerson := thread.Creator
	if endPerson != clientID {
		return fmt.Errorf("thread can only be ended by creator: %v", err)
	}

	status := thread.Status
	if status != "open" {
		return fmt.Errorf("cannot close thread that is not open")
	}

	thread.Status = string("closed")

	voteAmount := 0
	winOptions := make([]string, 0)
	for k, v := range thread.Options {
		if len(v) > voteAmount {
			winOptions = append(winOptions, k)
			winOptions = winOptions[len(winOptions)-1:]

			voteAmount = len(v)
		} else if len(v) == voteAmount {
			winOptions = append(winOptions, k)
		}
	}

	thread.WinOption = winOptions
	thread.Status = string("ended")

	endedThreadJSON, _ := json.Marshal(thread)

	err = ctx.GetStub().PutState(threadID, endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to end thread: %v", err)
	}

	err = ctx.GetStub().SetEvent(fmt.Sprintf("EndThread %s", threadID), endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of ending thread: %v", err)
	}

	return nil
}
