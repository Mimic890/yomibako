package check

import "testing"

func TestCompleted(t *testing.T) {
	cases := map[string]bool{
		"Завершён":        true,
		"completed":       true,
		"Продолжается":    false,
		"":                false,
		"Выпуск завершён": true,
		"ongoing":         false,
	}
	for status, want := range cases {
		if got := Completed(status); got != want {
			t.Errorf("Completed(%q) = %v, want %v", status, got, want)
		}
	}
}
