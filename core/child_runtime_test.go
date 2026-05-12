//go:build linux

package core

import "testing"

func TestExtractChildRuntimeContractNormalizesAndValidates(t *testing.T) {
	contract, ok, err := ExtractChildRuntimeContract(`{
		"child_runtime": {
			"executable": "mail-reader",
			"readonly_paths": ["/srv/mail/config", "/srv/mail/config"],
			"readonly_binds": [{"source":"/opt/mail/bin/mail-reader","target":"/usr/local/bin/mail-reader"}],
			"secret_binds": [{"source":"/home/child/.secrets/mail.env","target":"/run/secrets/mail.env"}],
			"env_from_parent": ["MAIL_TOKEN"]
		}
	}`, `{}`)
	if err != nil {
		t.Fatalf("ExtractChildRuntimeContract() err = %v", err)
	}
	if !ok {
		t.Fatal("ExtractChildRuntimeContract() ok = false, want true")
	}
	if contract.Executable != "mail-reader" || len(contract.ReadonlyPaths) != 1 || len(contract.ReadonlyBinds) != 1 || len(contract.SecretBinds) != 1 || len(contract.EnvFromParent) != 1 {
		t.Fatalf("contract = %#v, want normalized child runtime", contract)
	}
}

func TestExtractChildRuntimeContractRejectsRelativeReadonlyPath(t *testing.T) {
	_, _, err := ExtractChildRuntimeContract(`{"child_runtime":{"readonly_paths":["relative"]}}`, `{}`)
	if err == nil {
		t.Fatal("ExtractChildRuntimeContract() err = nil, want validation error")
	}
}

func TestExtractChildRuntimeContractRejectsRelativeSecretBind(t *testing.T) {
	_, _, err := ExtractChildRuntimeContract(`{"child_runtime":{"secret_binds":[{"source":"relative.env","target":"/run/secrets/x.env"}]}}`, `{}`)
	if err == nil {
		t.Fatal("ExtractChildRuntimeContract() err = nil, want secret bind validation error")
	}
}

func TestExtractChildRuntimeContractRejectsRemovedRuntimeMaterialization(t *testing.T) {
	_, _, err := ExtractChildRuntimeContract(`{"runtime_materialization":{"readonly_paths":["/srv/mail"]}}`, `{}`)
	if err == nil {
		t.Fatal("ExtractChildRuntimeContract() err = nil, want removed runtime_materialization rejection")
	}
}
