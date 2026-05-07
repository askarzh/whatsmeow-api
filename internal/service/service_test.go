package service_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"sync"

	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWA struct {
	status          waclient.Status
	resumeErr       error
	loginQR         <-chan waclient.QREvent
	loginQRErr      error
	loginPhone      <-chan waclient.PairEvent
	loginPhoneErr   error
	loginPhoneArg   string
	logoutErr       error
	closed          bool
	incoming        func(waclient.IncomingMessage)
	incomingReceipt func(waclient.IncomingReceipt)
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
func (f *fakeWA) SendText(context.Context, string, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) SendEdit(context.Context, string, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) SendRevoke(context.Context, string, string) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) OnIncomingMessage(h func(waclient.IncomingMessage)) {
	f.incoming = h
}
func (f *fakeWA) SendMedia(context.Context, string, string, string, string, string, []byte) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
func (f *fakeWA) SendReaction(context.Context, string, string, string) error        { return nil }
func (f *fakeWA) MarkRead(context.Context, string, string, string, time.Time) error { return nil }
func (f *fakeWA) SendChatPresence(context.Context, string, string) error            { return nil }
func (f *fakeWA) OnIncomingReceipt(h func(waclient.IncomingReceipt))                { f.incomingReceipt = h }
func (f *fakeWA) CreateGroup(context.Context, string, []string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeWA) GetGroupInfo(context.Context, string) (waclient.Group, error) {
	return waclient.Group{}, nil
}
func (f *fakeWA) UpdateGroupParticipants(context.Context, string, string, []string) ([]waclient.ParticipantChange, error) {
	return nil, nil
}
func (f *fakeWA) LeaveGroup(context.Context, string) error { return nil }

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

func newInMemoryBundle() (store.Bundle, *memChats, *memMessages, *memContacts, *reactionStore, *receiptStore) {
	c := memChats{}
	m := memMessages{}
	co := memContacts{}
	rs := &reactionStore{m: map[string]store.Reaction{}}
	rcps := &receiptStore{m: map[string]store.Receipt{}}
	return store.Bundle{
		Chats:     &chatStore{m: c},
		Messages:  &messageStore{m: m},
		Contacts:  &contactStore{m: co},
		Media:     &mediaStore{},
		Events:    &eventsStore{},
		KV:        &kvStore{m: map[string]string{}},
		Reactions: rs,
		Receipts:  rcps,
	}, &c, &m, &co, rs, rcps
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
func (s *chatStore) Count(context.Context) (int, error)              { return len(s.m), nil }
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
func (s *messageStore) SoftDelete(_ context.Context, id string, at time.Time) error {
	msg, ok := s.m[id]
	if !ok {
		return store.ErrNotFound
	}
	msg.DeletedAt = &at
	s.m[id] = msg
	return nil
}
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
	mu sync.Mutex
	m  map[string]store.MediaRef
}

func (s *mediaStore) Put(_ context.Context, ref store.MediaRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]store.MediaRef{}
	}
	s.m[ref.MessageID] = ref
	return nil
}
func (s *mediaStore) GetByMessageID(_ context.Context, id string) (store.MediaRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

type reactionStore struct {
	mu sync.Mutex
	m  map[string]store.Reaction // key: messageID + "|" + senderJID
}

func (s *reactionStore) Put(_ context.Context, r store.Reaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]store.Reaction{}
	}
	s.m[r.MessageID+"|"+r.SenderJID] = r
	return nil
}

func (s *reactionStore) Delete(_ context.Context, messageID, senderJID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, messageID+"|"+senderJID)
	return nil
}

func (s *reactionStore) ListByMessageID(_ context.Context, messageID string) ([]store.Reaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Reaction, 0)
	for _, r := range s.m {
		if r.MessageID == messageID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SenderJID < out[j].SenderJID })
	return out, nil
}

type receiptStore struct {
	mu sync.Mutex
	m  map[string]store.Receipt // key: messageID + "|" + readerJID + "|" + type
}

func (s *receiptStore) Put(_ context.Context, r store.Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]store.Receipt{}
	}
	s.m[r.MessageID+"|"+r.ReaderJID+"|"+r.Type] = r
	return nil
}

