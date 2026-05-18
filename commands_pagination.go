//go:build linux

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/idolum-ai/aphelion/core"
	"github.com/idolum-ai/aphelion/telegram"
)

const (
	telegramPageCallbackPrefix = "page:"

	telegramPageSurfaceThreads = "threads"
	telegramPageSurfaceAgents  = "agents"
	telegramPageSurfaceHealth  = "health"

	telegramPageViewList    = "list"
	telegramPageViewNonOpen = "nonopen"
	telegramPageViewTrace   = "trace"
)

type telegramPageRequest struct {
	Surface string
	View    string
	Page    int
}

type telegramPageInfo struct {
	Page      int
	PageSize  int
	Total     int
	PageCount int
	Start     int
	End       int
}

func encodeTelegramPageCallbackData(surface string, view string, page int) string {
	surface = strings.TrimSpace(surface)
	view = strings.TrimSpace(view)
	if page < 1 {
		page = 1
	}
	if !validTelegramPageSurfaceView(surface, view) {
		return ""
	}
	data := telegramPageCallbackPrefix + surface + ":" + view + ":" + strconv.Itoa(page)
	if len(data) > core.TelegramCallbackDataMaxBytes {
		return ""
	}
	return data
}

func decodeTelegramPageCallbackData(data string) (telegramPageRequest, bool) {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, telegramPageCallbackPrefix) {
		return telegramPageRequest{}, false
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, telegramPageCallbackPrefix))
	parts := strings.Split(payload, ":")
	if len(parts) != 3 {
		return telegramPageRequest{}, false
	}
	surface := strings.TrimSpace(parts[0])
	view := strings.TrimSpace(parts[1])
	page, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || page < 1 || !validTelegramPageSurfaceView(surface, view) {
		return telegramPageRequest{}, false
	}
	return telegramPageRequest{Surface: surface, View: view, Page: page}, true
}

func validTelegramPageSurfaceView(surface string, view string) bool {
	switch surface {
	case telegramPageSurfaceThreads:
		return view == telegramPageViewList || view == telegramPageViewNonOpen
	case telegramPageSurfaceAgents:
		return view == telegramPageViewList
	case telegramPageSurfaceHealth:
		return view == telegramPageViewTrace
	default:
		return false
	}
}

func telegramPageBounds(total int, page int, pageSize int) telegramPageInfo {
	if pageSize <= 0 {
		pageSize = 1
	}
	if total < 0 {
		total = 0
	}
	pageCount := 1
	if total > 0 {
		pageCount = (total + pageSize - 1) / pageSize
	}
	if page < 1 {
		page = 1
	}
	if page > pageCount {
		page = pageCount
	}
	start := 0
	end := 0
	if total > 0 {
		start = (page - 1) * pageSize
		end = start + pageSize
		if end > total {
			end = total
		}
	}
	return telegramPageInfo{
		Page:      page,
		PageSize:  pageSize,
		Total:     total,
		PageCount: pageCount,
		Start:     start,
		End:       end,
	}
}

func telegramPageItems[T any](items []T, page int, pageSize int) ([]T, telegramPageInfo) {
	info := telegramPageBounds(len(items), page, pageSize)
	if info.Total == 0 {
		return nil, info
	}
	return items[info.Start:info.End], info
}

func telegramPageNavigationRows(info telegramPageInfo, surface string, view string) [][]telegram.InlineButton {
	if info.PageCount <= 1 {
		return nil
	}
	row := make([]telegram.InlineButton, 0, 3)
	if info.Page > 1 {
		row = append(row, telegram.InlineButton{
			Text:         "Prev",
			CallbackData: encodeTelegramPageCallbackData(surface, view, info.Page-1),
		})
	}
	row = append(row, telegram.InlineButton{
		Text:         fmt.Sprintf("Page %d/%d", info.Page, info.PageCount),
		CallbackData: encodeTelegramPageCallbackData(surface, view, info.Page),
	})
	if info.Page < info.PageCount {
		row = append(row, telegram.InlineButton{
			Text:         "Next",
			CallbackData: encodeTelegramPageCallbackData(surface, view, info.Page+1),
		})
	}
	return [][]telegram.InlineButton{row}
}
