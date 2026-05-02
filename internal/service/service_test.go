package service_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWA struct {
	status        waclient.Status
	resumeErr     error
	loginQR       <-chan waclient.QREvent
	loginQRErr    error
	loginPhone    <-chan waclient.PairEvent
	loginPhoneErr error
	loginPhoneArg string
	logoutErr     error
	closed        bool
	incoming      func(waclient.IncomingMessage)
}

func (f *fakeWA) Status() waclient.Status      { return f.status }
func (f *fakeWA) Resume(context.Context) error { return f.resumeErr }
func (f *fakeWA) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return f.loginQR, f.loginQRErr
}
func (f *fakeWA) LoginPhone(_ context.Context, n string) (<-chan waclient.PairEvent, error) {
	f.loginPhoneArg = n
	return f.loginPhone, f.loginPhoneErr
}
func (f *fakeWA) Logout(context.Context) error { return f.logoutErr }
func (f *fakeWA) Close() error                 { f.closed = true; return nil }
func (f *fakeWA) SendText(context.Context, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) OnIncomingMessage(h func(waclient.IncomingMessage)) {
	f.incoming = h
}
func (f *fakeWA) SendMedia(context.Context, string, string, string, string, string, []byte) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}

func TestStatusPassThrough(t *testing.T) {
	jid := "27821234567@s.whatsapp.net"
	now := time.Now()
	f := &fakeWA{status: waclient.Status{Connected: true, JID: &jid, Since: &now}}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)

	got, err := s.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, got.Connected)
	assert.Equal(t, &jid, got.JID)
}

func TestLoginQRPassThrough(t *testing.T) {
	ch := make(chan waclient.QREvent)
	f := &fakeWA{loginQR: ch}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)

	got, err := s.LoginQR(context.Background())
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.QREvent)(ch), got)
}

func TestLoginQRError(t *testing.T) {
	f := &fakeWA{loginQRErr: waclient.ErrAlreadyLoggedIn}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)
	_, err := s.LoginQR(context.Background())
	assert.ErrorIs(t, err, waclient.ErrAlreadyLoggedIn)
}

func TestLoginPhoneRejectsBadNumber(t *testing.T) {
	f := &fakeWA{}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)
	_, err := s.LoginPhone(context.Background(), "27821234567")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phone number")
	assert.Empty(t, f.loginPhoneArg, "fake should not be called")
}

func TestLoginPhonePassThrough(t *testing.T) {
	ch := make(chan waclient.PairEvent)
	f := &fakeWA{loginPhone: ch}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)
	got, err := s.LoginPhone(context.Background(), "+27821234567")
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.PairEvent)(ch), got)
	assert.Equal(t, "+27821234567", f.loginPhoneArg)
}

func TestLogoutPassThrough(t *testing.T) {
	f := &fakeWA{logoutErr: errors.New("boom")}
	s := service.New(f, store.Bundle{}, mediastore.New(t.TempDir()), nil)
	err := s.Logout(context.Background())
	assert.ErrorContains(t, err, "boom")
}

// inMemoryBundle returns a store.Bundle whose interfaces are backed by simple
// in-memory maps. Sufficient for service-level tests; not thread-safe.
type memChats map[string]store.Chat
type memMessages map[string]store.Message
type memContacts map[string]store.Contact

func newInMemoryBundle() (store.Bundle, *memChats, *memMessages, *memContacts) {
	c := memChats{}
	m := memMessages{}
	co := memContacts{}
	return store.Bundle{
		Chats:    &chatStore{m: c},
		Messages: &messageStore{m: m},
		Contacts: &contactStore{m: co},
		Media:    &mediaStore{},
		Events:   &eventsStore{},
		KV:       &kvStore{m: map[string]string{}},
	}, &c, &m, &co
}

type chatStore struct{ m memChats }

