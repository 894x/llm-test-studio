package protocol

import "testing"

func TestPaidConfirmationUsesSelectedTaskCount(t *testing.T) {
	for _, test := range []struct {
		protocol string
		count    uint64
		want     bool
	}{
		{OpenAIChat, 10, false}, {KimiK3, 10, false}, {Seedance, 0, false}, {Seedance, 1, false}, {Seedance, 2, true},
		{WanVideo, 0, false}, {WanVideo, 1, true}, {MiniMaxVideo, 1, true},
	} {
		info, _ := Lookup(test.protocol)
		if got := info.RequiresPaidConfirmation(test.count); got != test.want {
			t.Fatalf("%s count=%d: confirmation=%v want=%v", test.protocol, test.count, got, test.want)
		}
	}
}
