package auction

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

type AnonThread struct {
	Category    string              `json:"category"`
	Theme       string              `json:"theme"`
	Description string              `json:"description"`
	Creator     string              `json:"creator"`
	Votes       []string            `json:"votes"`
	Options     map[string][]string `json:"options"`
	WinOption   []string            `json:"win_option"`
	Status      string              `json:"status"`
}

type AnonVote struct {
	ThreadID   string `json:"thread_id"`
	TxID       string `json:"tx_id"`
	Option     string `json:"option"`
	PrivateKey string `json:"private_key"`
}

func (s *SmartContract) CreateAnonThread(ctx contractapi.TransactionContextInterface) error {

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

	tread := AnonThread{
		Category:    category,
		Theme:       theme,
		Description: description,
		Creator:     clientID,
		Votes:       []string{},
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

	err = ctx.GetStub().SetEvent(fmt.Sprintf("CreateAnonThread %s", threadID), threadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of creating thread: %v", err)
	}

	return nil
}

func (s *SmartContract) UseAnonVote(ctx contractapi.TransactionContextInterface) error {

	transientMap, err := ctx.GetStub().GetTransient()
	if err != nil {
		return fmt.Errorf("error getting transient: %v", err)
	}

	transientOptionJSON, ok := transientMap["option"]
	if !ok {
		return fmt.Errorf("bid key not found in the transient map")
	}

	vote := &AnonVote{}
	err = json.Unmarshal(transientOptionJSON, &vote)
	if err != nil {
		return fmt.Errorf("error unmarshal vote data transient: %v", err)
	}

	// get the tread from public state
	tread, err := s.QueryAnonThread(ctx, vote.ThreadID)
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
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{vote.ThreadID, vote.TxID, collection})
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
	if !contains(tread.Options, vote.Option) {
		return fmt.Errorf("failed to use vote: unexpected option")
	}

	hash := sha256.New()
	hash.Write(transientOptionJSON)
	calculatedVoteJSONHash := base64.URLEncoding.EncodeToString(hash.Sum(nil))

	// Добавляем голос к выбранному варианту
	tread.Votes = append(tread.Votes, calculatedVoteJSONHash)

	// Переводим в джсон обновленный тред
	newThreadJSON, err := json.Marshal(tread)
	if err != nil {
		return err
	}

	err = ctx.GetStub().PutState(vote.ThreadID, newThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to update auction: %v", err)
	}

	err = ctx.GetStub().SetEvent(fmt.Sprintf("UseAnonVote %s", vote.ThreadID), newThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of using vote: %v", err)
	}

	return nil
}

type EndData struct {
	ThreadID string   `json:"thread_id"`
	Keys     []string `json:"keys"`
	VoteTxs  []string `json:"vote_txs"`
}

func (s *SmartContract) EndAnonThread(ctx contractapi.TransactionContextInterface) error {

	transientMap, err := ctx.GetStub().GetTransient()
	if err != nil {
		return fmt.Errorf("error getting transient: %v", err)
	}

	transientEndDataJSON, ok := transientMap["option"]
	if !ok {
		return fmt.Errorf("bid key not found in the transient map")
	}

	endData := &EndData{}
	err = json.Unmarshal(transientEndDataJSON, &endData)
	if err != nil {
		return fmt.Errorf("error unmarshal vote data transient: %v", err)
	}

	// get thread from public state
	thread, err := s.QueryAnonThread(ctx, endData.ThreadID)
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
	for _, vote := range thread.Votes {
		for _, tx := range endData.VoteTxs {
			for _, key := range endData.Keys {
				for option := range thread.Options {
					anonVote := &AnonVote{
						ThreadID:   endData.ThreadID,
						TxID:       tx,
						Option:     option,
						PrivateKey: key,
					}

					b, _ := json.Marshal(anonVote)

					hash := sha256.New()
					hash.Write(b)
					calculatedVoteJSONHash := base64.URLEncoding.EncodeToString(hash.Sum(nil))

					if calculatedVoteJSONHash == vote {
						thread.Options[option] = append(thread.Options[option], "vote")
					}
				}
			}
		}
	}

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

	err = ctx.GetStub().PutState(endData.ThreadID, endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to end thread: %v", err)
	}

	err = ctx.GetStub().SetEvent(fmt.Sprintf("EndAnonThread %s", endData.ThreadID), endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of ending thread: %v", err)
	}

	return nil
}
