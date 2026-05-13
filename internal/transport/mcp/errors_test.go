package mcp

import (
	"errors"
	"fmt"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

func TestMapErr_Nil(t *testing.T) {
	res, err := mapErr(nil, nil)
	require.Nil(t, res)
	require.NoError(t, err)
}

func TestMapErr_InvalidRequest(t *testing.T) {
	in := fmt.Errorf("%w: text is required", service.ErrInvalidRequest)
	res, err := mapErr(in, nil)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)
	require.Contains(t, contentText(t, res), "invalid request:")
	require.Contains(t, contentText(t, res), "text is required")
}

func TestMapErr_Forbidden(t *testing.T) {
	res, err := mapErr(fmt.Errorf("%w: not owner", service.ErrForbidden), nil)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, contentText(t, res), "forbidden:")
}

func TestMapErr_NotFound(t *testing.T) {
	res, err := mapErr(fmt.Errorf("%w: id=abc", store.ErrNotFound), nil)
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, contentText(t, res), "not found:")
}

func TestMapErr_Unknown(t *testing.T) {
	res, err := mapErr(errors.New("kaboom"), nil)
	require.Nil(t, res)
	require.EqualError(t, err, "internal error")
}

// contentText pulls the text out of the first content block. When the SDK
// renames TextContent or its accessor, only this helper changes.
func contentText(t *testing.T, res *mcpsdk.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok, "expected *mcp.TextContent, got %T", res.Content[0])
	return tc.Text
}
