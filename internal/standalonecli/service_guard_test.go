//go:build linux

package standalonecli

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeServiceGuard struct {
	unitList  string
	unitFiles string
	show      string
	versions  map[string]string
	readlinks map[string]string
	calls     []string
}

func (f *fakeServiceGuard) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "list-units", "--all", "--no-legend", "--plain"}) {
		return []byte(f.unitList), nil
	}
	if name == "systemctl" && reflect.DeepEqual(args, []string{"--user", "list-unit-files", "--no-legend", "--plain"}) {
		return []byte(f.unitFiles), nil
	}
	if name == "systemctl" && len(args) >= 4 && args[0] == "--user" && args[1] == "show" && args[2] == "aphelion" {
		return []byte(f.show), nil
	}
	if len(args) == 2 && args[0] == "version" && args[1] == "--json" {
		if out, ok := f.versions[name]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("missing version for " + name)
	}
	return nil, errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
}

func (f *fakeServiceGuard) readlink(path string) (string, error) {
	if out, ok := f.readlinks[path]; ok {
		return out, nil
	}
	return "", errors.New("missing readlink for " + path)
}

func TestVerifyAphelionServiceGuardPassesForExpectedBinary(t *testing.T) {
	fake := &fakeServiceGuard{
		unitList:  "aphelion.service loaded active running Aphelion\naphelion-sandbox-helper.service loaded active running helper\n",
		unitFiles: "aphelion.service enabled\naphelion-sandbox-helper.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=/opt/aphelion ; argv[]=/opt/aphelion --config /tmp/aphelion.toml }\n",
		readlinks: map[string]string{"/proc/123/exe": "/opt/aphelion"},
		versions:  map[string]string{"/opt/aphelion": `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	report, err := verifyAphelionServiceGuard(context.Background(), serviceGuardCheck{
		ExpectedExecPath: "/opt/aphelion",
		ExpectedVersion:  "v0.2.2",
		ExpectedRevision: "abc123",
		Runner:           fake.run,
		Readlink:         fake.readlink,
	})
	if err != nil {
		t.Fatalf("verifyAphelionServiceGuard() err = %v", err)
	}
	if report.RunningExecPath != "/opt/aphelion" || report.RunningRevision != "abc123" {
		t.Fatalf("report = %#v, want expected binary/revision", report)
	}
}

func TestVerifyAphelionServiceGuardFailsOnRunningBinaryMismatch(t *testing.T) {
	fake := &fakeServiceGuard{
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=/home/me/repo/bin/aphelion ; argv[]=/home/me/repo/bin/aphelion --config /tmp/aphelion.toml }\n",
		readlinks: map[string]string{"/proc/123/exe": "/home/me/repo/bin/aphelion"},
		versions:  map[string]string{"/home/me/repo/bin/aphelion": `{"version":"v0.1.6","vcs_revision":"old"}`},
	}

	_, err := verifyAphelionServiceGuard(context.Background(), serviceGuardCheck{
		ExpectedExecPath: "/home/me/.local/bin/aphelion",
		ExpectedVersion:  "v0.2.2",
		ExpectedRevision: "new",
		Runner:           fake.run,
		Readlink:         fake.readlink,
	})
	if err == nil || !strings.Contains(err.Error(), "running executable mismatch") {
		t.Fatalf("err = %v, want running executable mismatch", err)
	}
}

func TestVerifyAphelionServiceGuardFailsOnRevisionMismatch(t *testing.T) {
	fake := &fakeServiceGuard{
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "MainPID=123\nExecStart={ path=/opt/aphelion ; argv[]=/opt/aphelion --config /tmp/aphelion.toml }\n",
		readlinks: map[string]string{"/proc/123/exe": "/opt/aphelion"},
		versions:  map[string]string{"/opt/aphelion": `{"version":"v0.2.2","vcs_revision":"old"}`},
	}

	_, err := verifyAphelionServiceGuard(context.Background(), serviceGuardCheck{
		ExpectedExecPath: "/opt/aphelion",
		ExpectedVersion:  "v0.2.2",
		ExpectedRevision: "new",
		Runner:           fake.run,
		Readlink:         fake.readlink,
	})
	if err == nil || !strings.Contains(err.Error(), "running revision mismatch") {
		t.Fatalf("err = %v, want running revision mismatch", err)
	}
}

func TestVerifyAphelionServiceGuardFailsOnDuplicatePrimaryUnits(t *testing.T) {
	fake := &fakeServiceGuard{
		unitList:  "aphelion.service loaded active running Aphelion\naphelion-v013-deploy.service loaded failed failed old\naphelion-sandbox-helper.service loaded active running helper\n",
		unitFiles: "aphelion.service enabled\naphelion-main-redeploy-1779159152.service disabled\n",
		show:      "MainPID=123\nExecStart={ path=/opt/aphelion ; argv[]=/opt/aphelion }\n",
		readlinks: map[string]string{"/proc/123/exe": "/opt/aphelion"},
		versions:  map[string]string{"/opt/aphelion": `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	report, err := verifyAphelionServiceGuard(context.Background(), serviceGuardCheck{
		ExpectedExecPath: "/opt/aphelion",
		ExpectedVersion:  "v0.2.2",
		ExpectedRevision: "abc123",
		Runner:           fake.run,
		Readlink:         fake.readlink,
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate/stale Aphelion primary unit") {
		t.Fatalf("err = %v, want duplicate/stale primary unit error", err)
	}
	want := []string{"aphelion-main-redeploy-1779159152.service", "aphelion-v013-deploy.service"}
	if !reflect.DeepEqual(report.DuplicateUnitNames, want) {
		t.Fatalf("duplicates = %#v, want %#v", report.DuplicateUnitNames, want)
	}
}

func TestVerifyAphelionServiceGuardSmokeWithFakeSystemdRunner(t *testing.T) {
	fake := &fakeServiceGuard{
		unitList:  "aphelion.service loaded active running Aphelion\n",
		unitFiles: "aphelion.service enabled\n",
		show:      "LoadState=loaded\nActiveState=active\nSubState=running\nMainPID=123\nExecStart={ path=/tmp/aphelion ; argv[]=/tmp/aphelion }\n",
		readlinks: map[string]string{"/proc/123/exe": "/tmp/aphelion"},
		versions:  map[string]string{"/tmp/aphelion": `{"version":"v0.2.2","vcs_revision":"abc123"}`},
	}

	report, err := verifyAphelionServiceGuard(context.Background(), serviceGuardCheck{
		ExpectedExecPath: "/tmp/aphelion",
		ExpectedVersion:  "v0.2.2",
		ExpectedRevision: "abc123",
		Runner:           fake.run,
		Readlink:         fake.readlink,
	})
	if err != nil {
		t.Fatalf("verifyAphelionServiceGuard() err = %v", err)
	}
	if report.ActiveState != "active" || report.RunningExecPath != "/tmp/aphelion" || len(report.DuplicateUnitNames) != 0 {
		t.Fatalf("report = %#v, want active matching smoke report", report)
	}
	for _, call := range fake.calls {
		for _, forbidden := range []string{" restart", " start", " enable", " daemon-reload"} {
			if strings.Contains(call, forbidden) {
				t.Fatalf("service guard smoke invoked mutating systemctl command via %q: %#v", call, fake.calls)
			}
		}
	}
}
