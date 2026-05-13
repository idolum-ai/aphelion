//go:build linux

package internal

import (
	"reflect"
	"strings"
	"testing"
)

func collectEvents(ch <-chan Event) []Event {
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestSSEBasic(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "single event",
			input: "event: message\nid: 42\ndata: hello\n\n",
			want:  []Event{{Type: "message", Data: "hello", ID: "42"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectEvents(ParseSSE(strings.NewReader(tt.input)))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSSEMultilineData(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "multiple data lines joined by newline",
			input: "data: first\ndata: second\ndata: third\n\n",
			want:  []Event{{Data: "first\nsecond\nthird"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectEvents(ParseSSE(strings.NewReader(tt.input)))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSSEComments(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "comment lines are ignored",
			input: ": keep-alive\ndata: hi\n: another\n\n",
			want:  []Event{{Data: "hi"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectEvents(ParseSSE(strings.NewReader(tt.input)))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSSEEmptyLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "multiple blank lines are a single boundary",
			input: "data: one\n\n\n\n",
			want:  []Event{{Data: "one"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectEvents(ParseSSE(strings.NewReader(tt.input)))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSSENoEvent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []Event
	}{
		{
			name:  "missing event field keeps empty type",
			input: "id: abc\ndata: payload\n\n",
			want:  []Event{{Type: "", Data: "payload", ID: "abc"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectEvents(ParseSSE(strings.NewReader(tt.input)))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("events = %#v, want %#v", got, tt.want)
			}
		})
	}
}
