package directives

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 || Add(0, 0) != 0 || Add(-1, 1) != 0 {
		t.Fatal("Add")
	}
}

func TestSub(t *testing.T) {
	if Sub(5, 3) != 2 || Sub(0, 0) != 0 || Sub(-1, -1) != 0 {
		t.Fatal("Sub")
	}
}

func TestMagic(t *testing.T) {
	if Magic(2, 3) != 5 || Magic(0, 0) != 0 {
		t.Fatal("Magic")
	}
}

func TestPlain(t *testing.T) {
	if Plain(2, 3) != 5 || Plain(0, 0) != 0 || Plain(-1, 1) != 0 {
		t.Fatal("Plain")
	}
}

func TestMul(t *testing.T) {
	if Mul(2, 3) != 6 || Mul(0, 5) != 0 {
		t.Fatal("Mul")
	}
}
