package mcp

import (
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestListTools_AllNamesPresent(t *testing.T) {
	svc := &fakeService{}
	ctx, session := inMemoryClient(t, svc)

	res, err := session.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)

	got := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		got[tool.Name] = true
	}

	want := []string{
		"wa_status", "wa_stats",
		"wa_login_qr", "wa_login_phone", "wa_logout",
		"wa_send_text", "wa_send_media", "wa_get_media",
		"wa_edit_message", "wa_delete_message",
		"wa_react", "wa_list_reactions",
		"wa_mark_read", "wa_typing", "wa_list_receipts",
		"wa_list_chats", "wa_get_chat", "wa_list_messages", "wa_search_messages",
		"wa_list_contacts", "wa_search_contacts",
		"wa_create_group", "wa_list_group_members", "wa_update_group_members", "wa_leave_group",
	}
	require.Len(t, want, 25)
	for _, name := range want {
		require.True(t, got[name], "tool %q is missing from ListTools response", name)
	}
	require.Equal(t, len(want), len(res.Tools), "ListTools returned unexpected extras: %v", res.Tools)
}

func TestServerInstructions_NonEmptyAndShort(t *testing.T) {
	svc := &fakeService{}
	_, session := inMemoryClient(t, svc)

	instr := session.InitializeResult().Instructions
	require.NotEmpty(t, instr)
	require.LessOrEqual(t, len(instr), 1024)
}
