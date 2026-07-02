package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestCLIPrintsVersionAndHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"nexus-lens"}, want: "nexus-lens commands: version\n"},
		{name: "version", args: []string{"nexus-lens", "version"}, want: "nexus-lens dev\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalArgs := os.Args
			os.Args = tt.args
			t.Cleanup(func() { os.Args = originalArgs })

			got := captureStdout(t, main)
			if got != tt.want {
				t.Fatalf("stdout = %q, want %q", got, tt.want)
			}
		})
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = original })

	fn()

	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, read); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
