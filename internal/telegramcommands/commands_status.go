//go:build linux

package telegramcommands

const statusCallbackPrefix = "status:"
const staleStatusCallbackText = "This status action is no longer available. Run /status again."
const adminStatusOnlyText = "This status view is available to Telegram admins only."
const statusMessageChunkLimit = 3800

type statusView string

const (
	statusViewChat       statusView = "chat"
	statusViewPending    statusView = "pending"
	statusViewSystem     statusView = "system"
	statusViewHotChats   statusView = "hot"
	statusViewFindChat   statusView = "find"
	statusViewDurables   statusView = "durables"
	statusViewChatTarget statusView = "chat_target"
)
