package otlphttp

import "testing"

func TestRegister(t *testing.T) {
	if err := Register(); err != nil {
		t.Fatal(err)
	}
}