func (s *receiptStore) ListByMessageID(_ context.Context, messageID string) ([]store.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Receipt, 0)
	for _, r := range s.m {
		if r.MessageID == messageID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ReaderJID != out[j].ReaderJID {
			return out[i].ReaderJID < out[j].ReaderJID
		}
		return out[i].Type < out[j].Type
	})
	return out, nil
}

type readFakeWA struct {
	fakeWA
	gotMarkChat      string
	gotMarkSender    string
	gotMarkMsgID     string
	markErr          error
	gotPresenceChat  string
	gotPresenceState string
	presenceErr      error
}

func (f *readFakeWA) MarkRead(_ context.Context, chatJID, senderJID, messageID string, _ time.Time) error {
	f.gotMarkChat = chatJID
	f.gotMarkSender = senderJID
	f.gotMarkMsgID = messageID
	return f.markErr
}
func (f *readFakeWA) SendChatPresence(_ context.Context, chatJID, state string) error {
	f.gotPresenceChat = chatJID
	f.gotPresenceState = state
	return f.presenceErr
}

func TestMarkMessageReadHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}

	require.NoError(t, s.MarkMessageRead(ctx, "M1"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotMarkChat)
	assert.Equal(t, "alice@s.whatsapp.net", wa.gotMarkSender)
	assert.Equal(t, "M1", wa.gotMarkMsgID)

	got, err := bundle.Chats.Get(ctx, "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 4, got.UnreadCount)
}

func TestMarkMessageReadDecrementClampsAtZero(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 0,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "hi",
	}

	require.NoError(t, s.MarkMessageRead(ctx, "M1"))
	got, err := bundle.Chats.Get(ctx, "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 0, got.UnreadCount, "must not go negative")
}

func TestMarkMessageReadNotFound(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.MarkMessageRead(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestMarkMessageReadNotConnected(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{
		fakeWA:  fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		markErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.MarkMessageRead(context.Background(), "M1")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestMarkMessageReadValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.MarkMessageRead(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingComposing(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NoError(t, s.SendTyping(context.Background(), "c@s.whatsapp.net", "composing"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotPresenceChat)
	assert.Equal(t, "composing", wa.gotPresenceState)
}

func TestSendTypingPaused(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NoError(t, s.SendTyping(context.Background(), "c@s.whatsapp.net", "paused"))
	assert.Equal(t, "paused", wa.gotPresenceState)
}

func TestSendTypingValidationBadState(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "c@s.whatsapp.net", "yelling")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingValidationEmptyChatJID(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "", "composing")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSendTypingNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{
		fakeWA:      fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		presenceErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendTyping(context.Background(), "c@s.whatsapp.net", "composing")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestListReceiptsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	require.NoError(t, bundle.Receipts.Put(ctx, store.Receipt{
		MessageID: "M1", ReaderJID: "alice@s.whatsapp.net", Type: "read", Timestamp: time.Unix(1000, 0).UTC(),
	}))

	got, err := s.ListReceipts(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "read", got[0].Type)
}

func TestListReceiptsValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &readFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListReceipts(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

type sendableFakeWA struct {
	fakeWA
	sentArgs   [3]string // [0]=chatJID, [1]=text, [2]=replyTo
	sendResp   waclient.Sent
	sendErr    error
	calledSend bool
}

func (f *sendableFakeWA) SendText(_ context.Context, chatJID, text, replyTo string) (waclient.Sent, error) {
	f.calledSend = true
	f.sentArgs[0] = chatJID
	f.sentArgs[1] = text
	f.sentArgs[2] = replyTo
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
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()

	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.SendText(ctx, "27821234567@s.whatsapp.net", "hello", "")
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct{ chat, text, expect string }{
		{"", "hello", "chat_jid"},
		{"a@s.whatsapp.net", "", "text"},
		{"a@s.whatsapp.net", strings.Repeat("x", 4097), "text"},
	}
	for _, tc := range cases {
		t.Run(tc.expect, func(t *testing.T) {
			_, err := s.SendText(ctx, tc.chat, tc.text, "")
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
			assert.False(t, wa.calledSend, "fake WA must not be called on validation failure")
		})
	}
}

func TestSendTextNotConnected(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{sendErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.SendText(ctx, "a@s.whatsapp.net", "hi", "")
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

	got, err := s.SendText(context.Background(), "a@s.whatsapp.net", "hello", "")
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
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	(*chats)["chat@s.whatsapp.net"] = store.Chat{
		JID: "chat@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}

	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: time.Unix(1000, 0).UTC(), SenderJID: "me@s.whatsapp.net"},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.SendText(ctx, "chat@s.whatsapp.net", "hi", "")
	require.NoError(t, err)

	// unread_count must be preserved at 5 — sending should not reset it.
	assert.Equal(t, 5, (*chats)["chat@s.whatsapp.net"].UnreadCount)
}

func TestHandleIncomingNewChat(t *testing.T) {
	bundle, chats, msgs, contacts, _, _ := newInMemoryBundle()
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
	bundle, chats, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, contacts, _, _ := newInMemoryBundle()
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
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
	bundle, chats, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.GetChat(context.Background(), "missing@s.whatsapp.net")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetChatHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*chats)["x@s.whatsapp.net"] = store.Chat{JID: "x@s.whatsapp.net", Name: "X", Kind: "user"}

	got, err := s.GetChat(ctx, "x@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, "X", got.Name)
}

func TestListMessagesValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{ID: "M1", ChatJID: "x@s.whatsapp.net", Timestamp: time.Unix(100, 0).UTC(), Kind: "text", Body: "hi"}

	got, err := s.ListMessages(ctx, "x@s.whatsapp.net", time.Time{}, 50)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "M1", got[0].ID)
}

func TestSearchMessagesValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, contacts, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*contacts)["a@s.whatsapp.net"] = store.Contact{JID: "a@s.whatsapp.net", PushName: "A"}

	got, err := s.ListContacts(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestSearchContactsValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &sendableFakeWA{}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.SearchContacts(context.Background(), "", 50)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
	_, err = s.SearchContacts(context.Background(), "x", 0)
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestSearchContactsHappyPath(t *testing.T) {
	bundle, _, _, contacts, _, _ := newInMemoryBundle()
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
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
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
	bundle, chats, msgs, contacts, _, _ := newInMemoryBundle()
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
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	wa := &mediaSenderFakeWA{mediaErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, ms, nil)

	_, err := s.SendMedia(context.Background(), service.SendMediaRequest{
		ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg", Body: []byte("x"),
	})
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestGetMediaRefHappyPath(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
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
	bundle, _, _, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetMediaRefValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestHandleIncomingDownloadsMedia(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, ms, nil)
	require.NotNil(t, wa.incoming)

	body := []byte("inbound-media-bytes")
	mime := "image/png"
	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN1",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image",
		PushName:  "C",
		MediaDownloader: func(_ context.Context) ([]byte, string, error) {
			return body, mime, nil
		},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := bundle.Media.GetByMessageID(context.Background(), "MIN1")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, err := bundle.Media.GetByMessageID(context.Background(), "MIN1")
	require.NoError(t, err)
	assert.Equal(t, "image/png", got.MIME)
	assert.Equal(t, int64(len(body)), got.Size)
	assert.NotEmpty(t, got.Path)
}

type editFakeWA struct {
	fakeWA
	editResp           waclient.Sent
	editErr            error
	gotEditChatJID     string
	gotEditMessageID   string
	gotEditNewText     string
	revokeResp         waclient.Sent
	revokeErr          error
	gotRevokeChatJID   string
	gotRevokeMessageID string
}

func (f *editFakeWA) SendEdit(_ context.Context, chatJID, messageID, newText string) (waclient.Sent, error) {
	f.gotEditChatJID = chatJID
	f.gotEditMessageID = messageID
	f.gotEditNewText = newText
	return f.editResp, f.editErr
}
func (f *editFakeWA) SendRevoke(_ context.Context, chatJID, messageID string) (waclient.Sent, error) {
	f.gotRevokeChatJID = chatJID
	f.gotRevokeMessageID = messageID
	return f.revokeResp, f.revokeErr
}

func TestEditMessageHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	myJID := jid
	wa := &editFakeWA{
		fakeWA:   fakeWA{status: waclient.Status{Connected: true, JID: &myJID}},
		editResp: waclient.Sent{ID: "EDIT1", Timestamp: time.Unix(2000, 0).UTC(), SenderJID: jid},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: jid,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "old",
	}

	got, err := s.EditMessage(ctx, "M1", "new text")
	require.NoError(t, err)
	assert.Equal(t, "new text", got.Body)
	require.NotNil(t, got.EditedAt)
	assert.True(t, got.EditedAt.Equal(time.Unix(2000, 0).UTC()))

	assert.Equal(t, "M1", wa.gotEditMessageID)
	assert.Equal(t, "new text", wa.gotEditNewText)
	assert.Equal(t, "c@s.whatsapp.net", wa.gotEditChatJID)
}

func TestEditMessageNotFound(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.EditMessage(context.Background(), "missing", "x")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestEditMessageForbiddenWrongSender(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "someone-else@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	_, err := s.EditMessage(context.Background(), "M1", "new")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}

func TestEditMessageForbiddenAlreadyDeleted(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	deletedAt := time.Unix(1500, 0).UTC()
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: myJID,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
		DeletedAt: &deletedAt,
	}
	_, err := s.EditMessage(context.Background(), "M1", "new")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}

func TestEditMessageValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.EditMessage(context.Background(), "", "text")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	_, err = s.EditMessage(context.Background(), "M1", "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))

	_, err = s.EditMessage(context.Background(), "M1", strings.Repeat("x", 4097))
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestDeleteMessageHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &editFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: jid,
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	require.NoError(t, s.DeleteMessage(ctx, "M1"))
	assert.Equal(t, "M1", wa.gotRevokeMessageID)
	assert.Equal(t, "c@s.whatsapp.net", wa.gotRevokeChatJID)
}

func TestDeleteMessageNotFound(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.DeleteMessage(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestDeleteMessageForbidden(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	myJID := "me@s.whatsapp.net"
	wa := &editFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &myJID}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.DeleteMessage(context.Background(), "M1")
	assert.True(t, errors.Is(err, service.ErrForbidden))
}

func TestHandleIncomingDownloadFailureLogged(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	ms := mediastore.New(t.TempDir())
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, ms, nil)
	require.NotNil(t, wa.incoming)

	called := make(chan struct{}, 1)
	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN2",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image",
		MediaDownloader: func(_ context.Context) ([]byte, string, error) {
			called <- struct{}{}
			return nil, "", errors.New("simulated download failure")
		},
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("downloader never called")
	}
	time.Sleep(50 * time.Millisecond)

	_, err := bundle.Media.GetByMessageID(context.Background(), "MIN2")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestHandleIncomingRevoke(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	// Seed the message that gets revoked.
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "secret",
	}

	wa.incoming(waclient.IncomingMessage{
		ID:         "EVT1",
		ChatJID:    "c@s.whatsapp.net",
		ChatKind:   "user",
		SenderJID:  "other@s.whatsapp.net",
		Timestamp:  time.Unix(2000, 0).UTC(),
		RevokeOfID: "M1",
	})

	got, err := bundle.Messages.Get(context.Background(), "M1")
	require.NoError(t, err)
	require.NotNil(t, got.DeletedAt)
}

func TestHandleIncomingEditUpdatesBody(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "original",
	}

	editTS := time.Unix(2000, 0).UTC()
	wa.incoming(waclient.IncomingMessage{
		ID:        "EVT2",
		ChatJID:   "c@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "other@s.whatsapp.net",
		Timestamp: editTS,
		Body:      "edited body",
		EditOfID:  "M1",
	})

	got, err := bundle.Messages.Get(context.Background(), "M1")
	require.NoError(t, err)
	assert.Equal(t, "edited body", got.Body)
	require.NotNil(t, got.EditedAt)
	assert.True(t, got.EditedAt.Equal(editTS))
}

func TestHandleIncomingEditUnknownIDLogged(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	// No seeded message; the edit references a non-existent ID.
	wa.incoming(waclient.IncomingMessage{
		ID:        "EVT3",
		ChatJID:   "c@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Body:      "edited body",
		EditOfID:  "NON_EXISTENT",
	})

	// No row should be created.
	_, err := bundle.Messages.Get(context.Background(), "NON_EXISTENT")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestHandleIncomingRevokeDoesNotBumpUnread(t *testing.T) {
	bundle, chats, msgs, _, _, _ := newInMemoryBundle()
	wa := &mediaSenderFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "other@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "other@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		RevokeOfID: "M1",
	})

	chat, err := bundle.Chats.Get(context.Background(), "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 5, chat.UnreadCount, "revoke must not bump unread_count")
}

type reactionFakeWA struct {
	fakeWA
	gotReactionChatJID string
	gotReactionMsgID   string
	gotReactionEmoji   string
	reactionErr        error
}

func (f *reactionFakeWA) SendReaction(_ context.Context, chatJID, originalMessageID, emoji string) error {
	f.gotReactionChatJID = chatJID
	f.gotReactionMsgID = originalMessageID
	f.gotReactionEmoji = emoji
	return f.reactionErr
}

func TestSendReactionHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, rs, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}

	require.NoError(t, s.SendReaction(ctx, "M1", "👍"))
	assert.Equal(t, "c@s.whatsapp.net", wa.gotReactionChatJID)
	assert.Equal(t, "M1", wa.gotReactionMsgID)
	assert.Equal(t, "👍", wa.gotReactionEmoji)

	// Local reactions store has our reaction.
	got, err := bundle.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, jid, got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
	_ = rs
}

func TestSendReactionClear(t *testing.T) {
	ctx := context.Background()
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	// Pre-seed: we already had a reaction.
	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: jid, Emoji: "👍", Timestamp: time.Now(),
	}))

	require.NoError(t, s.SendReaction(ctx, "M1", ""))
	assert.Equal(t, "", wa.gotReactionEmoji)

	// Local reaction is gone.
	got, err := bundle.Reactions.ListByMessageID(ctx, "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSendReactionNotFound(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendReaction(context.Background(), "missing", "👍")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestSendReactionNotConnected(t *testing.T) {
	bundle, _, msgs, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{
		fakeWA:      fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		reactionErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	(*msgs)["M1"] = store.Message{
		ID: "M1", ChatJID: "c@s.whatsapp.net", SenderJID: "alice@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(), Kind: "text", Body: "x",
	}
	err := s.SendReaction(context.Background(), "M1", "👍")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))

	// Local store NOT touched.
	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestSendReactionValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.SendReaction(context.Background(), "", "👍")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListReactionsHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Unix(1000, 0).UTC(),
	}))

	got, err := s.ListReactions(ctx, "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "👍", got[0].Emoji)
}

func TestListReactionsValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &reactionFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListReactions(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestHandleIncomingReactionPut(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	wa.incoming(waclient.IncomingMessage{
		ID:               "EVT1",
		ChatJID:          "c@s.whatsapp.net",
		ChatKind:         "user",
		SenderJID:        "alice@s.whatsapp.net",
		Timestamp:        time.Unix(1000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "👍",
	})

	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alice@s.whatsapp.net", got[0].SenderJID)
	assert.Equal(t, "👍", got[0].Emoji)
}

func TestHandleIncomingReactionClear(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	require.NoError(t, bundle.Reactions.Put(ctx, store.Reaction{
		MessageID: "M1", SenderJID: "alice@s.whatsapp.net", Emoji: "👍", Timestamp: time.Now(),
	}))

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT2", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "alice@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "",
	})

	got, err := bundle.Reactions.ListByMessageID(context.Background(), "M1")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestHandleIncomingReactionDoesNotBumpUnread(t *testing.T) {
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	wa := &reactionFakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incoming)

	(*chats)["c@s.whatsapp.net"] = store.Chat{
		JID: "c@s.whatsapp.net", Kind: "user", UnreadCount: 5,
	}

	wa.incoming(waclient.IncomingMessage{
		ID: "EVT", ChatJID: "c@s.whatsapp.net", ChatKind: "user",
		SenderJID: "alice@s.whatsapp.net", Timestamp: time.Unix(2000, 0).UTC(),
		ReactionTargetID: "M1",
		ReactionEmoji:    "👍",
	})

	chat, err := bundle.Chats.Get(context.Background(), "c@s.whatsapp.net")
	require.NoError(t, err)
	assert.Equal(t, 5, chat.UnreadCount, "reaction must not bump unread_count")
}

