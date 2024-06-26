package utils

import (
	"context"

	"github.com/sirupsen/logrus"
)

type contextString string

type ctxKeys struct {
	UserID      contextString
	Log         contextString
	RequestID   contextString
	RequestType contextString
	Scope       contextString
}

// CtxKeys is context value keys
var CtxKeys = ctxKeys{
	UserID:      "userID",
	Log:         "Log",
	RequestID:   "requestID",
	RequestType: "requestType",
	Scope:       "scope",
}

// UserID extracts userID from context
func UserID(ctx context.Context) int64 {
	v := ctx.Value(CtxKeys.UserID)
	if v == nil {
		return 0
	}
	return v.(int64)
}

// RequestID extracts requestID from context
func RequestID(ctx context.Context) string {
	v := ctx.Value(CtxKeys.RequestID)
	if v == nil {
		return ""
	}
	return v.(string)
}

// RequestType extracts requestID from context
func RequestType(ctx context.Context) string {
	v := ctx.Value(CtxKeys.RequestType)
	if v == nil {
		return ""
	}
	return v.(string)
}

// Scope extracts scope from context
func Scope(ctx context.Context) string {
	v := ctx.Value(CtxKeys.Scope)
	if v == nil {
		return ""
	}
	return v.(string)
}

// LogCtx returns logger with certain context values included
func LogCtx(ctx context.Context) *logrus.Entry {
	entry := ctx.Value(CtxKeys.Log).(*logrus.Entry)

	if userID := UserID(ctx); userID != 0 {
		entry = entry.WithField(string(CtxKeys.UserID), userID)
	}
	if requestID := RequestID(ctx); len(requestID) > 0 {
		entry = entry.WithField(string(CtxKeys.RequestID), requestID)
	}
	if requestType := RequestType(ctx); len(requestType) > 0 {
		entry = entry.WithField(string(CtxKeys.RequestType), requestType)
	}
	if scope := Scope(ctx); len(scope) > 0 {
		entry = entry.WithField(string(CtxKeys.Scope), scope)
	}

	return entry
}
