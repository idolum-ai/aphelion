//go:build linux

package core

import (
	"strconv"
	"strings"
)

const reviewEventCallbackPrefix = "review_event:"

type ReviewEventAction string

const (
	ReviewEventActionApprove       ReviewEventAction = "approve"
	ReviewEventActionReject        ReviewEventAction = "reject"
	ReviewEventActionParentApprove ReviewEventAction = "parent_approve"
)

func EncodeReviewEventCallbackData(eventID int64, action ReviewEventAction) string {
	if eventID <= 0 || !validReviewEventAction(action) {
		return ""
	}
	return reviewEventCallbackPrefix + strconv.FormatInt(eventID, 10) + ":" + string(action)
}

func DecodeReviewEventCallbackData(data string) (eventID int64, action ReviewEventAction, ok bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, reviewEventCallbackPrefix) {
		return 0, "", false
	}
	parts := strings.Split(strings.TrimPrefix(trimmed, reviewEventCallbackPrefix), ":")
	if len(parts) != 2 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	action = ReviewEventAction(parts[1])
	if !validReviewEventAction(action) {
		return 0, "", false
	}
	return id, action, true
}

func validReviewEventAction(action ReviewEventAction) bool {
	switch action {
	case ReviewEventActionApprove, ReviewEventActionReject, ReviewEventActionParentApprove:
		return true
	default:
		return false
	}
}
