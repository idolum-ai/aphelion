//go:build linux

package tool

import (
	"context"

	"github.com/idolum-ai/aphelion/session"
)

type authorityUseRefContextKey struct{}
type executionAuthorityAdmissionContextKey struct{}

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
