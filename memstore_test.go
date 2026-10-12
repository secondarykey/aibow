package aibow

// memstore_test.go: テスト用の、メモリ上の保存先(Store の実装)。
// 会話の仕組みのテストを、DB ファイルなしで動かすために使う。

import (
	"slices"
	"sync"
	"uuid"
)

type memStore struct {
	mu       sync.Mutex
	convs    map[uuid.UUID]Conversation
	messages []StoredMessage
	settings map[string]string
}

func newMemStore() *memStore {
	return &memStore{convs: map[uuid.UUID]Conversation{}, settings: map[string]string{}}
}

func (s *memStore) ListConversations() ([]Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Conversation
	for _, c := range s.convs {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Conversation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

func (s *memStore) Conversation(id uuid.UUID) (Conversation, []StoredMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return Conversation{}, nil, ErrNotFound
	}
	var ms []StoredMessage
	for _, m := range s.messages {
		if m.ConversationID == id {
			ms = append(ms, m)
		}
	}
	slices.SortFunc(ms, func(a, b StoredMessage) int { return a.ID.Compare(b.ID) })
	return c, ms, nil
}

func (s *memStore) CreateConversation(c Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[c.ID] = c
	return nil
}

func (s *memStore) UpdateConversation(c Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[c.ID]; !ok {
		return ErrNotFound
	}
	s.convs[c.ID] = c
	return nil
}

func (s *memStore) DeleteConversation(id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.convs, id)
	s.messages = slices.DeleteFunc(s.messages, func(m StoredMessage) bool { return m.ConversationID == id })
	return nil
}

func (s *memStore) AddMessage(m StoredMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return nil
}

func (s *memStore) AppSetting(key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	return v, ok, nil
}

func (s *memStore) SetAppSetting(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}
