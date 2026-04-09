//go:build linux

package principal

import "testing"

func TestResolveTelegramUser(t *testing.T) {
	t.Parallel()

	resolver := NewResolver([]int64{10}, []int64{20})

	admin, ok := resolver.ResolveTelegramUser(10)
	if !ok {
		t.Fatal("admin user should resolve")
	}
	if admin.Role != RoleAdmin {
		t.Fatalf("admin role = %q, want %q", admin.Role, RoleAdmin)
	}

	approved, ok := resolver.ResolveTelegramUser(20)
	if !ok {
		t.Fatal("approved user should resolve")
	}
	if approved.Role != RoleApprovedUser {
		t.Fatalf("approved role = %q, want %q", approved.Role, RoleApprovedUser)
	}

	if _, ok := resolver.ResolveTelegramUser(30); ok {
		t.Fatal("unknown user should not resolve")
	}
}