func TestHandleReceiptPersistsAll(t *testing.T) {
	bundle, _, _, _, _, rcps := newInMemoryBundle()
	wa := &fakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incomingReceipt)

	ts := time.Unix(5000, 0).UTC()
	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1", "M2", "M3"},
		ChatJID:    "c@s.whatsapp.net",
		ReaderJID:  "alice@s.whatsapp.net",
		Type:       "read",
		Timestamp:  ts,
	})

	rcps.mu.Lock()
	defer rcps.mu.Unlock()
	assert.Len(t, rcps.m, 3, "expected 3 receipt rows, one per message ID")
	for _, id := range []string{"M1", "M2", "M3"} {
		key := id + "|alice@s.whatsapp.net|read"
		r, ok := rcps.m[key]
		require.True(t, ok, "missing receipt for %s", id)
		assert.Equal(t, id, r.MessageID)
		assert.Equal(t, "alice@s.whatsapp.net", r.ReaderJID)
		assert.Equal(t, "read", r.Type)
		assert.Equal(t, ts, r.Timestamp)
	}
}

func TestHandleReceiptUpsert(t *testing.T) {
	bundle, _, _, _, _, rcps := newInMemoryBundle()
	wa := &fakeWA{}
	_ = service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	require.NotNil(t, wa.incomingReceipt)

	ts1 := time.Unix(1000, 0).UTC()
	ts2 := time.Unix(2000, 0).UTC()

	// First receipt event.
	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1"},
		ChatJID:    "c@s.whatsapp.net",
		ReaderJID:  "alice@s.whatsapp.net",
		Type:       "read",
		Timestamp:  ts1,
	})
	// Second receipt event for the same (MessageID, ReaderJID, Type) key.
	wa.incomingReceipt(waclient.IncomingReceipt{
		MessageIDs: []string{"M1"},
		ChatJID:    "c@s.whatsapp.net",
		ReaderJID:  "alice@s.whatsapp.net",
		Type:       "read",
		Timestamp:  ts2,
	})

	rcps.mu.Lock()
	defer rcps.mu.Unlock()
	assert.Len(t, rcps.m, 1, "upsert: same key must result in exactly 1 row")
	key := "M1|alice@s.whatsapp.net|read"
	r, ok := rcps.m[key]
	require.True(t, ok)
	assert.Equal(t, ts2, r.Timestamp, "latest timestamp must win")
}

