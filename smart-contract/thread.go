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

type IssuedVotes struct {
	Votes map[string]bool `json:"votes"`
}

const voteKeyType = "vote"

// Создает сущность публичного голосования по заданным параметрам. 
func (s *SmartContract) CreateThread(ctx contractapi.TransactionContextInterface) error {
	// Получаем параметры из аргументов.
	args := ctx.GetStub().GetStringArgs()
	if len(args) < 4 {
		return fmt.Errorf("not enough arguments")
	}

	threadID := args[1]
	category := args[2]
	theme := args[3]
	description := args[4]
	options := args[5:]

	// Проверяем, есть ли тред с заданным ID.
	threadJSON, err := ctx.GetStub().GetState(threadID)
	if err != nil {
		return fmt.Errorf("failed to get thread %v", err)
	} else if threadJSON != nil {
		return fmt.Errorf("failed to create, thread ID already exist")
	}

	// Получаем ID вызывающего транзакцию.
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Получаем ID организации вызывающего транзакцию
	clientOrgID, err := ctx.GetClientIdentity().GetMSPID()
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Создаем структуру голосвания
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

	threadJSON, err = json.Marshal(tread)
	if err != nil {
		return err
	}

	// Выгружаем голосовние в блокчейн
	err = ctx.GetStub().PutState(threadID, threadJSON)
	if err != nil {
		return fmt.Errorf("failed to put auction in public data: %v", err)
	}

	// Уствнавливаем организацию создателя как ендорсера над внечением изменений
	err = setAssetStateBasedEndorsement(ctx, threadID, clientOrgID)
	if err != nil {
		return fmt.Errorf("failed setting state based endorsement for new organization: %v", err)
	}

	// Загружаем в блокчейн пустое хранилище для голосов по определенному голосованию.
	votes := &IssuedVotes{
		Votes: make(map[string]bool),
	}

	votesJSON, err := json.Marshal(votes)
	if err != nil {
		return err
	}

	votesKey := threadID + "_votes"

	err = ctx.GetStub().PutState(votesKey, votesJSON)
	if err != nil {
		return fmt.Errorf("failed to put thread in public data: %v", err)
	}

	// Записываем ивент создания голосования.
	err = ctx.GetStub().SetEvent(fmt.Sprintf("CreateThread %s", threadID), threadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of creating thread: %v", err)
	}

	return nil
}

// Создает голос по определенному голосованию на определенного пользователя.
func (s *SmartContract) CreateVote(ctx contractapi.TransactionContextInterface, threadID string, userID string) (string, error) {
	// Получаем ID вызывающего транзакцию.
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get client identity %v", err)
	}

	// Получаем сущность голосования.
	thread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return "", fmt.Errorf("failed to get thread from public state %v", err)
	}

	// Проверям, действительно ли создаль голосвания создает к нему голоса.
	creator := thread.Creator
	if creator != clientID {
		return "", fmt.Errorf("vote of this thread can only be created by creator of thread: %v", err)
	}

	// Получаем ID организации.
	collection, err := getCollectionName(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get implicit collection name: %v", err)
	}

	// Получаем ID транзакции как уникальный индекс голоса.
	txID := ctx.GetStub().GetTxID()

	// Создаем композитный ключ для голоса.
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{threadID, txID, collection, userID})
	if err != nil {
		return "", fmt.Errorf("failed to create composite key: %v", err)
	}

	err = ctx.GetStub().PutState(voteKey, []byte("vote"))
	if err != nil {
		return "", fmt.Errorf("failed to input vote into collection: %v", err)
	}

	// Вписываем выданных голос в список выданных голосв
	votesKey := threadID + "_votes"
	votesJSON, err := ctx.GetStub().GetState(votesKey)
	if err != nil {
		return "", fmt.Errorf("failed to get votes from state: %v", err)
	}

	votes := &IssuedVotes{}
	err = json.Unmarshal(votesJSON, &votes)
	if err != nil {
		return "", err
	}

	votes.Votes[txID] = false

	votesJSON, err = json.Marshal(votes)
	if err != nil {
		return "", err
	}

	// Выгружаем список голосов в блокчейн 
	err = ctx.GetStub().PutState(votesKey, votesJSON)
	if err != nil {
		return "", fmt.Errorf("failed to input vote into collection: %v", err)
	}

	return txID, nil
}

