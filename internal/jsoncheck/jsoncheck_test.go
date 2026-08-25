package jsoncheck

import "testing"

func TestIsObject(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
		want  bool
	}{
		{name: "object", value: []byte(`{"name":"acore"}`), want: true},
		{name: "empty object", value: []byte(`{}`), want: true},
		{name: "object with whitespace", value: []byte(" \n {} \t"), want: true},
		{name: "nil", value: nil, want: false},
		{name: "empty", value: []byte{}, want: false},
		{name: "invalid JSON", value: []byte(`{`), want: false},
		{name: "null", value: []byte(`null`), want: false},
		{name: "array", value: []byte(`[]`), want: false},
		{name: "string", value: []byte(`"value"`), want: false},
		{name: "number", value: []byte(`1`), want: false},
		{name: "boolean", value: []byte(`true`), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsObject(test.value); got != test.want {
				t.Fatalf("IsObject() = %t, want %t", got, test.want)
			}
		})
	}
}
