package runtime

import (
	"encoding/json"
	"testing"
)

func TestExchangeOwnsSelectedRequestBody(t *testing.T) {
	for _, test := range []struct {
		name   string
		actual json.RawMessage
		want   string
	}{
		{name: "template body", want: `{"model":"template"}`},
		{name: "empty actual body", actual: json.RawMessage{}, want: `{"model":"template"}`},
		{name: "bound body", actual: json.RawMessage(`{"model":"upstream"}`), want: `{"model":"upstream"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := Request{Body: json.RawMessage(`{"model":"template"}`)}
			entry := Exchange("initial", request, Response{RequestBody: test.actual})
			request.Body[0] = '!'
			if len(test.actual) > 0 {
				test.actual[0] = '!'
			}
			if string(entry.RequestBody) != test.want {
				t.Fatalf("selected request body = %s, want %s", entry.RequestBody, test.want)
			}
		})
	}
	if entry := Exchange("empty", Request{}, Response{}); entry.RequestBody == nil || len(entry.RequestBody) != 0 {
		t.Fatalf("empty request body = %#v, want non-nil empty bytes", entry.RequestBody)
	}
}
