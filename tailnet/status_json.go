//go:build linux

package tailnet

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/idolum-ai/aphelion/core"
)

type parsedStatus struct {
	Version         string
	BackendState    string
	HostName        string
	DNSName         string
	TailnetName     string
	User            string
	Online          bool
	Authenticated   bool
	TailscaleIPs    []string
	Tags            []string
	MagicDNSEnabled bool
}

type statusJSON struct {
	Version        string                     `json:"Version"`
	BackendState   string                     `json:"BackendState"`
	MagicDNSSuffix string                     `json:"MagicDNSSuffix"`
	CurrentTailnet any                        `json:"CurrentTailnet"`
	Self           statusNode                 `json:"Self"`
	User           map[string]statusUser      `json:"User"`
	Peer           map[string]json.RawMessage `json:"Peer"`
}

type statusNode struct {
	ID           string   `json:"ID"`
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	UserID       int64    `json:"UserID"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Tags         []string `json:"Tags"`
	Online       bool     `json:"Online"`
	Active       bool     `json:"Active"`
	OS           string   `json:"OS"`
}

type statusUser struct {
	ID          int64  `json:"ID"`
	LoginName   string `json:"LoginName"`
	DisplayName string `json:"DisplayName"`
}

func ParseStatusJSON(data []byte) (parsedStatus, error) {
	var raw statusJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return parsedStatus{}, err
	}
	if strings.TrimSpace(raw.BackendState) == "" && strings.TrimSpace(raw.Self.HostName) == "" && strings.TrimSpace(raw.Self.DNSName) == "" {
		return parsedStatus{}, fmt.Errorf("status JSON did not include backend or self node fields")
	}
	tailnetName := parseCurrentTailnet(raw.CurrentTailnet)
	if tailnetName == "" {
		tailnetName = deriveTailnetFromDNSName(raw.Self.DNSName)
	}
	user := ""
	if raw.Self.UserID != 0 {
		if found, ok := raw.User[fmt.Sprintf("%d", raw.Self.UserID)]; ok {
			user = firstNonEmpty(found.LoginName, found.DisplayName)
		}
	}
	return parsedStatus{
		Version:         strings.TrimSpace(raw.Version),
		BackendState:    strings.TrimSpace(raw.BackendState),
		HostName:        strings.TrimSpace(raw.Self.HostName),
		DNSName:         normalizeDNSName(raw.Self.DNSName),
		TailnetName:     strings.TrimSpace(tailnetName),
		User:            strings.TrimSpace(user),
		Online:          raw.Self.Online || raw.Self.Active,
		Authenticated:   strings.EqualFold(strings.TrimSpace(raw.BackendState), "Running"),
		TailscaleIPs:    normalizeList(raw.Self.TailscaleIPs),
		Tags:            normalizeList(raw.Self.Tags),
		MagicDNSEnabled: strings.TrimSpace(raw.Self.DNSName) != "" || strings.TrimSpace(raw.MagicDNSSuffix) != "",
	}, nil
}

func mergeParsedStatus(snapshot *core.TailnetStatusSnapshot, parsed parsedStatus) {
	if snapshot == nil {
		return
	}
	snapshot.TailscaleVersion = firstNonEmpty(snapshot.TailscaleVersion, parsed.Version)
	snapshot.BackendState = parsed.BackendState
	snapshot.HostName = parsed.HostName
	snapshot.DNSName = parsed.DNSName
	snapshot.TailnetName = parsed.TailnetName
	snapshot.User = parsed.User
	snapshot.Online = parsed.Online
	snapshot.Authenticated = parsed.Authenticated
	snapshot.TailscaleIPs = append([]string(nil), parsed.TailscaleIPs...)
	snapshot.Tags = append([]string(nil), parsed.Tags...)
	snapshot.MagicDNSEnabled = parsed.MagicDNSEnabled
}

func parseCurrentTailnet(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"Name", "name", "MagicDNSSuffix", "magicDNSSuffix", "DNSName", "dnsName"} {
			if raw, ok := v[key].(string); ok && strings.TrimSpace(raw) != "" {
				return normalizeDNSName(raw)
			}
		}
	}
	return ""
}

func deriveTailnetFromDNSName(dnsName string) string {
	parts := strings.Split(normalizeDNSName(dnsName), ".")
	if len(parts) <= 1 {
		return ""
	}
	return strings.Join(parts[1:], ".")
}

func normalizeDNSName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, ".")
	return name
}
