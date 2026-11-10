package control

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestConsoleCaptureBoundsIOCopy(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.WriteString(strings.Repeat("x", 1024*1024)); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	buffer := &boundedBuffer{max: 128}
	copied, err := io.Copy(buffer, file)
	if err != nil || copied != 1024*1024 {
		t.Fatal(copied, err)
	}
	if buffer.Value() != strings.Repeat("x", 128) {
		t.Fatal("console transport bypassed byte cap")
	}
}
