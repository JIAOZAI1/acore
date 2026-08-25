package nilcheck

import "testing"

func TestIsNil(t *testing.T) {
	var (
		nilPointer  *int
		nilChannel  chan int
		nilFunction func()
		nilMap      map[string]string
		nilSlice    []string
	)
	nonNilPointer := new(int)

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{name: "nil interface", value: nil, want: true},
		{name: "typed nil pointer", value: nilPointer, want: true},
		{name: "typed nil channel", value: nilChannel, want: true},
		{name: "typed nil function", value: nilFunction, want: true},
		{name: "typed nil map", value: nilMap, want: true},
		{name: "typed nil slice", value: nilSlice, want: true},
		{name: "non-nil pointer", value: nonNilPointer, want: false},
		{name: "non-nil map", value: map[string]string{}, want: false},
		{name: "zero integer", value: 0, want: false},
		{name: "empty struct", value: struct{}{}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsNil(test.value); got != test.want {
				t.Fatalf("IsNil() = %t, want %t", got, test.want)
			}
		})
	}
}
