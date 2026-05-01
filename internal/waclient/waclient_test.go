package waclient_test

import (
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/stretchr/testify/assert"
)

func TestValidatePhoneNumber(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"+27821234567", true},
		{"+1234567", true},
		{"+123456789012345", true},
		{"27821234567", false},       // missing +
		{"+abc12345", false},         // non-digit
		{"+", false},                 // empty digits
		{"+12345", false},            // too short (under 6 digits)
		{"+1234567890123456", false}, // too long (over 15 digits)
		{" +27821234567", false},     // leading space
		{"+27 821 234 567", false},   // spaces
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, waclient.IsValidPhoneNumber(tc.in))
		})
	}
}

func TestErrorsExist(t *testing.T) {
	assert.NotNil(t, waclient.ErrLoginInProgress)
	assert.NotNil(t, waclient.ErrAlreadyLoggedIn)
	assert.NotNil(t, waclient.ErrNotLoggedIn)
	assert.NotNil(t, waclient.ErrNotConnected)
}

func TestChatKindFromJID(t *testing.T) {
	cases := []struct {
		jid  string
		want string
	}{
		{"27821234567@s.whatsapp.net", "user"},
		{"123456789-1234567890@g.us", "group"},
		{"status@broadcast", "broadcast"},
		{"chan@newsletter", "newsletter"},
		{"oddball@example.com", "unknown"},
		{"", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.jid, func(t *testing.T) {
			assert.Equal(t, tc.want, waclient.ChatKindFromJID(tc.jid))
		})
	}
}