func (s *chatStore) Put(_ context.Context, c store.Chat) error { s.m[c.JID] = c; return nil }
func (s *chatStore) Get(_ context.Context, jid string) (store.Chat, error) {
	c, ok := s.m[jid]
	if !ok {
		return store.Chat{}, store.ErrNotFound
	}
	return c, nil
}
func (s *chatStore) List(_ context.Context, before time.Time, limit int, includeArchived bool) ([]store.Chat, error) {
	var out []store.Chat
	for _, c := range s.m {
		if !includeArchived && c.Archived {
			continue
		}
		if !before.IsZero() && (c.LastMsgAt.IsZero() || !c.LastMsgAt.Before(before)) {
			continue
		}
		out = append(out, c)
	}
	// sort by last_msg_at DESC, jid ASC for stability
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastMsgAt.Equal(out[j].LastMsgAt) {
			return out[i].LastMsgAt.After(out[j].LastMsgAt)
		}
		return out[i].JID < out[j].JID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *chatStore) SetArchived(context.Context, string, bool) error { return nil }
func (s *chatStore) Count(context.Context) (int, error) { return len(s.m), nil }
func (s *chatStore) TotalUnread(context.Context) (int, error) {
	total := 0
	for _, c := range s.m {
		total += c.UnreadCount
	}
	return total, nil
}

type messageStore struct{ m memMessages }

