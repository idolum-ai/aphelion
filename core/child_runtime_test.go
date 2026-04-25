//go:build linux

package core

import "testing"

func TestExtractChildRuntimeContractNormalizesAndValidates(t *testing.T) {
	contract, ok, err := ExtractChildRuntimeContract(`{
		"child_runtime": {
			"executable": "mail-reader",
			"readonly_paths": ["/srv/mail/config", "/srv/mail/config"],
			"readonly_binds": [{"source":"/opt/mail/bin/mail-reader","target":"/usr/local/bin/mail-reader"}],
			"env_from_parent": ["MAIL_TOKEN"],
			"environment": ["XDG_CONFIG_HOME"]
		}
	}`, `{}`)
	if err != nil {
		t.Fatalf("ExtractChildRuntimeContract() err = %v", err)
	}
	if !ok {
		t.Fatal("ExtractChildRuntimeContract() ok = false, want true")
	}
	if contract.Executable != "mail-reader" || len(contract.ReadonlyPaths) != 1 || len(contract.ReadonlyBinds) != 1 || len(contract.EnvFromParent) != 2 {
		t.Fatalf("contract = %#v, want normalized child runtime", contract)
	}
}

func TestExtractChildRuntimeContractRejectsRelativeReadonlyPath(t *testing.T) {
	_, _, err := ExtractChildRuntimeContract(`{"child_runtime":{"readonly_paths":["relative"]}}`, `{}`)
	if err == nil {
		t.Fatal("ExtractChildRuntimeContract() err = nil, want validation error")
	}
}
