package mcp_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	mcptransport "github.com/askarzh/whatsmeow-api/internal/transport/mcp"
)

func TestNew_ReturnsNonNilHandler(t *testing.T) {
	h := mcptransport.New(mcptransport.Deps{
		Version: "test",
	})
	require.NotNil(t, h)
}
