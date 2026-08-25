package clone

import "testing"

type bytes []byte

type value struct {
	data []byte
}

func TestSlice(t *testing.T) {
	t.Run("preserves nil", func(t *testing.T) {
		var source bytes
		if cloned := Slice(source); cloned != nil {
			t.Fatalf("Slice(nil) = %#v, want nil", cloned)
		}
	})

	t.Run("copies backing array", func(t *testing.T) {
		source := bytes("acore")
		cloned := Slice(source)
		cloned[0] = 'A'
		if string(source) != "acore" {
			t.Fatalf("source = %q, want acore", source)
		}
		if string(cloned) != "Acore" {
			t.Fatalf("cloned = %q, want Acore", cloned)
		}
	})

	t.Run("preserves non-nil empty slice", func(t *testing.T) {
		source := make([]int, 0)
		cloned := Slice(source)
		if cloned == nil || len(cloned) != 0 {
			t.Fatalf("Slice(empty) = %#v, want non-nil empty slice", cloned)
		}
	})
}

func TestSliceWith(t *testing.T) {
	source := []value{{data: []byte("first")}, {data: []byte("second")}}
	cloned := SliceWith(source, func(element value) value {
		element.data = Slice(element.data)
		return element
	})

	cloned[0].data[0] = 'F'
	if string(source[0].data) != "first" {
		t.Fatalf("source nested value = %q, want first", source[0].data)
	}
	if string(cloned[0].data) != "First" {
		t.Fatalf("cloned nested value = %q, want First", cloned[0].data)
	}
}
