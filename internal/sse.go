//go:build linux

package internal

import (
	"bufio"
	"io"
	"strings"
)

type Event struct {
	Type string
	Data string
	ID   string
}

func ParseSSE(r io.Reader) <-chan Event {
	ch := make(chan Event)

	go func() {
		defer close(ch)

		reader := bufio.NewReader(r)
		var cur Event
		var dataLines []string
		hasPending := false

		emit := func() {
			if !hasPending {
				return
			}
			cur.Data = strings.Join(dataLines, "\n")
			ch <- cur
			cur = Event{}
			dataLines = dataLines[:0]
			hasPending = false
		}

		for {
			line, err := reader.ReadString('\n')
			if err != nil && err != io.EOF {
				emit()
				return
			}

			if strings.HasSuffix(line, "\n") {
				line = strings.TrimSuffix(line, "\n")
			}
			if strings.HasSuffix(line, "\r") {
				line = strings.TrimSuffix(line, "\r")
			}

			if line == "" {
				emit()
			} else if strings.HasPrefix(line, ":") {
				// Comment line; ignore.
			} else {
				field, value, ok := strings.Cut(line, ":")
				if ok {
					if strings.HasPrefix(value, " ") {
						value = value[1:]
					}
				} else {
					field = line
					value = ""
				}

				switch field {
				case "data":
					dataLines = append(dataLines, value)
					hasPending = true
				case "event":
					cur.Type = value
					hasPending = true
				case "id":
					cur.ID = value
					hasPending = true
				}
			}

			if err == io.EOF {
				emit()
				return
			}
		}
	}()

	return ch
}
