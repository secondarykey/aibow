package aibow

// memstore_test.go: テスト用の、メモリ上の保存先(Store の実装)。
// 会話の仕組みのテストを、DB ファイルなしで動かすために使う。
// 利用者は1人として扱い、ctx は見ない(持ち主で分けることは、store パッケージの SQLite のテストで確かめる)。

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"
	"uuid"
)

type memStore struct {
	mu          sync.Mutex
	convs       map[uuid.UUID]Conversation
	messages    []StoredMessage
	compactions []Compaction
	profiles    []Profile
	settings    map[string]string // アプリ全体の設定
	user        map[string]string // 人ごとの設定
}

func newMemStore() *memStore {
	return &memStore{convs: map[uuid.UUID]Conversation{}, settings: map[string]string{}, user: map[string]string{}}
}

func (s *memStore) ListProfiles(_ context.Context) ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := slices.Clone(s.profiles)
	slices.SortFunc(out, func(a, b Profile) int { return a.ID.Compare(b.ID) })
	return out, nil
}

func (s *memStore) Profile(_ context.Context, id uuid.UUID) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := slices.IndexFunc(s.profiles, func(p Profile) bool { return p.ID == id }); i >= 0 {
		return s.profiles[i], nil
	}
	return Profile{}, ErrProfileNotFound
}

func (s *memStore) CreateProfile(_ context.Context, p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.profiles = append(s.profiles, p)
	return nil
}

func (s *memStore) UpdateProfile(_ context.Context, p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.IndexFunc(s.profiles, func(q Profile) bool { return q.ID == p.ID })
	if i < 0 {
		return ErrProfileNotFound
	}
	s.profiles[i] = p
	return nil
}

// DeleteProfile は SQLite と同じく、使っている会話があれば消さない(外部キー)。
func (s *memStore) DeleteProfile(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.convs {
		if c.ProfileID == id {
			return ErrProfileInUse
		}
	}
	n := len(s.profiles)
	s.profiles = slices.DeleteFunc(s.profiles, func(p Profile) bool { return p.ID == id })
	if len(s.profiles) == n {
		return ErrProfileNotFound
	}
	return nil
}

func (s *memStore) ListConversations(_ context.Context) ([]Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Conversation
	for _, c := range s.convs {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Conversation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return out, nil
}

func (s *memStore) Conversation(_ context.Context, id uuid.UUID) (Conversation, []StoredMessage, error) {
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

func (s *memStore) CreateConversation(_ context.Context, c Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.convs[c.ID] = c
	return nil
}

func (s *memStore) UpdateConversation(_ context.Context, c Conversation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.convs[c.ID]; !ok {
		return ErrNotFound
	}
	s.convs[c.ID] = c
	return nil
}

func (s *memStore) TouchConversation(_ context.Context, id uuid.UUID, at time.Time, autoTitle string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.convs[id]
	if !ok {
		return ErrNotFound
	}
	c.UpdatedAt = at
	if c.Title == "" {
		c.Title = autoTitle
	}
	s.convs[id] = c
	return nil
}

func (s *memStore) DeleteConversation(_ context.Context, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.convs, id)
	s.messages = slices.DeleteFunc(s.messages, func(m StoredMessage) bool { return m.ConversationID == id })
	s.compactions = slices.DeleteFunc(s.compactions, func(c Compaction) bool { return c.ConversationID == id })
	return nil
}

func (s *memStore) AddCompaction(_ context.Context, c Compaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.compactions = append(s.compactions, c)
	return nil
}

func (s *memStore) Compactions(_ context.Context, conv uuid.UUID) ([]Compaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Compaction{}
	for _, c := range s.compactions {
		if c.ConversationID == conv {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Compaction) int { return a.ID.Compare(b.ID) })
	return out, nil
}

func (s *memStore) AddMessage(_ context.Context, m StoredMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return nil
}

func (s *memStore) DeleteMessages(_ context.Context, ids []uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = slices.DeleteFunc(s.messages, func(m StoredMessage) bool { return slices.Contains(ids, m.ID) })
	return nil
}

func (s *memStore) SetMessagesExcluded(_ context.Context, ids []uuid.UUID, excluded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.messages {
		if slices.Contains(ids, s.messages[i].ID) {
			s.messages[i].Excluded = excluded
		}
	}
	return nil
}

func (s *memStore) AppSetting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	return v, ok, nil
}

func (s *memStore) SetAppSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings[key] = value
	return nil
}

func (s *memStore) UserSetting(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.user[key]
	return v, ok, nil
}

func (s *memStore) SetUserSetting(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.user[key] = value
	return nil
}

// ctx は、テストで会話の仕組みと保存先に渡す ctx(利用者はログインなしの形の利用者)。
// 止めることを確かめるテストは、自分で作った ctx を使う(その中では、この名前はそちらを指す)。
var ctx = WithUser(context.Background(), LocalUser)

// mustDefaults は、新しい会話の既定(cs.Defaults)。読めなければテストを止める。
func mustDefaults(t *testing.T, cs *Conversations) ConversationSettings {
	t.Helper()
	d, err := cs.Defaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// mustDefaultProfile は、新しい会話で最初に選ばれるプロファイル(cs.DefaultProfile)。読めなければテストを止める。
func mustDefaultProfile(t *testing.T, cs *Conversations) Profile {
	t.Helper()
	p, err := cs.DefaultProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