func (s *messageStore) Put(_ context.Context, msg store.Message) error { s.m[msg.ID] = msg; return nil }
func (s *messageStore) Get(_ context.Context, id string) (store.Message, error) {
	msg, ok := s.m[id]
	if !ok {
		return store.Message{}, store.ErrNotFound
	}
	return msg, nil
}
func (s *messageStore) ListByChat(_ context.Context, chatJID string, limit int, beforeTS time.Time) ([]store.Message, error) {
	var out []store.Message
	for _, m := range s.m {
		if m.ChatJID != chatJID {
			continue
		}
		if m.DeletedAt != nil {
			continue
		}
		if !beforeTS.IsZero() && !m.Timestamp.Before(beforeTS) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *messageStore) Search(_ context.Context, query string, limit int) ([]store.Message, error) {
	q := strings.ToLower(query)
	var out []store.Message
	for _, m := range s.m {
		if m.DeletedAt != nil {
			continue
		}
		if strings.Contains(strings.ToLower(m.Body), q) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *messageStore) SoftDelete(context.Context, string, time.Time) error { return nil }
func (s *messageStore) Count(context.Context) (int, error) {
	n := 0
	for _, m := range s.m {
		if m.DeletedAt == nil {
			n++
		}
	}
	return n, nil
}

type contactStore struct{ m memContacts }

func (s *contactStore) Put(_ context.Context, c store.Contact) error { s.m[c.JID] = c; return nil }
func (s *contactStore) Get(_ context.Context, jid string) (store.Contact, error) {
	c, ok := s.m[jid]
	if !ok {
		return store.Contact{}, store.ErrNotFound
	}
	return c, nil
}
func (s *contactStore) List(_ context.Context) ([]store.Contact, error) {
	var out []store.Contact
	for _, c := range s.m {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	return out, nil
}
func (s *contactStore) Count(context.Context) (int, error) { return len(s.m), nil }
func (s *contactStore) Search(_ context.Context, query string, limit int) ([]store.Contact, error) {
	q := strings.ToLower(query)
	var out []store.Contact
	for _, c := range s.m {
		if strings.Contains(strings.ToLower(c.PushName), q) ||
			strings.Contains(strings.ToLower(c.FullName), q) ||
			strings.Contains(strings.ToLower(c.BusinessName), q) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JID < out[j].JID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

type mediaStore struct {
	m map[string]store.MediaRef
}

func (s *mediaStore) Put(_ context.Context, ref store.MediaRef) error {
	if s.m == nil {
		s.m = map[string]store.MediaRef{}
	}
	s.m[ref.MessageID] = ref
	return nil
}
func (s *mediaStore) GetByMessageID(_ context.Context, id string) (store.MediaRef, error) {
	ref, ok := s.m[id]
	if !ok {
		return store.MediaRef{}, store.ErrNotFound
	}
	return ref, nil
}

type eventsStore struct{}

func (s *eventsStore) Append(context.Context, store.EventLogEntry) (int64, error) { return 0, nil }
func (s *eventsStore) SinceSeq(context.Context, int64, int) ([]store.EventLogEntry, error) {
	return nil, nil
}

type kvStore struct{ m map[string]string }

func (s *kvStore) Get(_ context.Context, k string) (string, error) {
	v, ok := s.m[k]
	if !ok {
		return "", store.ErrNotFound
	}
	return v, nil
}
func (s *kvStore) Set(_ context.Context, k, v string) error { s.m[k] = v; return nil }
func (s *kvStore) Delete(_ context.Context, k string) error { delete(s.m, k); return nil }

type sendableFakeWA struct {
	fakeWA
	sentArgs   [3]string // chat, text, sender (sender filled by SendText)
	sendResp   waclient.Sent
	sendErr    error
	calledSend bool
}

func (f *sendableFakeWA) SendText(_ context.Context, chatJID, text string) (waclient.Sent, error) {
	f.calledSend = true
	f.sentArgs[0] = chatJID
	f.sentArgs[1] = text
	return f.sendResp, f.sendErr
}

// mediaSenderFakeWA captures SendMedia args.
type mediaSenderFakeWA struct {
	sendableFakeWA
	mediaResp    waclient.Sent
	mediaErr     error
	gotMediaArgs [4]string // chatJID, kind, caption, filename
	gotMediaMIME string
	gotMediaBody []byte
}

func (f *mediaSenderFakeWA) SendMedia(_ context.Context, chatJID, kind, caption, filename, mime string, body []byte) (waclient.Sent, error) {
	f.gotMediaArgs[0] = chatJID
	f.gotMediaArgs[1] = kind
	f.gotMediaArgs[2] = caption
	f.gotMediaArgs[3] = filename
	f.gotMediaMIME = mime
	f.gotMediaBody = body
	return f.mediaResp, f.mediaErr
}

func TestSendTextSuccess(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _ := newInMemoryBundle()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.SendText(ctx, "27821234567@s.whatsapp.net", "hello")
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "hello", got.Body)
	assert.Equal(t, "text", got.Kind)
	assert.Equal(t, "me@s.whatsapp.net", got.SenderJID)
	assert.True(t, got.Timestamp.Equal(now))

	// Persistence side effects:
	require.Contains(t, *msgs, "MID1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
	chat := (*chats)["27821234567@s.whatsapp.net"]
	assert.True(t, chat.LastMsgAt.Equal(now))
	assert.Equal(t, "user", chat.Kind)
}

func TestSendTextValidation(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct{ chat, text, expect string }{
		{"", "hello", "chat_jid"},
		{"a@s.whatsapp.net", "", "text"},
		{"a@s.whatsapp.net", strings.Repeat("x", 4097), "text"},
	}
	for _, tc := range cases {
		t.Run(tc.expect, func(t *testing.T) {
			_, err := s.SendText(ctx, tc.chat, tc.text)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
			assert.False(t, wa.calledSend, "fake WA must not be called on validation failure")
		})
	}
}

func TestSendTextNotConnected(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{sendErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.SendText(ctx, "a@s.whatsapp.net", "hi")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestSendTextPersistFailureStillSucceeds(t *testing.T) {
	// Use a bundle whose Messages.Put errors but Chats.Put works.
	failMsgs := &failingMessageStore{}
	bundle := store.Bundle{
		Chats:    &chatStore{m: memChats{}},
		Messages: failMsgs,
		Contacts: &contactStore{m: memContacts{}},
		Media:    &mediaStore{},
		Events:   &eventsStore{},
		KV:       &kvStore{m: map[string]string{}},
	}
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID2", Timestamp: time.Now(), SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.SendText(context.Background(), "a@s.whatsapp.net", "hello")
	require.NoError(t, err) // persistence failure is logged, not returned
	assert.Equal(t, "MID2", got.ID)
	assert.True(t, failMsgs.called)
}

type failingMessageStore struct {
	called bool
}

func (f *failingMessageStore) Put(context.Context, store.Message) error {
	f.called = true
	return errors.New("boom")
}
func (f *failingMessageStore) Get(context.Context, string) (store.Message, error) {
	return store.Message{}, store.ErrNotFound
}
func (f *failingMessageStore) ListByChat(context.Context, string, int, time.Time) ([]store.Message, error) {
	return nil, nil
}
func (f *failingMessageStore) Search(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *failingMessageStore) SoftDelete(context.Context, string, time.Time) error { return nil }
func (f *failingMessageStore) Count(context.Context) (int, error)                  { return 0, nil }

func TestSendTextPreservesUnreadCount(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _ := newInMemoryBundle()
	(*chats)["chat@s.whatsapp.net"] = store.Chat{
		JID: "chat@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}

	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: time.Unix(1000, 0).UTC(), SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.SendText(ctx, "chat@s.whatsapp.net", "hi")
	require.NoError(t, err)

	// unread_count must be preserved at 5 — sending should not reset it.
	assert.Equal(t, 5, (*chats)["chat@s.whatsapp.net"].UnreadCount)
}

func TestHandleIncomingNewChat(t *testing.T) {
	bundle, chats, msgs, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil) // registers s.handleIncoming with wa

	require.NotNil(t, wa.incoming, "service.New must register an incoming handler")

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN1",
		ChatJID:   "27821234567@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "27821234567@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "text",
		Body:      "hi from phone",
		PushName:  "Alice",
	})

	require.Contains(t, *msgs, "MIN1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
	chat := (*chats)["27821234567@s.whatsapp.net"]
	assert.Equal(t, 1, chat.UnreadCount)
	assert.Equal(t, "user", chat.Kind)

	require.Contains(t, *contacts, "27821234567@s.whatsapp.net")
	assert.Equal(t, "Alice", (*contacts)["27821234567@s.whatsapp.net"].PushName)
}

func TestHandleIncomingExistingChat(t *testing.T) {
	bundle, chats, _, _ := newInMemoryBundle()
	(*chats)["chat@s.whatsapp.net"] = store.Chat{
		JID: "chat@s.whatsapp.net", Kind: "user", UnreadCount: 3,
	}
	wa := &sendableFakeWA{}
	service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN2",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(2000, 0).UTC(),
		Kind:      "text",
		Body:      "another",
		PushName:  "B",
	})

	chat := (*chats)["chat@s.whatsapp.net"]
	assert.Equal(t, 4, chat.UnreadCount)
}

func TestHandleIncomingNonText(t *testing.T) {
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN3",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(3000, 0).UTC(),
		Kind:      "image",
		Body:      "",
		PushName:  "C",
	})

	require.Contains(t, *msgs, "MIN3")
	got := (*msgs)["MIN3"]
	assert.Equal(t, "image", got.Kind)
	assert.Empty(t, got.Body)
}

func TestHandleIncomingEmptyPushName(t *testing.T) {
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN4",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "sender@s.whatsapp.net",
		Timestamp: time.Unix(4000, 0).UTC(),
		Kind:      "text",
		Body:      "yo",
		PushName:  "",
	})

	// No contact upsert when push_name is empty.
	assert.NotContains(t, *contacts, "sender@s.whatsapp.net")
}

func TestListChatsValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	// limit < 1
	_, err := s.ListChats(context.Background(), time.Time{}, 0, false)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit > 100
	_, err = s.ListChats(context.Background(), time.Time{}, 101, false)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListChatsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["a@s.whatsapp.net"] = store.Chat{JID: "a@s.whatsapp.net", Kind: "user", LastMsgAt: time.Unix(100, 0).UTC()}
	(*chats)["b@s.whatsapp.net"] = store.Chat{JID: "b@s.whatsapp.net", Kind: "user", LastMsgAt: time.Unix(200, 0).UTC()}

	got, err := s.ListChats(ctx, time.Time{}, 50, false)
	require.NoError(t, err)
	require.Len(t, got, 2)
	// in-memory fake sorts by LastMsgAt DESC
	assert.Equal(t, "b@s.whatsapp.net", got[0].JID)
}

func TestGetChatNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.GetChat(context.Background(), "missing@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetChatHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*chats)["x@s.whatsapp.net"] = store.Chat{JID: "x@s.whatsapp.net", Name: "X", Kind: "user"}

	got, err := s.GetChat(ctx, "x@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, "X", got.Name)
}

func TestListMessagesValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	// empty chat_jid
	_, err := s.ListMessages(context.Background(), "", time.Time{}, 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit out of range
	_, err = s.ListMessages(context.Background(), "x@s.whatsapp.net", time.Time{}, 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.ListMessages(context.Background(), "x@s.whatsapp.net", time.Time{}, 101)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListMessagesHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "x@s.whatsapp.net", Timestamp: time.Unix(100, 0).UTC(), Kind: "text", Body: "hi"}

	got, err := s.ListMessages(ctx, "x@s.whatsapp.net", time.Time{}, 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "M1", got[0].ID)
}

func TestSearchMessagesValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	// empty q
	_, err := s.SearchMessages(context.Background(), "", 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	// limit out of range
	_, err = s.SearchMessages(context.Background(), "x", 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.SearchMessages(context.Background(), "x", 101)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListContactsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}

	got, err := s.ListContacts(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSearchContactsValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.SearchContacts(context.Background(), "", 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.SearchContacts(context.Background(), "x", 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSearchContactsHappyPath(t *testing.T) {
	bundle, _, _, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net", PushName: "Alice"}
	(*contacts)["b@s.whatsapp.net"] = store.Contact{JID: "b@s.whatsapp.net", PushName: "Bob"}

	got, err := s.SearchContacts(context.Background(), "ali", 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a@s.whatsapp.net", got[0].JID)
}

func TestSearchMessagesHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "c@s.whatsapp.net", Body: "the quick fox", Timestamp: time.Unix(100, 0).UTC()}
	(*msgs)["M2"] = store.Message{ID: "M2", ChatJID: "c@s.whatsapp.net", Body: "lazy dog", Timestamp: time.Unix(200, 0).UTC()}

	got, err := s.SearchMessages(ctx, "fox", 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "M1", got[0].ID)
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, contacts := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["a@s.whatsapp.net"] = store.Chat{JID: "a@s.whatsapp.net", UnreadCount: 3}
	(*chats)["b@s.whatsapp.net"] = store.Chat{JID: "b@s.whatsapp.net", UnreadCount: 1}
	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "a@s.whatsapp.net", Body: "x"}
	(*msgs)["M2"] = store.Message{ID: "M2", ChatJID: "a@s.whatsapp.net", Body: "y"}
	(*msgs)["M3"] = store.Message{ID: "M3", ChatJID: "b@s.whatsapp.net", Body: "z"}
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net"}

	got, err := s.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Chats)
	assert.Equal(t, 3, got.Messages)
	assert.Equal(t, 1, got.Contacts)
	assert.Equal(t, 4, got.UnreadTotal)
}

func TestSendMediaSuccess(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _ := newInMemoryBundle()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	wa := &mediaSenderFakeWA{
		mediaResp: waclient.Sent{ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net"},
	}
	ms := mediastore.New(t.TempDir())
	s := service.New(wa, bundle, ms, nil)

	body := []byte("fake-image-bytes")
	got, err := s.SendMedia(ctx, service.SendMediaRequest{
		ChatJID: "27821234567@s.whatsapp.net",
		Kind:    "image",
		Caption: "hello",
		MIME:    "image/jpeg",
		Body:    body,
	})
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "image", got.Kind)
	assert.Equal(t, "hello", got.Body)

	assert.Equal(t, "27821234567@s.whatsapp.net", wa.gotMediaArgs[0])
	assert.Equal(t, "image", wa.gotMediaArgs[1])
	assert.Equal(t, "hello", wa.gotMediaArgs[2])
	assert.Equal(t, body, wa.gotMediaBody)

	require.Contains(t, *msgs, "MID1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
}

func TestSendMediaValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)

	cases := []struct {
		label string
		req   service.SendMediaRequest
	}{
		{"empty chat_jid", service.SendMediaRequest{Kind: "image", MIME: "image/jpeg", Body: []byte("x")}},
		{"empty body", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg"}},
		{"empty mime", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", Body: []byte("x")}},
		{"bad kind", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "video", MIME: "video/mp4", Body: []byte("x")}},
		{"document missing filename", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "document", MIME: "application/pdf", Body: []byte("x")}},
		{"caption too long", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg", Body: []byte("x"), Caption: strings.Repeat("c", 4097)}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.SendMedia(context.Background(), tc.req)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestSendMediaNotConnected(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	wa := &mediaSenderFakeWA{mediaErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, ms, nil)

	_, err := s.SendMedia(context.Background(), service.SendMediaRequest{
		ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg", Body: []byte("x"),
	})
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestGetMediaRefHappyPath(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)

	require.NoError(t, bundle.Media.Put(context.Background(), store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: 100, SHA256: "abc", Path: "/tmp/abc.jpg",
	}))

	got, err := s.GetMediaRef(context.Background(), "MID1")
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.MessageID)
	assert.Equal(t, "image/jpeg", got.MIME)
}

func TestGetMediaRefNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetMediaRefValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}