// Применяет голос к сущности.
func (s *SmartContract) UseVote(ctx contractapi.TransactionContextInterface, threadID string, txID string, option string) error {
	
	// Загружаем сущность голосования из блокчейна.
	tread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("failed to get auction from public state %v", err)
	}
	
	// Проверяем, открытое ли голосование.
	Status := tread.Status
	if Status != "open" {
		return fmt.Errorf("cannot join closed or ended auction")
	}

	// Проверяем наличие и актуальность голоса.
	votesKey := threadID + "_votes"
	votesJSON, err := ctx.GetStub().GetState(votesKey)
	if err != nil {
		return fmt.Errorf("failed to get votes from state: %v", err)
	}

	votes := &IssuedVotes{}
	err = json.Unmarshal(votesJSON, &votes)
	if err != nil {
		return err
	}

	if vote, ok := votes.Votes[txID]; !ok {
		return fmt.Errorf("this vote does no exist")
	} else if vote {
		return fmt.Errorf("this vote already used")
	}

	// Получаем ID организации.
	collection, err := getCollectionName(ctx)
	if err != nil {
		return fmt.Errorf("failed to get implicit collection name: %v", err)
	}

	// Получаем ID пользователя
	userID, _, err := ctx.GetClientIdentity().GetAttributeValue("hf.EnrollmentID")
	if err != nil {
		return err
	}

	// Создаем композитный ключ.
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{threadID, txID, collection, userID})
	if err != nil {
		return fmt.Errorf("failed to create composite key: %v", err)
	}

	// Проверяем, существует ли голос по заданному ключу.
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

	// Добавляем голос к выбранному варианту и загружаем в блокчейн.
	tread.Options[option] = append(tread.Options[option], userID)

	threadJSON, err := json.Marshal(tread)
	if err != nil {
		return err
	}

	err = ctx.GetStub().PutState(threadID, threadJSON)
	if err != nil {
		return fmt.Errorf("failed to update auction: %v", err)
	}

	// Обновляем состояние голоса и выгружаем его в блокчейн.
	votes.Votes[txID] = true
	votesJSON, err = json.Marshal(votes)
	if err != nil {
		return err
	}

	err = ctx.GetStub().PutState(votesKey, votesJSON)
	if err != nil {
		return fmt.Errorf("failed to update auction: %v", err)
	}

	// Отправляем ивент о оспользовании голоса
	err = ctx.GetStub().SetEvent(fmt.Sprintf("UseVote %s", threadID), threadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of using vote: %v", err)
	}

	return nil
}

// Подводит итоги и обьявляет выигрышный вариант/варианты в голосовании.
func (s *SmartContract) EndThread(ctx contractapi.TransactionContextInterface, threadID string) error {

	// Получает сущность голосования.
	thread, err := s.QueryThread(ctx, threadID)
	if err != nil {
		return fmt.Errorf("failed to get thread from public state %v", err)
	}

	// Получаем ID вызывающего транзакцию.
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Проверяем, является ли вызывающий создателем голосования.
	endPerson := thread.Creator
	if endPerson != clientID {
		return fmt.Errorf("thread can only be ended by creator: %v", err)
	}

	// Проверяем, не закрыто ли голосовние уже.
	status := thread.Status
	if status != "open" {
		return fmt.Errorf("cannot close thread that is not open")
	}

	thread.Status = string("closed")

	// Определяем победителя/победителей.
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

	// Записываем победителя и выгружаем в блокчейн.
	thread.WinOption = winOptions
	thread.Status = string("ended")

	endedThreadJSON, _ := json.Marshal(thread)

	err = ctx.GetStub().PutState(threadID, endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to end thread: %v", err)
	}

	// Записываем ивент о закрытии голосования.
	err = ctx.GetStub().SetEvent(fmt.Sprintf("EndThread %s", threadID), endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of ending thread: %v", err)
	}

	return nil
}
