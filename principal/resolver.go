//go:build linux

package principal

type Role string

const (
	RoleAdmin        Role = "admin"
	RoleApprovedUser Role = "approved_user"
	RoleDurableAgent Role = "durable_agent"
)

type Principal struct {
	TelegramUserID int64
	DurableAgentID string
	Role           Role
}

type Resolver struct {
	admin    map[int64]struct{}
	approved map[int64]struct{}
}

func NewResolver(adminUserIDs []int64, approvedUserIDs []int64) *Resolver {
	admin := make(map[int64]struct{}, len(adminUserIDs))
	for _, id := range adminUserIDs {
		admin[id] = struct{}{}
	}
	approved := make(map[int64]struct{}, len(approvedUserIDs))
	for _, id := range approvedUserIDs {
		approved[id] = struct{}{}
	}
	return &Resolver{
		admin:    admin,
		approved: approved,
	}
}

func (r *Resolver) ResolveTelegramUser(userID int64) (Principal, bool) {
	if r == nil || userID <= 0 {
		return Principal{}, false
	}
	if _, ok := r.admin[userID]; ok {
		return Principal{
			TelegramUserID: userID,
			Role:           RoleAdmin,
		}, true
	}
	if _, ok := r.approved[userID]; ok {
		return Principal{
			TelegramUserID: userID,
			Role:           RoleApprovedUser,
		}, true
	}
	return Principal{}, false
}
