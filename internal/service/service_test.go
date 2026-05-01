package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
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
func (f *fakeWA) OnIncomingMessage(func(waclient.IncomingMessage)) {}

func TestStatusPassThrough(t *testing.T) {
	jid := "27821234567@s.whatsapp.net"
	now := time.Now()
	f := &fakeWA{status: waclient.Status{Connected: true, JID: &jid, Since: &now}}
	s := service.New(f)

	got, err := s.Status(context.Background())
	require.NoError(t, err)
	assert.True(t, got.Connected)
	assert.Equal(t, &jid, got.JID)
}

func TestLoginQRPassThrough(t *testing.T) {
	ch := make(chan waclient.QREvent)
	f := &fakeWA{loginQR: ch}
	s := service.New(f)

	got, err := s.LoginQR(context.Background())
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.QREvent)(ch), got)
}

func TestLoginQRError(t *testing.T) {
	f := &fakeWA{loginQRErr: waclient.ErrAlreadyLoggedIn}
	s := service.New(f)
	_, err := s.LoginQR(context.Background())
	assert.ErrorIs(t, err, waclient.ErrAlreadyLoggedIn)
}

func TestLoginPhoneRejectsBadNumber(t *testing.T) {
	f := &fakeWA{}
	s := service.New(f)
	_, err := s.LoginPhone(context.Background(), "27821234567")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "phone number")
	assert.Empty(t, f.loginPhoneArg, "fake should not be called")
}

func TestLoginPhonePassThrough(t *testing.T) {
	ch := make(chan waclient.PairEvent)
	f := &fakeWA{loginPhone: ch}
	s := service.New(f)
	got, err := s.LoginPhone(context.Background(), "+27821234567")
	require.NoError(t, err)
	assert.Equal(t, (<-chan waclient.PairEvent)(ch), got)
	assert.Equal(t, "+27821234567", f.loginPhoneArg)
}

func TestLogoutPassThrough(t *testing.T) {
	f := &fakeWA{logoutErr: errors.New("boom")}
	s := service.New(f)
	err := s.Logout(context.Background())
	assert.ErrorContains(t, err, "boom")
}
