package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FlashpointProject/flashpoint-submission-system/constants"
	"github.com/FlashpointProject/flashpoint-submission-system/types"
	"github.com/FlashpointProject/flashpoint-submission-system/utils"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestRequestDeviceFlowApprovalRequiresAllScope(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "limited OAuth token rejected",
			scope:      types.AuthScopeIdentity,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "full-scope session accepted",
			scope:      types.AuthScopeAll,
			wantStatus: http.StatusNoContent,
			wantCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			next := func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}
			handler := (&App{}).requestDeviceFlowApproval(next)
			req := httptest.NewRequest(http.MethodPost, "/auth/device/respond", nil)
			ctx := context.WithValue(req.Context(), utils.CtxKeys.Scope, tt.scope)
			ctx = context.WithValue(ctx, utils.CtxKeys.RequestType, constants.RequestWeb)
			ctx = context.WithValue(ctx, utils.CtxKeys.Log, logrus.New().WithField("test", t.Name()))
			req = req.WithContext(ctx)
			response := httptest.NewRecorder()

			handler(response, req)

			require.Equal(t, tt.wantStatus, response.Code)
			require.Equal(t, tt.wantCalled, called)
		})
	}
}
