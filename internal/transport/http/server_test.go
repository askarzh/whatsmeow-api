package http_test

import (
	"context"
	"io"
	httpcl "net/http"
	"testing"
	"time"

	"github.com/askar/whatsmeow-api/internal/config"
	httpapi "github.com/askar/whatsmeow-api/internal/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerServesHealthAndShutsDown(t *testing.T) {
	srv := httpapi.NewServer(httpapi.Deps{
		Config: config.Config{Server: config.ServerConfig{Bind: "127.0.0.1", Port: 0}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	// wait for the server to bind
	deadline := time.Now().Add(2 * time.Second)
	var addr string
	for {
		addr = srv.Addr()
		if addr != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server never reported address")
		}
		time.Sleep(10 * time.Millisecond)
	}

	res, err := httpcl.Get("http://" + addr + "/v1/health")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, httpcl.StatusOK, res.StatusCode)
	_, _ = io.Copy(io.Discard, res.Body)

	cancel()
	select {
	case err := <-errCh:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}
