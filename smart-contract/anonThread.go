package auction

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/hyperledger/fabric-contract-api-go/contractapi"
)

// Сущность анонимного голосования
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

// Создает анонимное голосование.
func (s *SmartContract) CreateAnonThread(ctx contractapi.TransactionContextInterface) error {
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

	// Запрашиваем голосование по ID из блокчейна, тем самым проверяем, не существует ли уже голосвание с данным ID.
	res, err := ctx.GetStub().GetState(threadID)
	if err != nil {
		return fmt.Errorf("failed to get thread %v", err)
	} else if res != nil {
		return fmt.Errorf("failed to create, thread ID already exist")
	}

	// Получаем ID вызывающего.
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Получаем ID организации вызывающего.
	clientOrgID, err := ctx.GetClientIdentity().GetMSPID()
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Создаем сущность голосвания и заполняем ее параметрами.
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

	// Выгружаем голосование в блокчейн.
	err = ctx.GetStub().PutState(threadID, threadJSON)
	if err != nil {
		return fmt.Errorf("failed to put auction in public data: %v", err)
	}

	// Устанавливаем организацию вызыввающего как ендорсера.
	err = setAssetStateBasedEndorsement(ctx, threadID, clientOrgID)
	if err != nil {
		return fmt.Errorf("failed setting state based endorsement for new organization: %v", err)
	}

	// Отправляем ивент о создании голосования.
	err = ctx.GetStub().SetEvent(fmt.Sprintf("CreateAnonThread %s", threadID), threadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of creating thread: %v", err)
	}

	return nil
}

type AnonVote struct {
	ThreadID   string `json:"thread_id"`
	TxID       string `json:"tx_id"`
	Option     string `json:"option"`
	PrivateKey string `json:"private_key"`
}

// Принимает анонимный голос и добавляет хэш этого голоса к сущности.
func (s *SmartContract) UseAnonVote(ctx contractapi.TransactionContextInterface) error {

	// Получаем из запроса зашифрованные данные
	transientMap, err := ctx.GetStub().GetTransient()
	if err != nil {
		return fmt.Errorf("error getting transient: %v", err)
	}

	transientOptionJSON, ok := transientMap["option"]
	if !ok {
		return fmt.Errorf("option key not found in the transient map")
	}

	vote := &AnonVote{}
	err = json.Unmarshal(transientOptionJSON, &vote)
	if err != nil {
		return fmt.Errorf("error unmarshal vote data transient: %v", err)
	}

	// Получаем сущность голосования из блокчейна.
	tread, err := s.QueryAnonThread(ctx, vote.ThreadID)
	if err != nil {
		return fmt.Errorf("failed to get auction from public state %v", err)
	}

	// Проверяем, открыто ли голосование.
	Status := tread.Status
	if Status != "open" {
		return fmt.Errorf("cannot join closed or ended auction")
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

	// Создаем композитный ключ для поиска голоса в блокчейне.
	voteKey, err := ctx.GetStub().CreateCompositeKey(voteKeyType, []string{vote.ThreadID, vote.TxID, collection, userID})
	if err != nil {
		return fmt.Errorf("failed to create composite key: %v", err)
	}

	// Проверяем, существует ли голос по заданному ключу, и не использован ли он.
	data, err := ctx.GetStub().GetState(voteKey)
	if err != nil {
		return fmt.Errorf("failed to get vote: %v", err)
	}
	if data == nil {
		return fmt.Errorf("vote does not exist: %s", data)
	} else if len(data) == 5 {
		return fmt.Errorf("vote already used")
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

	// Обновляем статус голоса.
	status, _ := json.Marshal(false)
	err = ctx.GetStub().PutState(voteKey, status)
	if err != nil {
		return fmt.Errorf("failed to update auction: %v", err)
	}

	// Записываем ивент о использовании голоса.
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

// Завершает голосование, расшифровывает голоса и определяет выйгрышный вариант/варианты.
func (s *SmartContract) EndAnonThread(ctx contractapi.TransactionContextInterface) error {
	// Получаем зашифрованные данные.
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

	// Загружаем сущность голосования из блокчейна.
	thread, err := s.QueryAnonThread(ctx, endData.ThreadID)
	if err != nil {
		return fmt.Errorf("failed to get thread from public state %v", err)
	}

	// Получаем ID вызывающего.
	clientID, err := s.GetSubmittingClientIdentity(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client identity %v", err)
	}

	// Проверяем что вызывающий - автор голосования.
	endPerson := thread.Creator
	if endPerson != clientID {
		return fmt.Errorf("thread can only be ended by creator: %v", err)
	}

	// Если статус не "open" значит голосование уже закрыто.
	status := thread.Status
	if status != "open" {
		return fmt.Errorf("cannot close thread that is not open")
	}

	thread.Status = string("closed")

	// Разгадываем хэшт голосов и распределяем варианты.
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

	// Определяем выйгравший вариант/варианты.
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

	endedThreadJSON, _ := json.Marshal(thread)

	// Выгружаем измененное голосование в блокчейн.
	err = ctx.GetStub().PutState(endData.ThreadID, endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to end thread: %v", err)
	}

	// Записываем ивент о завершении голосования.
	err = ctx.GetStub().SetEvent(fmt.Sprintf("EndAnonThread %s", endData.ThreadID), endedThreadJSON)
	if err != nil {
		return fmt.Errorf("failed to set event of ending thread: %v", err)
	}

	return nil
}
