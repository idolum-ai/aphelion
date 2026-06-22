//go:build linux

package tool

import (
	"context"
	"strings"

	"github.com/idolum-ai/aphelion/session"
)

type authorityUseRefContextKey struct{}
type executionAuthorityAdmissionContextKey struct{}
type toolInvocationRefContextKey struct{}

type ToolInvocationRef struct {
	TurnRunID    int64
	InvocationID string
}

func WithAuthorityUseRef(ctx context.Context, ref session.AuthorityUseRef) context.Context {
	return context.WithValue(ctx, authorityUseRefContextKey{}, session.NormalizeAuthorityUseRef(ref))
}

func AuthorityUseRefFromContext(ctx context.Context) (session.AuthorityUseRef, bool) {
	if ctx == nil {
		return session.AuthorityUseRef{}, false
	}
	ref, ok := ctx.Value(authorityUseRefContextKey{}).(session.AuthorityUseRef)
	if !ok {
		return session.AuthorityUseRef{}, false
	}
	return session.NormalizeAuthorityUseRef(ref), true
}

func WithExecutionAuthorityAdmission(ctx context.Context, admission session.ExecutionRunAuthority) context.Context {
	return context.WithValue(ctx, executionAuthorityAdmissionContextKey{}, session.NormalizeExecutionRunAuthority(admission))
}

func ExecutionAuthorityAdmissionFromContext(ctx context.Context) (session.ExecutionRunAuthority, bool) {
	if ctx == nil {
		return session.ExecutionRunAuthority{}, false
	}
	admission, ok := ctx.Value(executionAuthorityAdmissionContextKey{}).(session.ExecutionRunAuthority)
	if !ok {
		return session.ExecutionRunAuthority{}, false
	}
	return session.NormalizeExecutionRunAuthority(admission), true
}

func WithToolInvocationRef(ctx context.Context, ref ToolInvocationRef) context.Context {
	ref.InvocationID = strings.TrimSpace(ref.InvocationID)
	return context.WithValue(ctx, toolInvocationRefContextKey{}, ref)
}

func ToolInvocationRefFromContext(ctx context.Context) (ToolInvocationRef, bool) {
	if ctx == nil {
		return ToolInvocationRef{}, false
	}
	ref, ok := ctx.Value(toolInvocationRefContextKey{}).(ToolInvocationRef)
	if !ok {
		return ToolInvocationRef{}, false
	}
	ref.InvocationID = strings.TrimSpace(ref.InvocationID)
	if ref.TurnRunID <= 0 && ref.InvocationID == "" {
		return ToolInvocationRef{}, false
	}
	return ref, true
}