// ---- Plan 08: groups ----

type groupFakeWA struct {
	fakeWA

	// CreateGroup capture
	gotCreateName  string
	gotCreateParts []string
	createResp     waclient.Group
	createErr      error

	// GetGroupInfo capture
	gotInfoJID string
	infoResp   waclient.Group
	infoErr    error

	// UpdateGroupParticipants capture
	gotUpdateGroupJID string
	gotUpdateAction   string
	gotUpdateParts    []string
	updateResp        []waclient.ParticipantChange
	updateErr         error

	// LeaveGroup capture
	gotLeaveJID string
	leaveErr    error
}

func (f *groupFakeWA) CreateGroup(_ context.Context, name string, participantJIDs []string) (waclient.Group, error) {
	f.gotCreateName = name
	f.gotCreateParts = participantJIDs
	return f.createResp, f.createErr
}
func (f *groupFakeWA) GetGroupInfo(_ context.Context, groupJID string) (waclient.Group, error) {
	f.gotInfoJID = groupJID
	return f.infoResp, f.infoErr
}
func (f *groupFakeWA) UpdateGroupParticipants(_ context.Context, groupJID, action string, participantJIDs []string) ([]waclient.ParticipantChange, error) {
	f.gotUpdateGroupJID = groupJID
	f.gotUpdateAction = action
	f.gotUpdateParts = participantJIDs
	return f.updateResp, f.updateErr
}
func (f *groupFakeWA) LeaveGroup(_ context.Context, groupJID string) error {
	f.gotLeaveJID = groupJID
	return f.leaveErr
}

func TestCreateGroupHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		createResp: waclient.Group{
			JID: "g1@g.us", Name: "Test", OwnerJID: jid,
			CreatedAt: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
			Participants: []waclient.GroupMember{
				{JID: jid, IsAdmin: true, IsSuperAdmin: true},
				{JID: "alice@s.whatsapp.net"},
			},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.CreateGroup(ctx, "  Test  ", []string{"alice@s.whatsapp.net"})
	require.NoError(t, err)
	assert.Equal(t, "g1@g.us", got.JID)
	assert.Equal(t, "Test", wa.gotCreateName, "name should be trimmed before reaching wa")
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, wa.gotCreateParts)

	// Chat row upserted with kind=group.
	require.Contains(t, *chats, "g1@g.us")
	chat := (*chats)["g1@g.us"]
	assert.Equal(t, "group", chat.Kind)
	assert.Equal(t, "Test", chat.Name)
}

func TestCreateGroupValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct {
		label string
		name  string
		parts []string
	}{
		{"empty name", "", []string{"alice@s.whatsapp.net"}},
		{"whitespace name", "  ", []string{"alice@s.whatsapp.net"}},
		{"name too long", strings.Repeat("a", 26), []string{"alice@s.whatsapp.net"}},
		{"empty participants", "Test", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.CreateGroup(context.Background(), tc.name, tc.parts)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestCreateGroupNotConnected(t *testing.T) {
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:    fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		createErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	_, err := s.CreateGroup(context.Background(), "Test", []string{"alice@s.whatsapp.net"})
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))

	// No chat row should have been created.
	assert.Empty(t, *chats)
}

func TestListGroupMembersHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		infoResp: waclient.Group{
			JID: "g1@g.us", Name: "Test", OwnerJID: jid,
			Participants: []waclient.GroupMember{
				{JID: jid, IsAdmin: true, IsSuperAdmin: true},
				{JID: "alice@s.whatsapp.net"},
			},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.ListGroupMembers(ctx, "g1@g.us")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "g1@g.us", wa.gotInfoJID)
}

func TestListGroupMembersValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListGroupMembers(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestListGroupMembersNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:  fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		infoErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	_, err := s.ListGroupMembers(context.Background(), "g1@g.us")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestUpdateGroupMembersHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		updateResp: []waclient.ParticipantChange{
			{JID: "alice@s.whatsapp.net", OK: true},
		},
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	got, err := s.UpdateGroupMembers(ctx, "g1@g.us", "add", []string{"alice@s.whatsapp.net"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].OK)
	assert.Equal(t, "g1@g.us", wa.gotUpdateGroupJID)
	assert.Equal(t, "add", wa.gotUpdateAction)
	assert.Equal(t, []string{"alice@s.whatsapp.net"}, wa.gotUpdateParts)
}

func TestUpdateGroupMembersValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	cases := []struct {
		label  string
		group  string
		action string
		parts  []string
	}{
		{"empty group jid", "", "add", []string{"alice@s.whatsapp.net"}},
		{"bad action", "g1@g.us", "yelling", []string{"alice@s.whatsapp.net"}},
		{"empty participants", "g1@g.us", "add", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.UpdateGroupMembers(context.Background(), tc.group, tc.action, tc.parts)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestLeaveGroupHappyPath(t *testing.T) {
	ctx := context.Background()
	bundle, chats, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)

	// Pre-seed a chat row so we can verify it's untouched after leaving.
	(*chats)["g1@g.us"] = store.Chat{JID: "g1@g.us", Kind: "group", Name: "Test"}

	require.NoError(t, s.LeaveGroup(ctx, "g1@g.us"))
	assert.Equal(t, "g1@g.us", wa.gotLeaveJID)

	// History preserved.
	require.Contains(t, *chats, "g1@g.us")
}

func TestLeaveGroupValidation(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{fakeWA: fakeWA{status: waclient.Status{Connected: true, JID: &jid}}}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.LeaveGroup(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}

func TestLeaveGroupNotConnected(t *testing.T) {
	bundle, _, _, _, _, _ := newInMemoryBundle()
	jid := "me@s.whatsapp.net"
	wa := &groupFakeWA{
		fakeWA:   fakeWA{status: waclient.Status{Connected: true, JID: &jid}},
		leaveErr: waclient.ErrNotConnected,
	}
	s := service.New(wa, bundle, mediastore.New(t.TempDir()), nil)
	err := s.LeaveGroup(context.Background(), "g1@g.us")
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}
