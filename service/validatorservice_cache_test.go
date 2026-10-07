package service

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestValidatorTagsAreInstanceOwnedAndFailuresRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "offline", 503)
			return
		}
		fmt.Fprint(w, `{"tags":[{"id":1,"name":"Action"}]}`)
	}))
	defer server.Close()
	a, b := NewValidator(server.URL), NewValidator(server.URL)
	_, err := a.GetTags(context.Background())
	require.Error(t, err)
	tags, err := a.GetTags(context.Background())
	require.NoError(t, err)
	require.Len(t, tags, 1)
	tags[0].Name = "caller mutation"
	tags, err = a.GetTags(context.Background())
	require.NoError(t, err)
	require.Equal(t, "Action", tags[0].Name)
	require.EqualValues(t, 2, calls.Load())
	_, err = b.GetTags(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 3, calls.Load(), "instances must not share external data")
}
