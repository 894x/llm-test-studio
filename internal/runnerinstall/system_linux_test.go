package runnerinstall

import (
	"strings"
	"testing"
)

func policyFixture() map[string]string {
	return map[string]string{
		"Type": "notify", "Restart": "on-failure", "StartLimitBurst": "5", "NeedDaemonReload": "no",
		"User": serviceUser, "Group": serviceUser, "WorkingDirectory": productionLayout().state, "LimitCORE": "0",
		"WatchdogUSec": "30s", "RestartUSec": "5s", "StartLimitIntervalUSec": "5min",
	}
}

func TestEffectiveSystemdPolicyRequiresRestartWatchdogLimitsAndLoadedIdentity(t *testing.T) {
	if err := verifyPolicy(policyFixture(), productionLayout()); err != nil {
		t.Fatal("canonical systemd policy was rejected")
	}
	for key, invalid := range map[string]string{
		"Type": "simple", "Restart": "no", "WatchdogUSec": "0", "RestartUSec": "0",
		"StartLimitIntervalUSec": "0", "StartLimitBurst": "0", "NeedDaemonReload": "yes",
		"User": "root", "Group": "docker", "WorkingDirectory": "/tmp", "LimitCORE": "infinity",
	} {
		fields := policyFixture()
		fields[key] = invalid
		if err := verifyPolicy(fields, productionLayout()); err == nil {
			t.Fatalf("unsupported effective policy for %s was accepted", key)
		}
	}
}

func TestProcessIdentityRejectsSupplementaryPrivilegesAndMissingIDs(t *testing.T) {
	valid := "Name:\trunner\nUid:\t995\t995\t995\t995\nGid:\t994\t994\t994\t994\nGroups:\t994\n"
	owner := account{uid: 995, gid: 994}
	if err := verifyProcessIDs([]byte(valid), owner); err != nil {
		t.Fatal(err)
	}
	if err := verifyProcessIDs([]byte(strings.Replace(
		valid,
		"Groups:\t994",
		"Groups:",
		1,
	)), owner); err != nil {
		t.Fatal("empty supplementary groups were mistaken for elevated privileges")
	}
	for _, invalid := range []string{
		strings.ReplaceAll(valid, "995", "0"), strings.ReplaceAll(valid, "994", "0"),
		strings.Replace(
			valid,
			"Groups:\t994",
			"Groups:\t994 998",
			1,
		),
		strings.Replace(
			valid,
			"Groups:\t994\n",
			"",
			1,
		),
		valid + "Uid:\t995\t995\t995\t995\n",
	} {
		if err := verifyProcessIDs([]byte(invalid), owner); err == nil {
			t.Fatal("unexpected process privilege or incomplete identity was accepted")
		}
	}
}
