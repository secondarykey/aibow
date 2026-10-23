package aibow

import (
	"testing"
	"time"
	"uuid"
)

// ほかの人の会話を写すと、発言・要約・「モデルに渡さない」の印も写り、自分の会話になる(0018 の決定事項 2・26)
func TestCopyShared(t *testing.T) {
	cs, st := newTestConversations(t, &fakeGen{})
	src, err := cs.Create(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	src.Title = "調べもの"
	src.Settings.WorkDir = "C:/work"
	st.UpdateConversation(ctx, src)
	now := time.Now()
	var msgs []StoredMessage
	for i, text := range []string{"質問1", "答え1", "質問2"} {
		role := "user"
		if i == 1 {
			role = "assistant"
		}
		m := StoredMessage{ID: uuid.NewV7(), ConversationID: src.ID, Role: role, Content: text, CreatedAt: now}
		if err := st.AddMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	st.SetMessagesExcluded(ctx, []uuid.UUID{msgs[1].ID}, true)
	if err := st.AddCompaction(ctx, Compaction{ID: uuid.NewV7(), ConversationID: src.ID, UptoMessageID: msgs[1].ID, Summary: "要約", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	c, err := cs.CopyShared(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == src.ID || c.Title != "調べもの(写し)" || c.Settings.WorkDir != "" || c.ProfileID == (uuid.UUID{}) {
		t.Errorf("写した会話: %+v", c)
	}
	_, got, err := st.Conversation(ctx, c.ID)
	if err != nil || len(got) != 3 || got[0].Content != "質問1" || got[2].Content != "質問2" || !got[1].Excluded || got[0].Excluded {
		t.Fatalf("写した発言: %+v %v", got, err)
	}
	comps, _ := st.Compactions(ctx, c.ID)
	if len(comps) != 1 || comps[0].UptoMessageID != got[1].ID || comps[0].Summary != "要約" {
		t.Errorf("写した要約: %+v", comps)
	}
}
